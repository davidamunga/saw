package doctor

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/runtest"
)

func TestMain(m *testing.M) { runtest.Main(m) }

var cfg = &config.Config{
	Path: "saw.json",
	Apps: map[string]config.App{"main": {Name: "main", Android: "com.example.app", IOS: "com.example.app"}},
}

func healthy() []runtest.Rule {
	return []runtest.Rule{
		{Cmd: "adb version", Stdout: "Android Debug Bridge version 1.0.41\nVersion 35\n"},
		{Cmd: "adb devices -l", Stdout: "List of devices attached\nS1 device model:Pixel_8\n"},
		{Cmd: "adb -s S1 shell getprop", Stdout: "14\n"},
		{Cmd: "adb -s S1 shell pm list packages com.example.app", Stdout: "package:com.example.app\n"},
		{Cmd: "xcrun --find simctl", Stdout: "/usr/bin/simctl\n"},
		{Cmd: "xcrun simctl list devices --json", Stdout: `{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-26-4": [{"udid": "SIM-1", "name": "iPhone 17 Pro", "state": "Booted", "isAvailable": true}]}}`},
		{Cmd: "xcrun simctl get_app_container SIM-1 com.example.app", Stdout: "/path\n"},
		{Cmd: "pymobiledevice3 version", Stdout: "9.12.0\n"},
		{Cmd: "pymobiledevice3 usbmux list", Stdout: `[{"ConnectionType": "USB", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "U1"}]`},
		{Cmd: "pymobiledevice3 lockdown info --udid U1", Stdout: `{"TrustedHostAttached": true}`},
		{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`},
	}
}

func report(t *testing.T, c *config.Config) (string, bool) {
	t.Helper()
	var b bytes.Buffer
	ok := Run(context.Background(), c, &b)
	return b.String(), ok
}

func TestHealthy(t *testing.T) {
	runtest.Install(t, healthy()...)
	out, ok := report(t, cfg)
	if !ok || strings.Contains(out, "✗") {
		t.Fatalf("want every check to pass:\n%s", out)
	}
	for _, want := range []string{"✓ config", "Android Debug Bridge version 1.0.41", "Pixel 8 (S1) Android 14",
		"9.12.0", "iPhone (U1) iOS 27.3, trusted", "installed on Pixel 8", "installed on iPhone"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if runtime.GOOS == "darwin" && !strings.Contains(out, "1 booted") {
		t.Errorf("want the booted simulator:\n%s", out)
	}
}

func TestProblemsAndFixes(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb version", Stdout: "Android Debug Bridge\n"},
		runtest.Rule{Cmd: "adb devices -l", Stdout: "List of devices attached\nS2 unauthorized\nS3 offline\n"},
		runtest.Rule{Cmd: "xcrun --find simctl", Exit: 1},
		runtest.Rule{Cmd: "pymobiledevice3 version", Stdout: "9.12.0\n"},
		runtest.Rule{Cmd: "pymobiledevice3 usbmux list", Stdout: `[{"ConnectionType": "USB", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "U2"}]`},
		runtest.Rule{Cmd: "pymobiledevice3 lockdown info --udid U2", Stderr: "PairingError", Exit: 1},
	)
	out, ok := report(t, &config.Config{})
	if ok {
		t.Fatalf("want failures:\n%s", out)
	}
	for _, want := range []string{"! config", "no saw.json found", `accept "Allow USB debugging"`, "S3 (S3) offline",
		"reconnect it", `"Trust This Computer"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if runtime.GOOS == "darwin" && !strings.Contains(out, "install Xcode") {
		t.Errorf("want the Xcode fix:\n%s", out)
	}
}

func TestMissingToolsAndApps(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "xcrun --find simctl", Stdout: "/usr/bin/simctl\n"},
		runtest.Rule{Cmd: "xcrun simctl list devices --json", Stdout: `{"devices": {}}`},
	)
	out, ok := report(t, cfg)
	if ok {
		t.Fatalf("missing adb and pymobiledevice3 should fail:\n%s", out)
	}
	for _, want := range []string{"✗ adb", "install Android platform-tools", "✗ pymobiledevice3", "pipx install pymobiledevice3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// Tools there, devices there, app missing: a warning, not a failure.
	rules := healthy()
	for i, r := range rules {
		if strings.Contains(r.Cmd, "pm list packages") {
			rules[i].Stdout = "package:com.other\n"
		}
		if strings.Contains(r.Cmd, "apps query") {
			rules[i].Stdout = "{}"
		}
	}
	runtest.Install(t, rules...)
	out, ok = report(t, cfg)
	if !ok || !strings.Contains(out, "not installed on Pixel 8") || !strings.Contains(out, "not installed on iPhone") {
		t.Errorf("want app warnings and an overall pass:\n%s", out)
	}
}

func TestNoDevices(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb version", Stdout: "Android Debug Bridge\n"},
		runtest.Rule{Cmd: "adb devices -l", Stdout: "List of devices attached\n"},
		runtest.Rule{Cmd: "pymobiledevice3 version", Stdout: "9.12.0\n"},
		runtest.Rule{Cmd: "pymobiledevice3 usbmux list", Stdout: "[]"},
	)
	out, _ := report(t, cfg)
	for _, want := range []string{"no devices or emulators", "none connected"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	runtest.Install(t,
		runtest.Rule{Cmd: "adb version", Stdout: "x\n"},
		runtest.Rule{Cmd: "adb devices -l", Stderr: "daemon not running", Exit: 1},
		runtest.Rule{Cmd: "pymobiledevice3 version", Stdout: "9.12.0\n"},
		runtest.Rule{Cmd: "pymobiledevice3 usbmux list", Stderr: "usbmuxd not running", Exit: 1},
	)
	out, ok := report(t, cfg)
	if ok || !strings.Contains(out, "daemon not running") || !strings.Contains(out, "usbmuxd not running") {
		t.Errorf("tool errors should be reported:\n%s", out)
	}
	_ = usbmuxFix()
}
