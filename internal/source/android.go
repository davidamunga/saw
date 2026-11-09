package source

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/run"
)

// Lines are filtered by the app's uid rather than its pid: the uid survives the app restarting
// or crashing, and it keeps out other processes' lines that share a tag (AndroidRuntime, say).
func streamAndroid(ctx context.Context, s Spec, emit func(event.Event)) error {
	if s.App.Android == "" {
		return fmt.Errorf("app %q has no android package in saw.json", s.App.Name)
	}
	serial := s.Device.ID
	uid, err := packageUID(ctx, serial, s.App.Android)
	if err != nil {
		return err
	}
	uids := uidNames(uid)

	args := []string{"-s", serial, "logcat", "-v", "threadtime", "-v", "year", "-v", "uid", "-T", s.logcatStart()}
	tagScope := map[string]string{}
	if s.TagFor != nil {
		for _, a := range s.Scopes {
			tagScope[s.TagFor(a)] = a
		}
	}
	if !s.All && len(tagScope) > 0 {
		for tag := range tagScope {
			args = append(args, tag+":V")
		}
		args = append(args, "AndroidRuntime:E", "*:S") // keep crashes
	}

	s.started()
	return run.Lines(ctx, func(line string) {
		e, lineUID, ok := ParseLogcat(line)
		if !ok || !uids[lineUID] {
			return
		}
		if !s.keepAndroidTag(e.Tag) {
			return
		}
		e.Device = s.Device.Name
		e.Process = s.App.Android
		e.Scope = tagScope[e.Tag]
		emit(e)
	}, "adb", args...)
}

// keepAndroidTag applies --scope when the project has no tag convention (no android.tag):
// the scopes are then logcat tags themselves, matched in any case. Crashes always show.
// With a convention, logcat already filtered by tag.
func (s Spec) keepAndroidTag(tag string) bool {
	if !s.Chosen || s.TagFor != nil {
		return true
	}
	return tag == "AndroidRuntime" || containsFold(s.Scopes, tag)
}

func packageUID(ctx context.Context, serial, pkg string) (int, error) {
	out, err := run.Output(ctx, "adb", "-s", serial, "shell", "pm", "list", "packages", "-U", pkg)
	if err != nil {
		return 0, err
	}
	// pm matches substrings, so find the exact package: "package:<pkg> uid:<uid>".
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "package:"+pkg {
			if uid, err := strconv.Atoi(strings.TrimPrefix(f[1], "uid:")); err == nil {
				return uid, nil
			}
		}
	}
	return 0, fmt.Errorf("%s is not installed on %s", pkg, serial)
}

// uidNames is how logcat's uid column can show an app's uid: 10710, or u0_a710 on some versions.
func uidNames(uid int) map[string]bool {
	user, app := uid/100000, uid%100000
	return map[string]bool{
		strconv.Itoa(uid):                       true,
		fmt.Sprintf("u%d_a%d", user, app-10000): true,
	}
}

// 2026-11-09 11:49:25.939 10710  9926  9926 I com.example.app: Late-enabling -Xcheck:jni
var logcatLine = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3})\s+(\S+)\s+(\d+)\s+(\d+)\s+([VDIWEFA])\s(.*?)\s*: ?(.*)$`)

// ParseLogcat reads one `logcat -v threadtime -v year -v uid` line and its uid column.
func ParseLogcat(line string) (e event.Event, uid string, ok bool) {
	m := logcatLine.FindStringSubmatch(line)
	if m == nil {
		return e, "", false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05.000", m[1], time.Local)
	if err != nil {
		return e, "", false
	}
	pid, _ := strconv.Atoi(m[3])
	tid, _ := strconv.ParseInt(m[4], 10, 64)
	return event.Event{
		Time: t, Platform: "android", PID: pid, TID: tid,
		Level: androidLevel(m[5]), Tag: strings.TrimSpace(m[6]), Message: m[7],
	}, m[2], true
}

func androidLevel(letter string) event.Level {
	switch letter {
	case "V":
		return event.Verbose
	case "D":
		return event.Debug
	case "I":
		return event.Info
	case "W":
		return event.Warn
	case "E":
		return event.Error
	}
	return event.Fatal // F, A (assert)
}
