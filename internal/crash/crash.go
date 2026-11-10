// Package crash finds an app's crash reports on a device: the crashes its own log may not show.
//
//   - Android: the system's dropbox, which keeps Java crashes, native crashes and ANRs (the app
//     not responding), each with the process and a stack trace.
//   - iOS simulator: the .ips crash reports macOS writes for simulator apps.
//   - iPhone/iPad: the device's .ips crash reports, copied off it with pymobiledevice3.
package crash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/run"
	"github.com/davidamunga/saw/internal/source"
)

// Report is one crash.
type Report struct {
	Time     time.Time `json:"time"`
	Platform string    `json:"platform"`
	Device   string    `json:"device"`
	Kind     string    `json:"kind"` // crash, native (Android native code) or anr (Android: not responding)
	Process  string    `json:"process,omitempty"`
	Summary  string    `json:"summary"`        // the exception, signal or reason, in one line
	Text     string    `json:"report"`         // the whole report, readable
	File     string    `json:"file,omitempty"` // iOS simulator: the .ips file on this computer
}

// List returns the app's crash reports on dev, newest first.
func List(ctx context.Context, dev device.Device, app config.App) ([]Report, error) {
	var reports []Report
	var err error
	switch {
	case dev.Platform == "android":
		reports, err = android(ctx, dev, app)
	case dev.Kind == "simulator":
		reports, err = simulator(dev, app)
	case dev.Platform == "ios":
		reports, err = iosDevice(ctx, dev, app)
	default:
		err = fmt.Errorf("unsupported device %s", dev)
	}
	for i := range reports {
		reports[i].Platform, reports[i].Device = dev.Platform, dev.Name
	}
	sort.SliceStable(reports, func(i, j int) bool { return reports[i].Time.After(reports[j].Time) })
	return reports, err
}

// ---- Android: dropbox ----

var dropboxTags = []struct{ tag, kind string }{
	{"data_app_crash", "crash"},
	{"data_app_native_crash", "native"},
	{"data_app_anr", "anr"},
}

func android(ctx context.Context, dev device.Device, app config.App) ([]Report, error) {
	if app.Android == "" {
		return nil, fmt.Errorf("app %q has no android package in saw.json", app.Name)
	}
	var reports []Report
	for _, t := range dropboxTags {
		out, err := run.Output(ctx, "adb", "-s", dev.ID, "shell", "dumpsys", "dropbox", "--print", t.tag)
		if err != nil {
			return nil, err
		}
		reports = append(reports, ParseDropbox(out, t.kind, app.Android)...)
	}
	return reports, nil
}

const dropboxRule = "========================================"

var (
	dropboxHead = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d) \S+ \(`)
	// #00 pc 000000000009e1e8  /apex/.../libc.so (__epoll_pwait+8) (BuildId: 5d21…)
	nativeFrame = regexp.MustCompile(`^\s*#00 pc \S+\s+(\S+)(?: \(([^)]*)\))?`)
	signalLine  = regexp.MustCompile(`^signal \d+ \((\w+)\)`)
	// Input dispatching timed out (… is not responding. Waited 5002ms for MotionEvent)
	anrSubject = regexp.MustCompile(`^(.*?) \(.*(Waited [^)]*)\)$`)
)

// ParseDropbox reads `dumpsys dropbox --print <tag>`, keeping the package's entries.
func ParseDropbox(out, kind, pkg string) []Report {
	var reports []Report
	for _, entry := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), dropboxRule+"\n") {
		head, rest, _ := strings.Cut(entry, "\n")
		m := dropboxHead.FindStringSubmatch(head)
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local)
		if err != nil {
			continue
		}
		fields, body, _ := strings.Cut(rest, "\n\n")
		info := map[string]string{}
		for _, l := range strings.Split(fields, "\n") {
			if k, v, ok := strings.Cut(l, ": "); ok {
				info[k] = strings.TrimSpace(v)
			}
		}
		process := info["Process"]
		if process != pkg && !strings.HasPrefix(process, pkg+":") {
			continue
		}
		r := Report{Time: t, Kind: kind, Process: process, Text: strings.TrimRight(entry, "\n")}
		switch kind {
		case "crash":
			r.Summary = javaSummary(body)
		case "native":
			r.Summary = nativeSummary(body)
		case "anr":
			r.Summary = anrSummary(info["Subject"], body)
			r.Text = anrText(fields, info, body)
		}
		reports = append(reports, r)
	}
	return reports
}

// javaSummary is the exception, or the last "Caused by", which is usually the real one.
func javaSummary(body string) string {
	summary := ""
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "at ") || strings.HasPrefix(l, "..."):
		case summary == "":
			summary = l
		case strings.HasPrefix(l, "Caused by: "):
			summary = strings.TrimPrefix(l, "Caused by: ")
		}
	}
	return summary
}

// nativeSummary is the abort message, or the signal and the top frame.
func nativeSummary(body string) string {
	signal, frame := "", ""
	for _, l := range strings.Split(body, "\n") {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(l), "Abort message: "); ok {
			return strings.Trim(msg, "'")
		}
		if m := signalLine.FindStringSubmatch(l); m != nil && signal == "" {
			signal = m[1]
		}
		if m := nativeFrame.FindStringSubmatch(l); m != nil && frame == "" {
			frame = filepath.Base(m[1])
			if m[2] != "" && !strings.HasPrefix(m[2], "BuildId:") {
				frame += " (" + m[2] + ")"
			}
		}
	}
	switch {
	case signal != "" && frame != "":
		return signal + " in " + frame
	case signal != "":
		return signal
	}
	return "native crash"
}

func anrSummary(subject, body string) string {
	if subject == "" {
		for _, l := range strings.Split(body, "\n") {
			if s, ok := strings.CutPrefix(l, "Subject: "); ok {
				subject = s
				break
			}
		}
	}
	if m := anrSubject.FindStringSubmatch(subject); m != nil {
		return m[1] + ": " + strings.ToLower(m[2][:1]) + m[2][1:]
	}
	if subject == "" {
		return "not responding"
	}
	return subject
}

// anrText keeps an ANR's header, its reason and the app's own threads; the rest of the entry is
// every other process on the device.
func anrText(fields string, info map[string]string, body string) string {
	pid := info["PID"]
	var b strings.Builder
	b.WriteString(fields + "\n")
	in := false
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "Subject: "):
			b.WriteString("\n" + l + "\n")
		case pid != "" && strings.HasPrefix(l, "----- pid "+pid+" "):
			in = true
			b.WriteString("\n")
		}
		if in {
			b.WriteString(l + "\n")
			if strings.HasPrefix(l, "----- end "+pid+" ") {
				in = false
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- iOS ----

// reportDirs are where macOS writes crash reports, simulator apps' included. Tests replace it.
var reportDirs = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	return []string{dir, filepath.Join(dir, "Retired")}
}

func simulator(dev device.Device, app config.App) ([]Report, error) {
	if app.IOS == "" {
		return nil, fmt.Errorf("app %q has no ios bundle id in saw.json", app.Name)
	}
	var reports []Report
	for _, dir := range reportDirs() {
		files, _ := filepath.Glob(filepath.Join(dir, "*.ips"))
		for _, f := range files {
			if !ipsHeaderMatches(f, app.IOS) {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			r, procPath, ok := ParseIPS(data, app.IOS)
			if ok && strings.Contains(procPath, "/Devices/"+dev.ID+"/") {
				r.File = f
				reports = append(reports, r)
			}
		}
	}
	return reports, nil
}

func iosDevice(ctx context.Context, dev device.Device, app config.App) ([]Report, error) {
	process, err := source.AppProcess(ctx, dev, app)
	if err != nil {
		return nil, err
	}
	out, err := run.Output(ctx, "pymobiledevice3", "crash", "ls", "--udid", dev.ID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, l := range strings.Split(out, "\n") {
		base := filepath.Base(strings.TrimSpace(l))
		if strings.HasPrefix(base, process+"-") && strings.HasSuffix(base, ".ips") {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "saw-crashes-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	match := "^" + regexp.QuoteMeta(process) + "-.*\\.ips$"
	if _, err := run.Output(ctx, "pymobiledevice3", "crash", "pull", "--udid", dev.ID, "--match", match, dir); err != nil {
		return nil, err
	}
	var reports []Report
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".ips") {
			return nil
		}
		if data, err := os.ReadFile(p); err == nil {
			if r, _, ok := ParseIPS(data, app.IOS); ok {
				reports = append(reports, r)
			}
		}
		return nil
	})
	return reports, nil
}
