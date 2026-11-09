// Package doctor checks that the tools and devices `saw` needs are ready, and says how to fix what isn't.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/run"
)

type Status int

const (
	OK Status = iota
	Warn
	Fail
)

type Check struct {
	Status Status
	Name   string
	Detail string
	Fix    string
}

// Run checks everything and reports whether nothing failed.
func Run(ctx context.Context, cfg *config.Config, w io.Writer) bool {
	ok := true
	report := func(c Check) {
		mark := [...]string{"✓", "!", "✗"}[c.Status]
		fmt.Fprintf(w, "%s %-16s %s\n", mark, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(w, "  %-16s → %s\n", "", c.Fix)
		}
		if c.Status == Fail {
			ok = false
		}
	}
	for _, c := range Checks(ctx, cfg) {
		report(c)
	}
	return ok
}

func Checks(ctx context.Context, cfg *config.Config) []Check {
	var checks []Check
	add := func(s Status, name, detail, fix string) { checks = append(checks, Check{s, name, detail, fix}) }

	if cfg.Path == "" {
		add(Warn, "config", "no saw.json found", "optional: add one to name your apps and areas, or pass --app <package or bundle id>")
	} else {
		add(OK, "config", fmt.Sprintf("%s (%d apps, %d areas)", cfg.Path, len(cfg.Apps), len(cfg.AreaSet)), "")
	}

	// Android
	var androidDevs []device.Device
	if !run.Has("adb") {
		add(Fail, "adb", "not found", "install Android platform-tools and put adb on PATH")
	} else {
		v, _ := run.Output(ctx, "adb", "version")
		add(OK, "adb", firstLine(v), "")
		devs, err := device.Android(ctx)
		if err != nil {
			add(Fail, "android", err.Error(), "")
		}
		for _, d := range devs {
			switch d.State {
			case "device":
				add(OK, "android", fmt.Sprintf("%s %s", d, d.OS), "")
				androidDevs = append(androidDevs, d)
			case "unauthorized":
				add(Fail, "android", d.String()+" unauthorized", `unlock it and accept "Allow USB debugging"`)
			default:
				add(Fail, "android", d.String()+" "+d.State, "reconnect it, or run adb kill-server")
			}
		}
		if len(devs) == 0 {
			add(Warn, "android", "no devices or emulators", "connect one with USB debugging on, or start an emulator")
		}
	}

	// iOS simulators
	var sims []device.Device
	if runtime.GOOS != "darwin" {
		add(Warn, "simulators", "need macOS (Apple limitation)", "")
	} else if _, err := run.Output(ctx, "xcrun", "--find", "simctl"); err != nil {
		add(Fail, "xcode", "simctl not found", "install Xcode, then: sudo xcode-select -s /Applications/Xcode.app")
	} else {
		var err error
		sims, err = device.Simulators(ctx, false)
		if err != nil {
			add(Fail, "simulators", err.Error(), "")
		} else {
			add(OK, "simulators", fmt.Sprintf("%d booted", len(sims)), "")
		}
	}

	// iOS devices
	var iosDevs []device.Device
	if !run.Has("pymobiledevice3") {
		add(Fail, "pymobiledevice3", "not found (needed for iPhones and iPads)", "pipx install pymobiledevice3")
	} else {
		v, _ := run.Output(ctx, "pymobiledevice3", "version")
		add(OK, "pymobiledevice3", strings.TrimSpace(v), "")
		devs, err := device.IOSDevices(ctx)
		if err != nil {
			add(Fail, "ios devices", err.Error(), usbmuxFix())
		}
		for _, d := range devs {
			if trusted(ctx, d.ID) {
				add(OK, "ios device", fmt.Sprintf("%s %s, trusted", d, d.OS), "")
				iosDevs = append(iosDevs, d)
			} else {
				add(Fail, "ios device", d.String()+" not trusted", `unlock it and tap "Trust This Computer"`)
			}
		}
		if err == nil && len(devs) == 0 {
			add(Warn, "ios devices", "none connected", "connect one with USB and unlock it")
		}
	}

	// The configured apps on each ready device
	for _, app := range cfg.Apps {
		for _, d := range androidDevs {
			if app.Android != "" {
				out, _ := run.Output(ctx, "adb", "-s", d.ID, "shell", "pm", "list", "packages", app.Android)
				installed(add, app, d, hasLine(out, "package:"+app.Android))
			}
		}
		for _, d := range sims {
			if app.IOS != "" {
				_, err := run.Output(ctx, "xcrun", "simctl", "get_app_container", d.ID, app.IOS)
				installed(add, app, d, err == nil)
			}
		}
		for _, d := range iosDevs {
			if app.IOS != "" {
				out, _ := run.Output(ctx, "pymobiledevice3", "apps", "query", "--udid", d.ID, app.IOS)
				installed(add, app, d, strings.Contains(out, `"CFBundleExecutable"`))
			}
		}
	}
	return checks
}

func installed(add func(Status, string, string, string), app config.App, d device.Device, ok bool) {
	if ok {
		add(OK, "app "+app.Name, "installed on "+d.Name, "")
	} else {
		add(Warn, "app "+app.Name, "not installed on "+d.Name, "")
	}
}

func trusted(ctx context.Context, udid string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := run.Output(ctx, "pymobiledevice3", "lockdown", "info", "--udid", udid)
	if err != nil {
		return false
	}
	var info struct{ TrustedHostAttached bool }
	return json.Unmarshal([]byte(out), &info) == nil && info.TrustedHostAttached
}

func usbmuxFix() string {
	switch runtime.GOOS {
	case "linux":
		return "install and start usbmuxd"
	case "windows":
		return "install Apple Devices (or iTunes) for its USB driver"
	}
	return ""
}

func hasLine(out, want string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
