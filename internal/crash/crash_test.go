package crash

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
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

var (
	pixel  = device.Device{Platform: "android", Kind: "device", ID: "S1", Name: "Pixel 8"}
	sim    = device.Device{Platform: "ios", Kind: "simulator", ID: "SIM-1", Name: "iPhone 17"}
	iphone = device.Device{Platform: "ios", Kind: "device", ID: "U1", Name: "iPhone"}
	app    = config.App{Name: "main", Android: "com.example.app", IOS: "com.example.app"}
)

func TestAndroid(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_crash", Stdout: fixture(t, "dropbox-crash.txt")},
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_native_crash", Stdout: fixture(t, "dropbox-native.txt")},
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_anr", Stdout: fixture(t, "dropbox-anr.txt")},
	)
	got, err := List(context.Background(), pixel, app)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range got {
		lines = append(lines, r.Time.Format("01-02 15:04:05")+" "+r.Kind+" "+r.Summary)
		if r.Device != "Pixel 8" || r.Platform != "android" || r.Process != "com.example.app" {
			t.Errorf("got %+v", r)
		}
	}
	want := []string{
		"11-10 15:02:12 native SIGSEGV in libc.so (__epoll_pwait+8)",
		"11-10 15:02:07 crash android.app.RemoteServiceException: shell-induced crash",
		"11-09 14:20:05 anr Input dispatching timed out: waited 5002ms for MotionEvent",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s", strings.Join(lines, "\n"))
	}
	// An ANR keeps the app's own threads, not every process on the device.
	anr := got[2].Text
	if !strings.Contains(anr, "SyncActivity.onCreate") || strings.Contains(anr, "system_server") || strings.Contains(anr, "CPU usage") {
		t.Errorf("ANR text:\n%s", anr)
	}

	if _, err := List(context.Background(), pixel, config.App{Name: "x", IOS: "com.x"}); err == nil {
		t.Error("want an error for an app with no android package")
	}
	runtest.Install(t, runtest.Rule{Cmd: "adb", Stderr: "device offline", Exit: 1})
	if _, err := List(context.Background(), pixel, app); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("got %v", err)
	}
}

func TestSummaries(t *testing.T) {
	for body, want := range map[string]string{
		"java.lang.RuntimeException: Unable to start activity\n\tat a.b(C.java:1)\nCaused by: java.lang.IllegalStateException: no user\n\tat x.y(Z.kt:9)\n\t... 3 more": "java.lang.IllegalStateException: no user",
		"": "",
	} {
		if got := javaSummary(body); got != want {
			t.Errorf("java: got %q, want %q", got, want)
		}
	}
	for body, want := range map[string]string{
		"signal 6 (SIGABRT), code -1\nAbort message: 'Check failed: x'\nbacktrace:\n  #00 pc 01 /lib/libc.so (abort+164)": "Check failed: x",
		"signal 11 (SIGSEGV), code 1\nbacktrace:\n  #00 pc 01 /data/app/lib/libexample.so (BuildId: ab)":                  "SIGSEGV in libexample.so",
		"signal 7 (SIGBUS)": "SIGBUS",
		"nothing":           "native crash",
	} {
		if got := nativeSummary(body); got != want {
			t.Errorf("native: got %q, want %q", got, want)
		}
	}
	if got := anrSummary("", "x\nSubject: Broadcast of Intent { act=y }\n"); got != "Broadcast of Intent { act=y }" {
		t.Errorf("got %q", got)
	}
	if got := anrSummary("", ""); got != "not responding" {
		t.Errorf("got %q", got)
	}
}

// ipsWith rewrites the simulator fixture's report with changes.
func ipsWith(t *testing.T, change func(h, b map[string]any)) []byte {
	t.Helper()
	head, body, _ := strings.Cut(fixture(t, "sim-crash.ips"), "\n")
	var h, b map[string]any
	if json.Unmarshal([]byte(head), &h) != nil || json.Unmarshal([]byte(body), &b) != nil {
		t.Fatal("bad fixture")
	}
	change(h, b)
	hj, _ := json.Marshal(h)
	bj, _ := json.Marshal(b)
	return append(append(hj, '\n'), bj...)
}

func TestParseIPS(t *testing.T) {
	r, path, ok := ParseIPS([]byte(fixture(t, "sim-crash.ips")), "com.example.app")
	if !ok || !strings.Contains(path, "/Devices/SIM-1/") {
		t.Fatalf("got %v %q", ok, path)
	}
	// The app's own code (in Example.debug.dylib) is where it happened.
	if r.Summary != "EXC_CRASH (SIGSEGV) in static ExampleApp.$main()" || r.Kind != "crash" || r.Process != "Example" {
		t.Errorf("got %+v", r)
	}
	for _, want := range []string{"Example 1.0 (1), com.example.app", "Exception: EXC_CRASH (SIGSEGV)", "Termination: Segmentation fault: 11 (by zsh)",
		"Thread 0 crashed (com.apple.main-thread):", "  0  libsystem_kernel.dylib  mach_msg2_trap + 8"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("missing %q in:\n%s", want, r.Text)
		}
	}

	// An uncaught exception: its name and reason, and the exception's backtrace.
	data := ipsWith(t, func(h, b map[string]any) {
		b["asi"] = map[string]any{"CoreFoundation": []string{"*** Terminating app due to uncaught exception 'NSRangeException', reason: 'index 3 beyond bounds'"}}
		b["lastExceptionBacktrace"] = []map[string]any{{"imageIndex": 99, "imageOffset": 4096}}
	})
	r, _, _ = ParseIPS(data, "com.example.app")
	if r.Summary != "NSRangeException: index 3 beyond bounds" || !strings.Contains(r.Text, "Last exception backtrace:\n  0  ???  + 4096") {
		t.Errorf("got %q\n%s", r.Summary, r.Text)
	}
	// A Swift fatal error.
	data = ipsWith(t, func(h, b map[string]any) {
		b["asi"] = map[string]any{"libswiftCore.dylib": []string{"Example/Cart.swift:12: Fatal error: Index out of range"}}
	})
	if r, _, _ = ParseIPS(data, "com.example.app"); r.Summary != "Example/Cart.swift:12: Fatal error: Index out of range" {
		t.Errorf("got %q", r.Summary)
	}
	// Killed by iOS: the reason.
	data = ipsWith(t, func(h, b map[string]any) {
		b["exception"] = map[string]any{"type": "EXC_CRASH", "signal": "SIGKILL"}
		b["termination"] = map[string]any{"namespace": "FRONTBOARD", "indicator": "scene-update watchdog transgression"}
		b["threads"] = []any{}
	})
	if r, _, _ = ParseIPS(data, "com.example.app"); r.Summary != "EXC_CRASH (SIGKILL): scene-update watchdog transgression" {
		t.Errorf("got %q", r.Summary)
	}
	// Not this app, not a crash, or not a report.
	for _, data := range [][]byte{
		[]byte(fixture(t, "sim-crash.ips")),
		ipsWith(t, func(h, b map[string]any) { h["bug_type"] = "298" }),
		[]byte("{}\n{"), []byte("junk"),
	} {
		if _, _, ok := ParseIPS(data, "com.other"); ok {
			t.Errorf("should not parse: %.40q", data)
		}
	}
}

func TestSimulator(t *testing.T) {
	dir := t.TempDir()
	old := reportDirs
	reportDirs = func() []string { return []string{dir, filepath.Join(dir, "missing")} }
	t.Cleanup(func() { reportDirs = old })
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Example-1.ips", []byte(fixture(t, "sim-crash.ips")))
	write("Example-2.ips", ipsWith(t, func(h, b map[string]any) { // another simulator
		b["procPath"] = "/x/Devices/SIM-2/data/Example.app/Example"
	}))
	write("Other-1.ips", ipsWith(t, func(h, b map[string]any) { h["bundleID"] = "com.other" }))
	write("empty.ips", nil)
	got, err := List(context.Background(), sim, app)
	if err != nil || len(got) != 1 || got[0].File != filepath.Join(dir, "Example-1.ips") || got[0].Device != "iPhone 17" {
		t.Errorf("got %+v %v", got, err)
	}
	if _, err := List(context.Background(), sim, config.App{Name: "x", Android: "com.x"}); err == nil {
		t.Error("want an error for an app with no ios bundle id")
	}
	if _, err := List(context.Background(), device.Device{Platform: "web"}, app); err == nil {
		t.Error("want an error for an unknown platform")
	}
}

func TestIOSDevice(t *testing.T) {
	// The fake pull writes the report where pymobiledevice3 would: into the folder it's given.
	src := filepath.Join(t.TempDir(), "Example-2026-11-10-150334.ips")
	if err := os.WriteFile(src, []byte(fixture(t, "sim-crash.ips")), 0o644); err != nil {
		t.Fatal(err)
	}
	query := runtest.Rule{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`}
	runtest.Install(t, query,
		runtest.Rule{Cmd: "pymobiledevice3 crash ls --udid U1", Stdout: "/JetsamEvent-2026-11-10-145828.ips\n/Example-2026-11-10-150334.ips\n"},
		runtest.Rule{Cmd: "pymobiledevice3 crash pull --udid U1 --match ^Example-.*\\.ips$", Copy: src},
	)
	got, err := List(context.Background(), iphone, app)
	if err != nil || len(got) != 1 || got[0].Device != "iPhone" || got[0].File != "" || !strings.Contains(got[0].Summary, "EXC_CRASH") {
		t.Errorf("got %+v %v", got, err)
	}

	runtest.Install(t, query, runtest.Rule{Cmd: "pymobiledevice3 crash ls --udid U1", Stdout: "/JetsamEvent-2026-11-10-145828.ips\n"})
	if got, err := List(context.Background(), iphone, app); err != nil || len(got) != 0 {
		t.Errorf("no reports: got %+v %v", got, err)
	}
	runtest.Install(t, query, runtest.Rule{Cmd: "pymobiledevice3 crash ls", Stderr: "not paired", Exit: 1})
	if _, err := List(context.Background(), iphone, app); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Errorf("got %v", err)
	}
	runtest.Install(t, query,
		runtest.Rule{Cmd: "pymobiledevice3 crash ls --udid U1", Stdout: "/Example-2026-11-10-150334.ips\n"},
		runtest.Rule{Cmd: "pymobiledevice3 crash pull", Stderr: "lost connection", Exit: 1},
	)
	if _, err := List(context.Background(), iphone, app); err == nil || !strings.Contains(err.Error(), "lost connection") {
		t.Errorf("got %v", err)
	}
}
