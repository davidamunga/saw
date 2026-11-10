// Package source streams a running app's logs from one device, as events.
package source

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/event"
)

// Spec says what to stream.
type Spec struct {
	Device    device.Device
	App       config.App
	Subsystem string              // iOS: the app's Logger subsystem; "" = all but Apple's own lines
	Scopes    []string            // the project's scopes; empty means no scope filter
	Chosen    bool                // Scopes were picked with --scope, not just every configured scope
	TagFor    func(string) string // Android: scope → logcat tag; nil when the project has none
	All       bool                // everything the app's process logs, not only its own lines
	Since     time.Time           // start this far back in the device's log; zero = from now
	OnStart   func()              // called once the app is found, just before lines start
	OnLive    func()              // called once the lines from Since are read and live ones follow
}

// started calls OnStart, and OnLive too unless the source reads its history separately first.
func (s Spec) started() {
	if s.OnStart != nil {
		s.OnStart()
	}
	if s.Since.IsZero() || s.Device.Kind != "simulator" {
		s.live()
	}
}

func (s Spec) live() {
	if s.OnLive != nil {
		s.OnLive()
	}
}

// containsFold reports whether list holds v, ignoring case.
func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// Stream sends each of the app's log lines to emit until ctx ends.
func Stream(ctx context.Context, s Spec, emit func(event.Event)) error {
	switch {
	case s.Device.Platform == "android":
		return streamAndroid(ctx, s, emit)
	case s.Device.Kind == "simulator":
		return streamSimulator(ctx, s, emit)
	case s.Device.Platform == "ios":
		return streamIOSDevice(ctx, s, emit)
	}
	return fmt.Errorf("unsupported device %s", s.Device)
}

func (s Spec) checkIOS() error {
	if s.App.IOS == "" {
		return fmt.Errorf("app %q has no ios bundle id in saw.json", s.App.Name)
	}
	if !s.Since.IsZero() && s.Device.Kind != "simulator" {
		return fmt.Errorf("--since isn't available on %s: iPhones and iPads only stream live logs", s.Device.Name)
	}
	return nil
}

// logcatStart is logcat's -T value: from Since (as seconds since the epoch), or from the newest
// line instead of replaying the whole buffer.
func (s Spec) logcatStart() string {
	if s.Since.IsZero() {
		return "1"
	}
	return fmt.Sprintf("%d.%03d", s.Since.Unix(), s.Since.Nanosecond()/1e6)
}

// setScope names an iOS line's scope: its category, when it's in the app's own subsystem.
func (s Spec) setScope(e *event.Event) {
	if s.Subsystem != "" && e.Subsystem == s.Subsystem {
		e.Scope = e.Category
	}
}

// keepIOS decides whether an iOS line from the app's process is the app's own.
// With a subsystem: that subsystem (and the chosen categories). Without one, for apps that log with
// NSLog, os_log or a third-party logger: lines sent from code inside the app bundle (its binary and
// the frameworks it ships), minus Apple subsystems. That drops Apple's frameworks, which log a lot
// from inside every app, often with no subsystem.
func (s Spec) keepIOS(e event.Event) bool {
	if s.All {
		return true
	}
	if s.Chosen && !containsFold(s.Scopes, e.Category) {
		return false
	}
	if s.Subsystem != "" {
		return e.Subsystem == s.Subsystem
	}
	// Apple subsystems are dropped even from inside the bundle (Xcode's debug stub logs there).
	return nsLog(e) || strings.Contains(e.Sender, ".app/") && !strings.HasPrefix(e.Subsystem, "com.apple.")
}

// nsLog reports whether a line was logged with NSLog, which logs from Foundation, not from the
// code that called it, and with no subsystem.
func nsLog(e event.Event) bool {
	return e.Subsystem == "" && path.Base(e.Sender) == "Foundation"
}
