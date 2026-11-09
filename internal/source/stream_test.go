package source

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/runtest"
)

func TestMain(m *testing.M) { runtest.Main(m) }

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func collect(t *testing.T, s Spec) ([]event.Event, error) {
	t.Helper()
	var got []event.Event
	err := Stream(context.Background(), s, func(e event.Event) { got = append(got, e) })
	return got, err
}

var (
	pixel  = device.Device{Platform: "android", Kind: "device", ID: "S1", Name: "Pixel 8", Ready: true}
	sim    = device.Device{Platform: "ios", Kind: "simulator", ID: "SIM-1", Name: "iPhone 17 Pro", Ready: true}
	iphone = device.Device{Platform: "ios", Kind: "device", ID: "U1", Name: "iPhone", Ready: true}
	app    = config.App{Name: "main", Android: "com.example.app", IOS: "com.example.app"}
)

func androidTools(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb -s S1 shell pm list packages -U com.example.app", Stdout: "package:com.example.app.debug uid:10999\npackage:com.example.app uid:10710\n"},
		runtest.Rule{Cmd: "adb -s S1 logcat", Stdout: fixture(t, "android-logcat.txt")},
	)
}

func TestStreamAndroidAllLines(t *testing.T) {
	androidTools(t)
	started := false
	got, err := collect(t, Spec{Device: pixel, App: app, OnStart: func() { started = true }})
	if err != nil {
		t.Fatal(err)
	}
	// Every line from the app's uid (10710 or u0_a710); the shell's AndroidRuntime line is dropped.
	if len(got) != 23 || !started {
		t.Fatalf("got %d events, started=%v", len(got), started)
	}
	if e := got[0]; e.Device != "Pixel 8" || e.Process != "com.example.app" || e.Platform != "android" {
		t.Errorf("got %+v", e)
	}
}

func TestStreamAndroidScopes(t *testing.T) {
	androidTools(t)
	tag := func(a string) string { return "Example" + strings.ToUpper(a[:1]) + a[1:] }
	got, err := collect(t, Spec{Device: pixel, App: app, Scopes: []string{"http", "auth"}, TagFor: tag})
	if err != nil {
		t.Fatal(err)
	}
	// The fake ignores logcat's tag filter, so check the mapping: tags become scopes.
	var scopes []string
	for _, e := range got {
		if e.Scope != "" {
			scopes = append(scopes, e.Scope)
		}
	}
	if strings.Join(scopes, ",") != "http,auth" {
		t.Errorf("got scopes %v", scopes)
	}
}

func TestStreamAndroidRawTags(t *testing.T) {
	androidTools(t)
	got, err := collect(t, Spec{Device: pixel, App: app, Scopes: []string{"examplehttp"}, Chosen: true})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, e := range got {
		tags = append(tags, e.Tag)
	}
	if strings.Join(tags, ",") != "ExampleHttp,AndroidRuntime" {
		t.Errorf("got %v", tags)
	}
}

func TestStreamAndroidErrors(t *testing.T) {
	androidTools(t)
	if _, err := collect(t, Spec{Device: pixel, App: config.App{Name: "x", IOS: "com.x"}}); err == nil || !strings.Contains(err.Error(), "no android package") {
		t.Errorf("got %v", err)
	}
	other := config.App{Name: "o", Android: "com.other"}
	runtest.Install(t, runtest.Rule{Cmd: "adb -s S1 shell pm list packages -U com.other", Stdout: "\n"})
	if _, err := collect(t, Spec{Device: pixel, App: other}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("got %v", err)
	}
	runtest.Install(t, runtest.Rule{Cmd: "adb -s S1 shell pm", Stderr: "device offline", Exit: 1})
	if _, err := collect(t, Spec{Device: pixel, App: app}); err == nil || !strings.Contains(err.Error(), "device offline") {
		t.Errorf("got %v", err)
	}
}

func TestStreamSimulator(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "xcrun simctl appinfo SIM-1 com.example.app", Stdout: "{\n    CFBundleExecutable = Example;\n}\n"},
		runtest.Rule{Cmd: "xcrun simctl spawn SIM-1 log stream", Stdout: fixture(t, "sim-logstream.ndjson")},
	)
	got, err := collect(t, Spec{Device: sim, App: app, Subsystem: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Scope != "auth" || got[1].Scope != "calls" || got[0].Device != "iPhone 17 Pro" {
		t.Errorf("got %+v", got)
	}
	runtest.Install(t, runtest.Rule{Cmd: "xcrun simctl appinfo", Exit: 1})
	if _, err := collect(t, Spec{Device: sim, App: app}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("got %v", err)
	}
}

func TestStreamIOSDevice(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`},
		runtest.Rule{Cmd: "pymobiledevice3 syslog live --udid U1", Stdout: fixture(t, "ios-device.ndjson")},
	)
	// No saw.json: only lines from code inside the app bundle, minus Apple subsystems.
	got, err := collect(t, Spec{Device: iphone, App: app})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Category != "http" || got[0].Scope != "" {
		t.Errorf("got %+v", got)
	}
	// With the convention, the subsystem's category becomes the scope.
	got, _ = collect(t, Spec{Device: iphone, App: app, Subsystem: "com.example.app"})
	if len(got) != 1 || got[0].Scope != "http" {
		t.Errorf("got %+v", got)
	}
	// --all keeps everything the process logs.
	got, _ = collect(t, Spec{Device: iphone, App: app, All: true})
	if len(got) != 31 {
		t.Errorf("--all: got %d", len(got))
	}
}

func TestStreamIOSDeviceErrors(t *testing.T) {
	runtest.Install(t, runtest.Rule{Cmd: "pymobiledevice3 apps query", Stdout: "{}"})
	if _, err := collect(t, Spec{Device: iphone, App: app}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("got %v", err)
	}
	runtest.Install(t, runtest.Rule{Cmd: "pymobiledevice3 apps query", Stderr: "not paired", Exit: 1})
	if _, err := collect(t, Spec{Device: iphone, App: app}); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Errorf("got %v", err)
	}
	if _, err := collect(t, Spec{Device: iphone, App: config.App{Name: "x", Android: "com.x"}}); err == nil || !strings.Contains(err.Error(), "no ios bundle id") {
		t.Errorf("got %v", err)
	}
	if _, err := collect(t, Spec{Device: device.Device{Platform: "web"}}); err == nil {
		t.Error("want an error for an unknown platform")
	}
}

func TestParsersRejectJunk(t *testing.T) {
	if _, _, ok := ParseLogcat("--------- beginning of main"); ok {
		t.Error("logcat banner should not parse")
	}
	if _, _, ok := ParseLogcat("2026-13-45 99:99:99.999 1 2 3 I Tag: bad date"); ok {
		t.Error("bad date should not parse")
	}
	for _, l := range []string{"", "{", `{"eventType":"logEvent","timestamp":"yesterday"}`} {
		if _, ok := ParseUnifiedLog([]byte(l)); ok {
			t.Errorf("%q should not parse", l)
		}
	}
	for _, l := range []string{"", "not json", `{"timestamp":"soon"}`} {
		if _, ok := ParseDeviceLog([]byte(l)); ok {
			t.Errorf("%q should not parse", l)
		}
	}
	if androidLevel("V") != event.Verbose || androidLevel("A") != event.Fatal || iosLevel("Fault") != event.Fatal {
		t.Error("levels")
	}
	if simExecutable("nothing here") != "" || deviceExecutable([]byte("nope"), "x") != "" {
		t.Error("lookups should come back empty")
	}
}
