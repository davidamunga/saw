package source

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/run"
)

// streamSimulator uses the simulator's own `log stream`, filtered on the simulator side.
func streamSimulator(ctx context.Context, s Spec, emit func(event.Event)) error {
	if err := s.checkIOS(); err != nil {
		return err
	}
	udid := s.Device.ID
	process, err := simProcess(ctx, s)
	if err != nil {
		return err
	}
	predicate := simPredicate(s, process)
	s.started()
	return run.Lines(ctx, func(line string) {
		e, ok := ParseUnifiedLog([]byte(line))
		if !ok {
			return
		}
		if !s.keepIOS(e) {
			return
		}
		e.Device = s.Device.Name
		s.setScope(&e)
		emit(e)
	}, "xcrun", "simctl", "spawn", udid, "log", "stream", "--style", "ndjson", "--level", "debug", "--predicate", predicate)
}

func simPredicate(s Spec, process string) string {
	p := fmt.Sprintf("process == %q", process)
	if s.All {
		return p
	}
	// Without a subsystem, which lines are the app's is decided in keepIOS.
	if s.Subsystem != "" {
		p += fmt.Sprintf(" AND subsystem == %q", s.Subsystem)
	}
	if s.Chosen {
		quoted := make([]string, len(s.Scopes))
		for i, a := range s.Scopes {
			quoted[i] = fmt.Sprintf("%q", a)
		}
		p += fmt.Sprintf(" AND category IN {%s}", strings.Join(quoted, ", "))
	}
	return p
}

// simProcess is the app's executable name on a simulator, which is also its process name.
func simProcess(ctx context.Context, s Spec) (string, error) {
	if s.App.Process != "" {
		return s.App.Process, nil
	}
	out, err := run.Output(ctx, "xcrun", "simctl", "appinfo", s.Device.ID, s.App.IOS)
	if p := simExecutable(out); err == nil && p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s is not installed on %s", s.App.IOS, s.Device)
}

// deviceProcess is the app's executable name on an iPhone or iPad.
func deviceProcess(ctx context.Context, s Spec) (string, error) {
	if s.App.Process != "" {
		return s.App.Process, nil
	}
	out, err := run.Output(ctx, "pymobiledevice3", "apps", "query", "--udid", s.Device.ID, s.App.IOS)
	if err != nil {
		return "", err
	}
	if p := deviceExecutable([]byte(out), s.App.IOS); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s is not installed on %s", s.App.IOS, s.Device)
}

var simExecutableRe = regexp.MustCompile(`CFBundleExecutable = "?([^";]+)"?;`)

// simExecutable reads CFBundleExecutable from `simctl appinfo`'s plist-style output.
func simExecutable(out string) string {
	if m := simExecutableRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// ParseUnifiedLog reads one line of `log stream --style ndjson`. Its first line is plain text
// ("Filtering the log data using ..."), which is skipped.
func ParseUnifiedLog(line []byte) (event.Event, bool) {
	var l struct {
		Timestamp        string
		EventType        string
		MessageType      string
		Subsystem        string
		Category         string
		ProcessImagePath string
		SenderImagePath  string
		ProcessID        int
		ThreadID         int64
		EventMessage     string
	}
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &l) != nil || l.EventType != "logEvent" {
		return event.Event{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05.000000-0700", l.Timestamp)
	if err != nil {
		return event.Event{}, false
	}
	return event.Event{
		Time: t, Platform: "ios", Process: filepath.Base(l.ProcessImagePath), Sender: l.SenderImagePath, PID: l.ProcessID, TID: l.ThreadID,
		Level: iosLevel(l.MessageType), Subsystem: l.Subsystem, Category: l.Category, Message: l.EventMessage,
	}, true
}

// streamIOSDevice reads an iPhone or iPad through pymobiledevice3, which keeps each line's
// subsystem and category (idevicesyslog drops them). Filtering on those happens here.
func streamIOSDevice(ctx context.Context, s Spec, emit func(event.Event)) error {
	if err := s.checkIOS(); err != nil {
		return err
	}
	udid := s.Device.ID
	process, err := deviceProcess(ctx, s)
	if err != nil {
		return err
	}
	s.started()
	return run.Lines(ctx, func(line string) {
		e, ok := ParseDeviceLog([]byte(line))
		if !ok {
			return
		}
		if !s.keepIOS(e) {
			return
		}
		s.setScope(&e)
		e.Device = s.Device.Name
		emit(e)
	}, "pymobiledevice3", "syslog", "live", "--udid", udid, "--format", "json", "--process-name", process)
}

func deviceExecutable(data []byte, bundle string) string {
	var apps map[string]struct{ CFBundleExecutable string }
	if json.Unmarshal(data, &apps) != nil {
		return ""
	}
	return apps[bundle].CFBundleExecutable
}

// ParseDeviceLog reads one line of `pymobiledevice3 syslog live --format json`.
func ParseDeviceLog(line []byte) (event.Event, bool) {
	var l struct {
		PID       int    `json:"pid"`
		Timestamp string `json:"timestamp"`
		Level     string `json:"level"`
		Filename  string `json:"filename"`
		ImageName string `json:"image_name"`
		Message   string `json:"message"`
		Label     *struct {
			Subsystem string `json:"subsystem"`
			Category  string `json:"category"`
		} `json:"label"`
	}
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &l) != nil {
		return event.Event{}, false
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05.999999", l.Timestamp, time.Local)
	if err != nil {
		return event.Event{}, false
	}
	e := event.Event{Time: t, Platform: "ios", Process: filepath.Base(l.Filename), Sender: l.ImageName, PID: l.PID,
		Level: iosLevel(l.Level), Message: l.Message}
	if l.Label != nil {
		e.Subsystem, e.Category = l.Label.Subsystem, l.Label.Category
	}
	return e, true
}

// iosLevel maps Apple's levels (log stream's "Default", pymobiledevice3's "NOTICE", ...).
func iosLevel(s string) event.Level {
	switch strings.ToLower(s) {
	case "debug":
		return event.Debug
	case "error":
		return event.Error
	case "fault":
		return event.Fatal
	}
	return event.Info // info, default, notice
}
