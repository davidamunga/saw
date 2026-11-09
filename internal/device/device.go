// Package device finds Android devices and emulators, iOS simulators and iPhones/iPads.
package device

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/davidamunga/saw/internal/run"
)

type Device struct {
	Platform string `json:"platform"` // android | ios
	Kind     string `json:"kind"`     // device | emulator | simulator
	ID       string `json:"id"`       // adb serial or UDID
	Name     string `json:"name"`
	OS       string `json:"os,omitempty"`
	State    string `json:"state"`
	Ready    bool   `json:"ready"` // can stream logs now
}

func (d Device) String() string { return fmt.Sprintf("%s (%s)", d.Name, d.ID) }

// List returns every device the installed tools can see; with all, also shut-down simulators.
// A missing tool is skipped; a tool that fails is reported in errs.
func List(ctx context.Context, all bool) (devs []Device, errs []error) {
	add := func(ds []Device, err error) {
		devs = append(devs, ds...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if run.Has("adb") {
		add(Android(ctx))
	}
	if runtime.GOOS == "darwin" && run.Has("xcrun") {
		add(Simulators(ctx, all))
	}
	if run.Has("pymobiledevice3") {
		add(IOSDevices(ctx))
	}
	sort.SliceStable(devs, func(i, j int) bool {
		a, b := devs[i], devs[j]
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.Ready != b.Ready {
			return a.Ready
		}
		return a.Name < b.Name
	})
	return devs, errs
}

// Android lists `adb devices -l`.
func Android(ctx context.Context) ([]Device, error) {
	out, err := run.Output(ctx, "adb", "devices", "-l")
	if err != nil {
		return nil, err
	}
	devs := ParseADBDevices(out)
	for i, d := range devs {
		if d.Ready {
			if v, err := run.Output(ctx, "adb", "-s", d.ID, "shell", "getprop", "ro.build.version.release"); err == nil {
				devs[i].OS = "Android " + strings.TrimSpace(v)
			}
		}
	}
	return devs, nil
}

func ParseADBDevices(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "List of") {
			continue
		}
		d := Device{Platform: "android", Kind: "device", ID: f[0], Name: f[0], State: f[1], Ready: f[1] == "device"}
		if strings.HasPrefix(d.ID, "emulator-") {
			d.Kind = "emulator"
		}
		for _, kv := range f[2:] {
			if model, ok := strings.CutPrefix(kv, "model:"); ok {
				d.Name = strings.ReplaceAll(model, "_", " ")
			}
		}
		devs = append(devs, d)
	}
	return devs
}

// Simulators lists booted simulators, or every available one with all.
func Simulators(ctx context.Context, all bool) ([]Device, error) {
	out, err := run.Output(ctx, "xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return nil, err
	}
	return ParseSimctl([]byte(out), all)
}

func ParseSimctl(data []byte, all bool) ([]Device, error) {
	var list struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			State       string `json:"state"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("simctl list: %w", err)
	}
	var devs []Device
	for rt, sims := range list.Devices {
		for _, s := range sims {
			ready := s.State == "Booted"
			if !s.IsAvailable || (!all && !ready) {
				continue
			}
			devs = append(devs, Device{Platform: "ios", Kind: "simulator", ID: s.UDID, Name: s.Name,
				OS: runtimeName(rt), State: strings.ToLower(s.State), Ready: ready})
		}
	}
	return devs, nil
}

// runtimeName turns com.apple.CoreSimulator.SimRuntime.iOS-26-4 into iOS 26.4.
func runtimeName(id string) string {
	id = id[strings.LastIndex(id, ".")+1:]
	os, version, ok := strings.Cut(id, "-")
	if !ok {
		return id
	}
	return os + " " + strings.ReplaceAll(version, "-", ".")
}

// IOSDevices lists iPhones and iPads over usbmuxd, which works on macOS, Linux and Windows.
func IOSDevices(ctx context.Context) ([]Device, error) {
	out, err := run.Output(ctx, "pymobiledevice3", "usbmux", "list")
	if err != nil {
		return nil, err
	}
	return ParseUsbmux([]byte(out))
}

func ParseUsbmux(data []byte) ([]Device, error) {
	var list []struct {
		ConnectionType string
		DeviceClass    string
		DeviceName     string
		ProductVersion string
		UniqueDeviceID string
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("pymobiledevice3 usbmux list: %w", err)
	}
	byID := map[string]int{}
	var devs []Device
	for _, l := range list {
		d := Device{Platform: "ios", Kind: "device", ID: l.UniqueDeviceID, Name: l.DeviceName,
			OS: "iOS " + l.ProductVersion, State: "connected", Ready: true}
		if l.DeviceClass == "iPad" {
			d.OS = "iPadOS " + l.ProductVersion
		}
		if l.ConnectionType != "USB" {
			d.State = "connected (wifi)"
		}
		// A device on USB and Wi-Fi shows twice: keep one, preferring USB.
		if i, seen := byID[d.ID]; seen {
			if l.ConnectionType == "USB" {
				devs[i] = d
			}
			continue
		}
		byID[d.ID] = len(devs)
		devs = append(devs, d)
	}
	return devs, nil
}

// AmbiguousError means more than one device fits and the user has to choose.
type AmbiguousError struct{ Candidates []Device }

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	b.WriteString("more than one device; pass --device with one of:")
	for _, d := range e.Candidates {
		fmt.Fprintf(&b, "\n  %-24s %s", d.ID, d.Name)
	}
	return b.String()
}

// Pick chooses the device to stream: the one --device names (id, id prefix or name),
// or with no --device the only ready one. platform ("android", "ios" or "") narrows the choice.
func Pick(devs []Device, query, platform string) (Device, error) {
	var pool []Device
	for _, d := range devs {
		if platform == "" || d.Platform == platform {
			pool = append(pool, d)
		}
	}
	if query != "" {
		q := strings.ToLower(query)
		var matches []Device
		for _, d := range pool {
			id := strings.ToLower(d.ID)
			if id == q || strings.EqualFold(d.Name, query) || (len(q) >= 4 && strings.HasPrefix(id, q)) {
				matches = append(matches, d)
			}
		}
		// Nothing exact: part of a name ("pixel", "17 pro").
		if len(matches) == 0 {
			for _, d := range pool {
				if strings.Contains(strings.ToLower(d.Name), q) {
					matches = append(matches, d)
				}
			}
		}
		switch len(matches) {
		case 0:
			return Device{}, fmt.Errorf("no device matches %q (see `saw devices --all`)", query)
		case 1:
			if !matches[0].Ready {
				return Device{}, fmt.Errorf("%s is %s", matches[0], matches[0].State)
			}
			return matches[0], nil
		}
		pool = matches
	}
	var ready []Device
	for _, d := range pool {
		if d.Ready {
			ready = append(ready, d)
		}
	}
	switch len(ready) {
	case 0:
		return Device{}, fmt.Errorf("no running device, emulator or booted simulator found (try `saw doctor`)")
	case 1:
		return ready[0], nil
	}
	return Device{}, &AmbiguousError{Candidates: ready}
}
