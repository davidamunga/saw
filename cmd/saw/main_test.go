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
	"time"

	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/event"
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

	cfg := writeConfig(t, `{"apps": {"main": {"android": "com.example.app"}}, "scopes": ["http", "auth"], "android": {"tag": "Example{Scope}"}}`)
	out, _, err = saw(t, "", "stream", "--config", cfg, "--scope", "http", "--platform", "android", "--device", "pixel")
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
	cfg := writeConfig(t, `{"apps": {"main": {"android": "com.example.app"}}, "scopes": ["http"]}`)
	if _, _, err := saw(t, "", "stream", "--config", cfg, "--scope", "nope"); !isUsage(err) {
		t.Errorf("unknown scope: %v", err)
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
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 logcat -v threadtime -v year -v uid -d"})
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 logcat", Stdout: "", Hang: true})
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 shell while", Stdout: "SAW-TCP\n   1: 0E00A8C0:B7E0 097100CB:01BB 01 0 0 0 10710 0 0\nSAW-UDP\nSAW-END\n"})
	runtest.Install(t, rules...)
	t.Chdir(t.TempDir())
	out, errOut, err := saw(t, "", "net", "--app", "com.example.app")
	if err != nil || !strings.Contains(out, "TCP                         203.0.113.9:443") || !strings.Contains(errOut, "network on Pixel 8") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
	if out, _, err := saw(t, "", "net", "--app", "com.example.app", "--full"); err != nil || !strings.HasPrefix(out, "# ") || !strings.Contains(out, "203.0.113.9:443") {
		t.Errorf("--full: %q %v", out, err)
	}
	if out, _, err := saw(t, "", "net", "--app", "com.example.app", "--curl"); err != nil || !strings.HasPrefix(out, "# ") {
		t.Errorf("--curl: %q %v", out, err)
	}
	if _, _, err := saw(t, "", "net", "--app", "com.example.app", "--full", "--json"); !isUsage(err) {
		t.Errorf("two formats: want a usage error, got %v", err)
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

func TestQuietConnections(t *testing.T) {
	var got []string
	q := &quietConnections{next: printFunc(func(n event.Net) { got = append(got, n.Kind) })}
	for _, k := range []string{"tcp", "dns", "logged", "tcp", "tls", "request", "log", "logged"} {
		q.Print(event.Net{Kind: k})
	}
	if strings.Join(got, ",") != "tcp,dns,logged,log,logged" {
		t.Errorf("connections should stop once the app logs a request: %v", got)
	}
}

type printFunc func(event.Net)

func (f printFunc) Print(n event.Net) { f(n) }

func TestStreamFilters(t *testing.T) {
	runtest.Install(t, androidRules(t)...)
	t.Chdir(t.TempDir())
	stream := func(args ...string) (string, error) {
		out, _, err := saw(t, "", append([]string{"stream", "--app", "com.example.app"}, args...)...)
		return out, err
	}
	for _, c := range []struct {
		args  []string
		lines int
	}{
		{[]string{"--grep", "keystore"}, 4},       // lower case: any case, and tags match too
		{[]string{"--grep", "KeyStore"}, 4},       // capitals: exact case
		{[]string{"--grep", "keyStore"}, 0},       // ... so this matches nothing
		{[]string{"--grep", "slow|refreshed"}, 3}, // a regular expression
		{[]string{"--exclude-tag", "keystoresecuritylevel,CheckTime"}, 17},
		{[]string{"--until", "session refreshed"}, 22}, // stops after the match
	} {
		out, err := stream(c.args...)
		if n := strings.Count(out, "\n"); err != nil || n != c.lines {
			t.Errorf("%v: got %d lines, %v:\n%s", c.args, n, err, out)
		}
	}
	if out, _ := stream("--until", "session refreshed"); !strings.HasSuffix(out, "session refreshed\n") {
		t.Errorf("--until should stop at the match: %q", out)
	}
	if _, err := stream("--until", "nope"); err == nil || isUsage(err) || !strings.Contains(err.Error(), `nothing matched --until "nope"`) {
		t.Errorf("no match: got %v", err)
	}
	for _, args := range [][]string{{"--grep", "("}, {"--until", "[a"}, {"--since", "yesterday"}, {"--since", "-5m"}, {"--for", "-1s"}} {
		if _, err := stream(args...); !isUsage(err) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
}

func TestStreamForAndSince(t *testing.T) {
	rules := androidRules(t)[:3]
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 logcat", Stdout: fixture(t, "android-logcat.txt"), Hang: true})
	runtest.Install(t, rules...)
	t.Chdir(t.TempDir())
	start := time.Now()
	out, errOut, err := saw(t, "", "stream", "--app", "com.example.app", "--for", "300ms", "--since", "5m")
	if err != nil || strings.Count(out, "\n") != 23 || !strings.Contains(errOut, "(for 300ms)") {
		t.Errorf("got %d lines, %q, %v", strings.Count(out, "\n"), errOut, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("--for 300ms took %s", d)
	}
	_, _, err = saw(t, "", "stream", "--app", "com.example.app", "--for", "300ms", "--until", "never")
	if err == nil || !strings.Contains(err.Error(), `nothing matched --until "never" in 300ms`) {
		t.Errorf("got %v", err)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"":         {},
		"5m":       now.Add(-5 * time.Minute),
		"90s":      now.Add(-90 * time.Second),
		"09:40":    time.Date(2026, 11, 9, 9, 40, 0, 0, time.Local),
		"09:40:30": time.Date(2026, 11, 9, 9, 40, 30, 0, time.Local),
		"11:00":    time.Date(2026, 11, 8, 11, 0, 0, 0, time.Local), // not yet today: yesterday
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%q: got %v %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"0s", "-1m", "25:00", "soon"} {
		if _, err := parseSince(in, now); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

// twoPhones fakes two Android phones with the app; one logs at :02, the other at :01.
func twoPhones(t *testing.T, extra ...runtest.Rule) {
	rules := append(extra,
		runtest.Rule{Cmd: "adb devices -l", Stdout: "List of devices attached\nS1 device model:Pixel_8\nS2 device model:Pixel_9\n"},
		runtest.Rule{Cmd: "adb -s S1 shell getprop", Stdout: "14\n"},
		runtest.Rule{Cmd: "adb -s S2 shell getprop", Stdout: "15\n"},
		runtest.Rule{Cmd: "adb -s S1 shell pm list packages -U com.example.app", Stdout: "package:com.example.app uid:10710\n"},
		runtest.Rule{Cmd: "adb -s S2 shell pm list packages -U com.example.app", Stdout: "package:com.example.app uid:10710\n"},
		runtest.Rule{Cmd: "adb -s S1 logcat", Stdout: "2026-11-09 11:50:02.000 10710 1 1 I One: from the first\n"},
		runtest.Rule{Cmd: "adb -s S2 logcat", Stdout: "2026-11-09 11:50:01.000 10710 1 1 I Two: from the second\n"},
	)
	runtest.Install(t, rules...)
	t.Chdir(t.TempDir())
	old := settleTime
	settleTime = time.Second
	t.Cleanup(func() { settleTime = old })
}

func TestStreamSeveralDevices(t *testing.T) {
	twoPhones(t)
	// With --since, history is sorted across devices.
	out, errOut, err := saw(t, "", "stream", "--app", "com.example.app", "--device", "all", "--since", "1h")
	want := "11:50:01.000 Pixel 9 I Two                from the second\n" +
		"11:50:02.000 Pixel 8 I One                from the first\n"
	if err != nil || out != want {
		t.Errorf("got:\n%s\nwant:\n%s\n%v", out, want, err)
	}
	if !strings.Contains(errOut, "on 2 devices (Ctrl-C to stop)") || !strings.Contains(errOut, "saw: com.example.app on Pixel 9 Android 15\n") {
		t.Errorf("got stderr %q", errOut)
	}
	// Named with commas; without --since, lines show as they come.
	out, _, err = saw(t, "", "stream", "--app", "com.example.app", "--device", "s1, pixel 9", "--json")
	if err != nil || strings.Count(out, "\n") != 2 || !strings.Contains(out, `"device":"Pixel 9"`) {
		t.Errorf("got %q %v", out, err)
	}
	// The same device twice is one device: no device column.
	out, _, _ = saw(t, "", "stream", "--app", "com.example.app", "--device", "S1,S1")
	if out != "11:50:02.000 I One                from the first\n" {
		t.Errorf("got %q", out)
	}
	for _, args := range [][]string{{"--device", "all", "--platform", "ios"}, {"--device", "S1,nope"}} {
		if _, _, err := saw(t, "", append([]string{"stream", "--app", "com.example.app"}, args...)...); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

func TestStreamSeveralDevicesOneFails(t *testing.T) {
	twoPhones(t, runtest.Rule{Cmd: "adb -s S2 shell pm list packages", Stdout: "\n"})
	out, errOut, err := saw(t, "", "stream", "--app", "com.example.app", "--device", "all", "--since", "1h")
	if err != nil || !strings.Contains(out, "from the first") || !strings.Contains(errOut, "saw: Pixel 9: com.example.app is not installed") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
	twoPhones(t, runtest.Rule{Cmd: "adb -s S1 shell pm", Stderr: "offline", Exit: 1},
		runtest.Rule{Cmd: "adb -s S2 shell pm", Stderr: "offline", Exit: 1})
	if _, _, err := saw(t, "", "stream", "--app", "com.example.app", "--device", "S1,S2"); err == nil || !strings.Contains(err.Error(), "no device could be read") {
		t.Errorf("got %v", err)
	}
}

func TestNetFilters(t *testing.T) {
	n := event.Net{Method: "POST", Status: 402, URL: "https://api.example.com/v1/payments", ResponseBody: `{"error":"card_declined"}`}
	for _, want := range []string{"POST 402 https://api.example.com/v1/payments", "card_declined"} {
		if !strings.Contains(netText(n), want) {
			t.Errorf("missing %q in %q", want, netText(n))
		}
	}
	var got []string
	q := &quietConnections{next: printFunc(func(n event.Net) { got = append(got, n.Device+":"+n.Kind) })}
	for _, n := range []event.Net{{Device: "a", Kind: "logged"}, {Device: "a", Kind: "tcp"}, {Device: "b", Kind: "tcp"}} {
		q.Print(n)
	}
	if strings.Join(got, ",") != "a:logged,b:tcp" {
		t.Errorf("connections should be quieted per device: %v", got)
	}
}

func TestNetHAR(t *testing.T) {
	rules := androidRules(t)[:3]
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 logcat -v threadtime -v year -v uid -d"})
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 logcat", Stdout: "2026-11-09 11:50:02.000 10710 1 7 D OkHttp: --> GET https://api.example.com/v1/orders\n" +
		"2026-11-09 11:50:02.300 10710 1 7 D OkHttp: <-- 200 https://api.example.com/v1/orders (300ms)\n" +
		"2026-11-09 11:50:02.300 10710 1 7 D OkHttp: <-- END HTTP\n"})
	// The poller waits, so the stream ends when logcat's lines are all read.
	rules = append(rules, runtest.Rule{Cmd: "adb -s S1 shell while", Stdout: "SAW-TCP\nSAW-UDP\nSAW-END\n", Hang: true})
	runtest.Install(t, rules...)
	t.Chdir(t.TempDir())
	_, errOut, err := saw(t, "", "net", "--app", "com.example.app", "--har", "s.har")
	data, _ := os.ReadFile("s.har")
	if err != nil || !strings.Contains(errOut, "saw: saved 1 request to s.har") || !strings.Contains(string(data), `"url": "https://api.example.com/v1/orders"`) {
		t.Errorf("got %q %v\n%s", errOut, err, data)
	}
	if _, _, err := saw(t, "", "net", "--app", "com.example.app", "--har", "missing/s.har"); err == nil {
		t.Error("want an error for a folder that doesn't exist")
	}
}

func crashRules(t *testing.T) []runtest.Rule {
	return []runtest.Rule{
		{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_crash", Stdout: fixture(t, "dropbox-crash.txt")},
		{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_native_crash", Stdout: fixture(t, "dropbox-native.txt")},
		{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_anr", Stdout: fixture(t, "dropbox-anr.txt")},
	}
}

func TestCrashes(t *testing.T) {
	runtest.Install(t, append(crashRules(t), androidRules(t)...)...)
	t.Chdir(t.TempDir())
	out, errOut, err := saw(t, "", "crashes", "--app", "com.example.app")
	want := "2026-11-10 15:02:12  native  SIGSEGV in libc.so (__epoll_pwait+8)\n" +
		"2026-11-10 15:02:07  crash   android.app.RemoteServiceException: shell-induced crash\n" +
		"2026-11-09 14:20:05  anr     Input dispatching timed out: waited 5002ms for MotionEvent\n"
	if err != nil || out != want || !strings.Contains(errOut, "saw: 3 reports for com.example.app on Pixel 8") {
		t.Errorf("got:\n%s%q %v", out, errOut, err)
	}
	out, _, _ = saw(t, "", "crashes", "--app", "com.example.app", "--last")
	if !strings.HasPrefix(out, "# 2026-11-10 15:02:12  Pixel 8  native  SIGSEGV") || !strings.Contains(out, "backtrace:") {
		t.Errorf("--last: %q", out)
	}
	out, _, _ = saw(t, "", "crashes", "--app", "com.example.app", "--json")
	if strings.Count(out, "\n") != 3 || !strings.Contains(out, `"kind":"native"`) || !strings.Contains(out, `"report":"`) {
		t.Errorf("--json: %q", out)
	}
	// --since leaves out older reports; with none left, saw says so.
	oldNow := now
	now = func() time.Time { return time.Date(2026, 11, 10, 16, 0, 0, 0, time.Local) }
	t.Cleanup(func() { now = oldNow })
	out, _, _ = saw(t, "", "crashes", "--app", "com.example.app", "--since", "15:02:10")
	if strings.Count(out, "\n") != 1 {
		t.Errorf("--since: %q", out)
	}
	out, errOut, err = saw(t, "", "crashes", "--app", "com.example.app", "--since", "1s")
	if err != nil || out != "" || !strings.Contains(errOut, "no crashes for com.example.app on Pixel 8") {
		t.Errorf("none: %q %q %v", out, errOut, err)
	}
	for _, args := range [][]string{{"--last", "--json"}, {"--since", "soon"}, {"extra"}} {
		if _, _, err := saw(t, "", append([]string{"crashes", "--app", "com.example.app"}, args...)...); !isUsage(err) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
}

func TestCrashesSeveralDevices(t *testing.T) {
	twoPhones(t, append(crashRules(t), runtest.Rule{Cmd: "adb -s S2 shell dumpsys", Stderr: "offline", Exit: 1})...)
	out, errOut, err := saw(t, "", "crashes", "--app", "com.example.app", "--device", "all")
	if err != nil || !strings.HasPrefix(out, "2026-11-10 15:02:12  Pixel 8  native") || !strings.Contains(errOut, "saw: Pixel 9: ") {
		t.Errorf("got %q %q %v", out, errOut, err)
	}
	twoPhones(t, runtest.Rule{Cmd: "adb -s S1 shell dumpsys", Exit: 1}, runtest.Rule{Cmd: "adb -s S2 shell dumpsys", Exit: 1})
	if _, _, err := saw(t, "", "crashes", "--app", "com.example.app", "--device", "all"); err == nil || !strings.Contains(err.Error(), "no device could be read") {
		t.Errorf("got %v", err)
	}
	twoPhones(t, runtest.Rule{Cmd: "adb -s S1 shell dumpsys", Stderr: "offline", Exit: 1})
	if _, _, err := saw(t, "", "crashes", "--app", "com.example.app", "--device", "S1"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("one device: got %v", err)
	}
}
