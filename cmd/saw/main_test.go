package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/runtest"
)

func TestMain(m *testing.M) { runtest.Main(m) }

// saw runs the CLI with stdin, returning stdout, stderr and the error.
func saw(t *testing.T, in string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	oldOut, oldErr, oldIn := stdout, stderr, stdin
	stdout, stderr, stdin = &out, &errOut, strings.NewReader(in)
	t.Cleanup(func() { stdout, stderr, stdin = oldOut, oldErr, oldIn })
	err := runCLI(context.Background(), args)
	return out.String(), errOut.String(), err
}

func isUsage(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func androidRules(t *testing.T) []runtest.Rule {
	return []runtest.Rule{
		{Cmd: "adb devices -l", Stdout: "List of devices attached\nS1 device model:Pixel_8\n"},
		{Cmd: "adb -s S1 shell getprop", Stdout: "14\n"},
		{Cmd: "adb -s S1 shell pm list packages -U com.example.app", Stdout: "package:com.example.app uid:10710\n"},
		{Cmd: "adb -s S1 logcat", Stdout: fixture(t, "android-logcat.txt")},
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "saw.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelpVersionUnknown(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		out, _, err := saw(t, "", args...)
		if err != nil || !strings.Contains(out, "saw stream") {
			t.Errorf("%v: got %q %v", args, out, err)
		}
	}
	out, _, _ := saw(t, "", "version")
	if !strings.HasPrefix(out, "saw ") {
		t.Errorf("got %q", out)
	}
	old := version
	version = "9.9.9"
	t.Cleanup(func() { version = old })
	if out, _, _ := saw(t, "", "--version"); out != "saw 9.9.9\n" {
		t.Errorf("got %q", out)
	}
	if _, _, err := saw(t, "", "launch"); !isUsage(err) {
		t.Errorf("unknown command should be a usage error: %v", err)
	}
}

func TestDevices(t *testing.T) {
	runtest.Install(t, androidRules(t)...)
	out, _, err := saw(t, "", "devices")
	if err != nil || !strings.Contains(out, "PLATFORM") || !strings.Contains(out, "Pixel 8") || !strings.Contains(out, "Android 14") {
		t.Errorf("got %q %v", out, err)
	}
	out, _, err = saw(t, "", "devices", "--json")
	var devs []device.Device
	if err != nil || json.Unmarshal([]byte(out), &devs) != nil || len(devs) != 1 || devs[0].ID != "S1" {
		t.Errorf("got %q %v", out, err)
	}
	if _, _, err := saw(t, "", "devices", "--nope"); !isUsage(err) {
		t.Errorf("bad flag: %v", err)
	}
	if _, _, err := saw(t, "", "devices", "extra"); !isUsage(err) {
		t.Errorf("extra argument: %v", err)
	}
}

func TestDevicesNone(t *testing.T) {
	runtest.Install(t, runtest.Rule{Cmd: "adb devices -l", Stderr: "daemon not running", Exit: 1})
	out, errOut, err := saw(t, "", "devices")
	if err != nil || out != "" || !strings.Contains(errOut, "no devices found") || !strings.Contains(errOut, "daemon not running") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
	if out, _, _ := saw(t, "", "devices", "--json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("want an empty JSON list, got %q", out)
	}
}

func TestStreamAndroid(t *testing.T) {
	runtest.Install(t, androidRules(t)...)
	t.Chdir(t.TempDir()) // no saw.json
	out, errOut, err := saw(t, "", "stream", "--app", "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "\n"); n != 23 {
		t.Errorf("want 23 lines, got %d:\n%s", n, out)
	}
	if !strings.Contains(errOut, "saw: com.example.app on Pixel 8 Android 14 (Ctrl-C to stop)") {
		t.Errorf("got stderr %q", errOut)
	}

	out, _, _ = saw(t, "", "stream", "--app", "com.example.app", "--level", "error", "--json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var e struct{ Level, Tag string }
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &e) != nil || e.Level != "error" || e.Tag != "AndroidRuntime" {
		t.Errorf("want just the crash line as JSON, got %q", out)
	}

	cfg := writeConfig(t, `{"apps": {"main": {"android": "com.example.app"}}, "areas": ["http", "auth"], "android": {"tag": "Example{Area}"}}`)
	out, _, err = saw(t, "", "stream", "--config", cfg, "--area", "http", "--platform", "android", "--device", "pixel")
	if err != nil || !strings.Contains(out, "http") {
		t.Errorf("got %q %v", out, err)
	}
}

func TestStreamIOSPrivateHint(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "pymobiledevice3 usbmux list", Stdout: `[{"ConnectionType": "USB", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "U1"}]`},
		runtest.Rule{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`},
		runtest.Rule{Cmd: "pymobiledevice3 syslog live", Stdout: fixture(t, "ios-device.ndjson")},
	)
	t.Chdir(t.TempDir())
	out, errOut, err := saw(t, "", "stream", "--app", "com.example.app")
	if err != nil || !strings.Contains(out, "failed: <private>") || !strings.Contains(errOut, "privacy: .public") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
}

func TestStreamErrors(t *testing.T) {
	runtest.Install(t, androidRules(t)...)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"stream", "--app", "com.x", "--platform", "web"},
		{"stream", "--app", "com.x", "--level", "loud"},
		{"stream"}, // no --app and no saw.json
		{"stream", "--app", "com.x", "stray"},
	} {
		if _, _, err := saw(t, "", args...); !isUsage(err) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
	cfg := writeConfig(t, `{"apps": {"main": {"android": "com.example.app"}}, "areas": ["http"]}`)
	if _, _, err := saw(t, "", "stream", "--config", cfg, "--area", "nope"); !isUsage(err) {
		t.Errorf("unknown area: %v", err)
	}
	if _, _, err := saw(t, "", "stream", "--config", writeConfig(t, "{"), "--app", "com.x"); err == nil || isUsage(err) {
		t.Errorf("broken saw.json: %v", err)
	}
	if _, _, err := saw(t, "", "stream", "--app", "com.x", "--platform", "ios"); err == nil || !strings.Contains(err.Error(), "no running device") {
		t.Errorf("no iOS device: %v", err)
	}
}

func TestStreamAmbiguousDevice(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb devices -l", Stdout: "List of devices attached\nS1 device model:Pixel_8\nS2 device model:Pixel_9\n"},
		runtest.Rule{Cmd: "adb -s S1 shell getprop", Stdout: "14\n"},
		runtest.Rule{Cmd: "adb -s S2 shell getprop", Stdout: "15\n"},
	)
	t.Chdir(t.TempDir())
	// Not a terminal, so no prompt: the error lists the choices.
	_, _, err := saw(t, "", "stream", "--app", "com.x")
	if err == nil || !strings.Contains(err.Error(), "S1") || !strings.Contains(err.Error(), "S2") {
		t.Errorf("got %v", err)
	}
}

func TestChoose(t *testing.T) {
	devs := []device.Device{{ID: "S1", Name: "Pixel 8"}, {ID: "S2", Name: "Pixel 9"}}
	oldIn, oldErr := stdin, stderr
	t.Cleanup(func() { stdin, stderr = oldIn, oldErr })
	var prompt bytes.Buffer
	stderr = &prompt
	stdin = strings.NewReader("2\n")
	if d, err := choose(devs); err != nil || d.ID != "S2" || !strings.Contains(prompt.String(), "2) Pixel 9") {
		t.Errorf("got %v %v, prompt %q", d, err, prompt.String())
	}
	stdin = strings.NewReader("seven\n")
	if _, err := choose(devs); !isUsage(err) {
		t.Errorf("got %v", err)
	}
}

func TestDoctor(t *testing.T) {
	ok := []runtest.Rule{
		{Cmd: "adb version", Stdout: "Android Debug Bridge\n"},
		{Cmd: "adb devices -l", Stdout: "List of devices attached\n"},
		{Cmd: "xcrun --find simctl", Stdout: "/usr/bin/simctl\n"},
		{Cmd: "xcrun simctl list devices --json", Stdout: `{"devices": {}}`},
		{Cmd: "pymobiledevice3 version", Stdout: "9.12.0\n"},
		{Cmd: "pymobiledevice3 usbmux list", Stdout: "[]"},
	}
	runtest.Install(t, ok...)
	t.Chdir(t.TempDir())
	out, _, err := saw(t, "", "doctor")
	if err != nil || !strings.Contains(out, "✓ adb") {
		t.Errorf("got %q %v", out, err)
	}
	runtest.Install(t)
	if _, _, err := saw(t, "", "doctor"); err == nil || !strings.Contains(err.Error(), "some checks failed") {
		t.Errorf("missing tools: %v", err)
	}
	if _, _, err := saw(t, "", "doctor", "--config", writeConfig(t, "{")); err == nil {
		t.Error("broken saw.json should fail")
	}
	if _, _, err := saw(t, "", "doctor", "extra"); !isUsage(err) {
		t.Errorf("got %v", err)
	}
}

func TestHelpers(t *testing.T) {
	var l listFlag
	l.Set("a, b,")
	l.Set("c")
	if l.String() != "a,b,c" {
		t.Error(l.String())
	}
	var b bytes.Buffer
	if isTerminal(&b) || wantColor(&b) {
		t.Error("a buffer is not a terminal")
	}
	if v := versionString(); v == "" {
		t.Error("empty version")
	}
}

func TestNet(t *testing.T) {
	rules := androidRules(t)[:3]
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 shell while", Stdout: "SAW-TCP\n   1: 0E00A8C0:B7E0 097100CB:01BB 01 0 0 0 10710 0 0\nSAW-UDP\nSAW-END\n"})
	runtest.Install(t, rules...)
	t.Chdir(t.TempDir())
	out, errOut, err := saw(t, "", "net", "--app", "com.example.app")
	if err != nil || !strings.Contains(out, "TCP  203.0.113.9:443") || !strings.Contains(errOut, "network on Pixel 8") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
	out, _, _ = saw(t, "", "net", "--app", "com.example.app", "--json")
	if !strings.Contains(out, `"kind":"tcp"`) || !strings.Contains(out, `"ip":"203.0.113.9"`) {
		t.Errorf("got %q", out)
	}
	for _, args := range [][]string{{"net", "--platform", "web"}, {"net"}, {"net", "--app", "com.x", "stray"}} {
		if _, _, err := saw(t, "", args...); !isUsage(err) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
	if _, _, err := saw(t, "", "net", "--app", "com.x", "--platform", "ios"); err == nil {
		t.Error("no iOS device should fail")
	}
}
