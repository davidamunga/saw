package device

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/runtest"
)

func TestMain(m *testing.M) { runtest.Main(m) }

const simJSON = `{"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-26-4": [
  {"udid": "SIM-1", "name": "iPhone 17 Pro", "state": "Booted", "isAvailable": true},
  {"udid": "SIM-2", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true}]}}`

const usbmuxJSON = `[{"ConnectionType": "USB", "DeviceClass": "iPhone", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "UDID-1"}]`

func allTools() []runtest.Rule {
	return []runtest.Rule{
		{Cmd: "adb devices -l", Stdout: "List of devices attached\nSERIAL-1 device usb:1 model:Pixel_8\nemulator-5554 unauthorized\n"},
		{Cmd: "adb -s SERIAL-1 shell getprop ro.build.version.release", Stdout: "14\n"},
		{Cmd: "xcrun simctl list devices --json", Stdout: simJSON},
		{Cmd: "pymobiledevice3 usbmux list", Stdout: usbmuxJSON},
	}
}

func TestList(t *testing.T) {
	runtest.Install(t, allTools()...)
	devs, errs := List(context.Background(), false)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	var got []string
	for _, d := range devs {
		got = append(got, d.Platform+"/"+d.Kind+"/"+d.Name+"/"+d.OS+"/"+d.State)
	}
	want := []string{"android/device/Pixel 8/Android 14/device", "android/emulator/emulator-5554//unauthorized", "ios/device/iPhone/iOS 27.3/connected"}
	if runtime.GOOS == "darwin" {
		want = []string{want[0], want[1], "ios/device/iPhone/iOS 27.3/connected", "ios/simulator/iPhone 17 Pro/iOS 26.4/booted"}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if runtime.GOOS == "darwin" {
		all, _ := List(context.Background(), true)
		if len(all) != len(devs)+1 {
			t.Errorf("--all should add the shut-down simulator: %d vs %d", len(all), len(devs))
		}
	}
}

func TestListReportsToolFailures(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb devices -l", Stderr: "adb: daemon failed", Exit: 1},
		runtest.Rule{Cmd: "xcrun simctl list devices --json", Stdout: "not json"},
		runtest.Rule{Cmd: "pymobiledevice3 usbmux list", Stdout: "{"},
	)
	devs, errs := List(context.Background(), false)
	want := 2
	if runtime.GOOS == "darwin" {
		want = 3
	}
	if len(devs) != 0 || len(errs) != want {
		t.Errorf("want %d errors and no devices, got %v %v", want, devs, errs)
	}
}

func TestListWithoutTools(t *testing.T) {
	runtest.Install(t)
	devs, errs := List(context.Background(), false)
	if len(devs) != 0 || len(errs) != 0 {
		t.Errorf("no tools: want nothing, got %v %v", devs, errs)
	}
}

func TestPickErrors(t *testing.T) {
	devs := []Device{
		{Platform: "android", ID: "emulator-5554", Name: "emulator-5554", State: "offline"},
		{Platform: "ios", Kind: "simulator", ID: "SIM-1", Name: "iPhone 17", Ready: true},
		{Platform: "ios", Kind: "simulator", ID: "SIM-2", Name: "iPhone 17", Ready: true},
	}
	if _, err := Pick(devs, "emulator-5554", ""); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("want 'offline', got %v", err)
	}
	if _, err := Pick(devs, "nothing", ""); err == nil || !strings.Contains(err.Error(), "no device matches") {
		t.Errorf("got %v", err)
	}
	if _, err := Pick(devs, "", "android"); err == nil || !strings.Contains(err.Error(), "no running device") {
		t.Errorf("got %v", err)
	}
	_, err := Pick(devs, "iphone 17", "")
	amb, ok := err.(*AmbiguousError)
	if !ok || len(amb.Candidates) != 2 || !strings.Contains(amb.Error(), "SIM-1") || !strings.Contains(amb.Error(), "SIM-2") {
		t.Errorf("same-named simulators should be ambiguous, got %v", err)
	}
	if d := (Device{ID: "X", Name: "Pixel"}); d.String() != "Pixel (X)" {
		t.Error(d.String())
	}
}

func TestRuntimeName(t *testing.T) {
	if got := runtimeName("com.apple.CoreSimulator.SimRuntime.watchOS-11-2"); got != "watchOS 11.2" {
		t.Error(got)
	}
	if got := runtimeName("weird"); got != "weird" {
		t.Error(got)
	}
}
