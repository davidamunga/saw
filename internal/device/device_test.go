package device

import (
	"errors"
	"testing"
)

func TestParseADBDevices(t *testing.T) {
	out := "* daemon not running; starting now at tcp:5037\nList of devices attached\n" +
		"38291FDJH00ABC    device usb:18022400X product:ShadowcatPlus_00WW model:Pixel_8 device:SCP_sprout transport_id:1\n" +
		"emulator-5554          offline transport_id:2\n\n"
	devs := ParseADBDevices(out)
	if len(devs) != 2 {
		t.Fatalf("got %+v", devs)
	}
	if d := devs[0]; d.Name != "Pixel 8" || !d.Ready || d.Kind != "device" {
		t.Errorf("got %+v", d)
	}
	if d := devs[1]; d.Kind != "emulator" || d.Ready || d.State != "offline" {
		t.Errorf("got %+v", d)
	}
}

func TestParseSimctl(t *testing.T) {
	data := []byte(`{"devices": {
	  "com.apple.CoreSimulator.SimRuntime.iOS-26-4": [
	    {"udid": "A", "name": "iPhone 17 Pro", "state": "Booted", "isAvailable": true},
	    {"udid": "B", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true},
	    {"udid": "C", "name": "Old", "state": "Shutdown", "isAvailable": false}]}}`)
	booted, _ := ParseSimctl(data, false)
	if len(booted) != 1 || booted[0].OS != "iOS 26.4" || !booted[0].Ready {
		t.Errorf("got %+v", booted)
	}
	all, _ := ParseSimctl(data, true)
	if len(all) != 2 {
		t.Errorf("got %+v", all)
	}
}

func TestParseUsbmuxPrefersUSB(t *testing.T) {
	data := []byte(`[
	  {"ConnectionType": "Network", "DeviceClass": "iPhone", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "U1"},
	  {"ConnectionType": "USB", "DeviceClass": "iPhone", "DeviceName": "iPhone", "ProductVersion": "27.3", "UniqueDeviceID": "U1"},
	  {"ConnectionType": "USB", "DeviceClass": "iPad", "DeviceName": "iPad", "ProductVersion": "27.2", "UniqueDeviceID": "U2"}]`)
	devs, err := ParseUsbmux(data)
	if err != nil || len(devs) != 2 || devs[0].State != "connected" || devs[1].OS != "iPadOS 27.2" {
		t.Errorf("got %+v %v", devs, err)
	}
}

func TestPick(t *testing.T) {
	devs := []Device{
		{Platform: "android", ID: "38291FDJH00ABC", Name: "Pixel 8", Ready: true},
		{Platform: "ios", Kind: "device", ID: "00008120-000A1B2C3D4E5F6A", Name: "iPhone", Ready: true},
		{Platform: "ios", Kind: "simulator", ID: "4C2D9E1A", Name: "iPhone 17 Pro", State: "shutdown"},
	}
	var amb *AmbiguousError
	if _, err := Pick(devs, "", ""); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Errorf("want ambiguous, got %v", err)
	}
	if d, err := Pick(devs, "", "android"); err != nil || d.Name != "Pixel 8" {
		t.Errorf("got %v %v", d, err)
	}
	if d, err := Pick(devs, "00008120", ""); err != nil || d.Name != "iPhone" {
		t.Errorf("prefix: got %v %v", d, err)
	}
	if d, err := Pick(devs, "pixel 8", ""); err != nil || d.ID != "38291FDJH00ABC" {
		t.Errorf("name: got %v %v", d, err)
	}
	if d, err := Pick(devs, "pixel", ""); err != nil || d.ID != "38291FDJH00ABC" {
		t.Errorf("partial name: got %v %v", d, err)
	}
	if _, err := Pick(devs, "iPhone 17 Pro", ""); err == nil {
		t.Error("shut-down simulator should not be picked")
	}
}
