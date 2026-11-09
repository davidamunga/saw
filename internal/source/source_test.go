package source

import (
	"bufio"
	"os"
	"testing"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/event"
)

func lines(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestParseLogcatFixture(t *testing.T) {
	ls := lines(t, "android-logcat.txt")
	for _, l := range ls {
		if _, _, ok := ParseLogcat(l); !ok {
			t.Errorf("did not parse: %s", l)
		}
	}
	e, uid, _ := ParseLogcat(ls[20])
	if uid != "10710" || e.Tag != "ExampleHttp" || e.Level != event.Debug || e.PID != 9926 || e.TID != 9987 ||
		e.Message != "GET /v1/appointments 200 (143 ms)" || e.Time.Format("15:04:05.000") != "11:50:02.118" {
		t.Errorf("got %+v uid %s", e, uid)
	}
}

func TestAndroidUIDFilter(t *testing.T) {
	uids := uidNames(10710)
	var kept []string
	for _, l := range lines(t, "android-logcat.txt") {
		if e, uid, ok := ParseLogcat(l); ok && uids[uid] {
			kept = append(kept, e.Tag)
		}
	}
	// Every line but the shell's AndroidRuntime one; u0_a710 counts as the app.
	if len(kept) != 23 || kept[21] != "ExampleAuth" || kept[22] != "AndroidRuntime" {
		t.Errorf("kept %d: %v", len(kept), kept)
	}
}

func TestParseDeviceLogFixture(t *testing.T) {
	ls := lines(t, "ios-device.ndjson")
	for _, l := range ls {
		if _, ok := ParseDeviceLog([]byte(l)); !ok {
			t.Errorf("did not parse: %s", l)
		}
	}
	e, _ := ParseDeviceLog([]byte(ls[len(ls)-1]))
	if e.Subsystem != "com.example.app" || e.Category != "http" || e.Level != event.Error ||
		e.Process != "Example" || e.PID != 2122 {
		t.Errorf("got %+v", e)
	}
	if e, _ := ParseDeviceLog([]byte(ls[1])); e.Subsystem != "" || e.Level != event.Info {
		t.Errorf("unlabelled line: %+v", e)
	}
}

func TestParseUnifiedLogFixture(t *testing.T) {
	var got []event.Event
	for _, l := range lines(t, "sim-logstream.ndjson") {
		if e, ok := ParseUnifiedLog([]byte(l)); ok {
			got = append(got, e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 log events (header and activity skipped), got %d", len(got))
	}
	if got[0].Category != "auth" || got[0].Level != event.Info || got[0].Process != "Example" || got[0].PID != 4021 {
		t.Errorf("got %+v", got[0])
	}
	if got[1].Level != event.Fatal || got[1].Message != "call dropped\nreason: timeout" {
		t.Errorf("got %+v", got[1])
	}
}

func TestSimPredicate(t *testing.T) {
	s := Spec{Subsystem: "com.x.app", Areas: []string{"api", "auth"}}
	if got := simPredicate(s, "App"); got != `process == "App" AND subsystem == "com.x.app"` {
		t.Error(got)
	}
	s.Chosen = true
	if got := simPredicate(s, "App"); got != `process == "App" AND subsystem == "com.x.app" AND category IN {"api", "auth"}` {
		t.Error(got)
	}
	s.All = true
	if got := simPredicate(s, "App"); got != `process == "App"` {
		t.Error(got)
	}
}

func TestExecutableLookups(t *testing.T) {
	if got := simExecutable("{\n    CFBundleExecutable = Example;\n    CFBundleIdentifier = \"com.x\";\n}"); got != "Example" {
		t.Error(got)
	}
	if got := deviceExecutable([]byte(`{"com.x": {"CFBundleExecutable": "Example"}}`), "com.x"); got != "Example" {
		t.Error(got)
	}
	if got := deviceExecutable([]byte(`{}`), "com.x"); got != "" {
		t.Error(got)
	}
}

func TestTagFor(t *testing.T) {
	c := &config.Config{}
	c.Android.Tag = "Example{Area}"
	if got := c.TagFor()("api"); got != "ExampleApi" {
		t.Error(got)
	}
}

func TestKeepIOSWithoutConvention(t *testing.T) {
	const app = "/private/var/containers/Bundle/Application/X/MyApp.app/"
	s := Spec{} // no saw.json: lines from the app's bundle, minus Apple subsystems
	for _, c := range []struct {
		name string
		e    event.Event
		want bool
	}{
		{"NSLog from the app", event.Event{Sender: app + "MyApp"}, true},
		{"os_log in a shipped framework", event.Event{Sender: app + "Frameworks/React.framework/React"}, true},
		{"Logger with the app's subsystem", event.Event{Subsystem: "com.x.app", Sender: app + "MyApp.debug.dylib"}, true},
		{"third-party subsystem", event.Event{Subsystem: "com.facebook.react.log", Sender: app + "Frameworks/React.framework/React"}, true},
		{"Apple framework, own subsystem", event.Event{Subsystem: "PrototypeTools", Sender: "/System/Library/PrivateFrameworks/PrototypeTools.framework/PrototypeTools"}, false},
		{"Apple framework, no subsystem", event.Event{Sender: "/System/Library/Frameworks/Security.framework/Security"}, false},
		{"Xcode's debug stub in the bundle", event.Event{Subsystem: "com.apple.dt.previews", Sender: app + "MyApp"}, false},
		{"Apple subsystem", event.Event{Subsystem: "com.apple.UIKit", Sender: "/System/Library/PrivateFrameworks/UIKitCore.framework/UIKitCore"}, false},
	} {
		if got := s.keepIOS(c.e); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	if got := simPredicate(s, "MyApp"); got != `process == "MyApp"` {
		t.Error(got)
	}
	s = Spec{Subsystem: "com.x.app"} // the convention: only the app's subsystem
	if s.keepIOS(event.Event{Sender: app + "MyApp"}) || !s.keepIOS(event.Event{Subsystem: "com.x.app"}) {
		t.Error("subsystem filter")
	}
}
