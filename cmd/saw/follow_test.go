package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/device"
)

// following sets up a watch on dev with fast polling, and devices listed from lists in turn.
func following(t *testing.T, dev device.Device, lists ...[]device.Device) (*watch, *bytes.Buffer) {
	t.Helper()
	var errOut bytes.Buffer
	oldErr, oldList, oldPoll, oldQuick, oldOn := stderr, listDevices, pollEvery, quickFailure, reconnect
	stderr, pollEvery, quickFailure, reconnect = &errOut, time.Millisecond, time.Hour, true
	n := 0
	listDevices = func(context.Context, bool) ([]device.Device, []error) {
		l := lists[min(n, len(lists)-1)]
		n++
		return l, nil
	}
	t.Cleanup(func() {
		stderr, listDevices, pollEvery, quickFailure, reconnect = oldErr, oldList, oldPoll, oldQuick, oldOn
	})
	return &watch{devs: []device.Device{dev}, live: map[string]bool{}}, &errOut
}

func TestFollowReconnects(t *testing.T) {
	pixel := device.Device{Platform: "android", ID: "S1", Name: "Pixel 8", OS: "Android 14", Ready: true}
	// Gone, then offline, then back.
	w, errOut := following(t, pixel, nil, []device.Device{{ID: "S1", State: "offline"}}, []device.Device{pixel})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sinces []time.Time
	before := time.Now()
	err := w.follow(ctx, pixel, func(ctx context.Context, d device.Device, since time.Time) error {
		sinces = append(sinces, since)
		w.onStart("app", d)()
		if len(sinces) == 1 {
			return errors.New("adb -s S1 logcat: exit status 1\nerror: device offline")
		}
		cancel() // Ctrl-C
		return nil
	})
	if err != nil || len(sinces) != 2 {
		t.Fatalf("got %v after %d reads", err, len(sinces))
	}
	// Android keeps its log, so the second read starts from when the device went away.
	if !sinces[0].IsZero() || sinces[1].Before(before) {
		t.Errorf("sinces: %v", sinces)
	}
	got := errOut.String()
	for _, want := range []string{"saw: lost Pixel 8 (error: device offline); waiting for it to come back (Ctrl-C to stop)\n", "saw: Pixel 8 is back\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestFollowIPhoneStartsLive(t *testing.T) {
	iphone := device.Device{Platform: "ios", Kind: "device", ID: "U1", Name: "iPhone", Ready: true}
	w, _ := following(t, iphone, []device.Device{iphone})
	w.since = time.Now().Add(-time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sinces []time.Time
	_ = w.follow(ctx, iphone, func(ctx context.Context, d device.Device, since time.Time) error {
		sinces = append(sinces, since)
		w.onStart("app", d)()
		if len(sinces) == 2 {
			cancel()
		}
		return nil
	})
	if len(sinces) != 2 || sinces[0].IsZero() || !sinces[1].IsZero() {
		t.Errorf("iPhones can't look back, so a reconnect starts live: %v", sinces)
	}
}

func TestFollowErrors(t *testing.T) {
	pixel := device.Device{Platform: "android", ID: "S1", Name: "Pixel 8", Ready: true}
	// Failing before the stream starts is an error, with no waiting.
	w, errOut := following(t, pixel, []device.Device{pixel})
	err := w.follow(context.Background(), pixel, func(context.Context, device.Device, time.Time) error {
		return errors.New("com.example.app is not installed")
	})
	if err == nil || errOut.Len() != 0 {
		t.Errorf("got %v %q", err, errOut)
	}
	// A stream that keeps ending right after it starts gives up.
	w, _ = following(t, pixel, []device.Device{pixel})
	quickFailure = time.Hour
	reads := 0
	err = w.follow(context.Background(), pixel, func(ctx context.Context, d device.Device, _ time.Time) error {
		reads++
		w.onStart("app", d)()
		return nil
	})
	if err == nil || reads != 3 || !strings.Contains(err.Error(), "keeps ending") {
		t.Errorf("got %v after %d reads", err, reads)
	}
	// Ctrl-C while waiting stops quietly.
	w, _ = following(t, pixel, nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err = w.follow(ctx, pixel, func(ctx context.Context, d device.Device, _ time.Time) error {
		w.onStart("app", d)()
		return nil
	})
	if err != nil {
		t.Errorf("got %v", err)
	}
	if because(nil) != "" || because(errors.New("xcrun simctl spawn: exit status 143")) != "" {
		t.Error("no message from the tool, no reason")
	}
}
