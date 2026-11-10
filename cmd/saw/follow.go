package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/device"
)

// readFunc reads one device until its stream ends, starting at since (zero: from now).
type readFunc func(ctx context.Context, dev device.Device, since time.Time) error

// Where follow looks for devices and how often; tests replace them.
var (
	listDevices = device.List
	pollEvery   = 2 * time.Second
	// A stream that ends this soon after starting, three times running, keeps failing for
	// some other reason than the device going away.
	quickFailure = 10 * time.Second
	// reconnect is off in tests whose fake streams simply end.
	reconnect = true
)

// follow reads a device, and when its stream ends after it started, which usually means the
// device went away (unplugged, Wi-Fi dropped, simulator shut down), waits for it to come back
// and reads it again. Android and simulators keep their logs, so the lines from while it was
// away come first. A device that fails before it starts (the app isn't installed) is an error.
func (w *watch) follow(ctx context.Context, d device.Device, read readFunc) error {
	since := w.since
	quick := 0
	for {
		w.setStarted(d.ID, false)
		began := time.Now()
		err := read(ctx, d, since)
		if ctx.Err() != nil {
			return nil
		}
		if !reconnect || !w.isStarted(d.ID) {
			return err
		}
		if time.Since(began) < quickFailure {
			quick++
		} else {
			quick = 0
		}
		if quick >= 3 {
			if err == nil {
				err = errors.New("its stream keeps ending")
			}
			return err
		}
		lost := now()
		w.say(fmt.Sprintf("saw: lost %s%s; waiting for it to come back (Ctrl-C to stop)", d.Name, because(err)))
		if !w.waitFor(ctx, d) {
			return nil
		}
		w.say(fmt.Sprintf("saw: %s is back", d.Name))
		since = time.Time{}
		if d.Platform == "android" || d.Kind == "simulator" {
			since = lost
		}
	}
}

// because is what the tool said when it stopped, as " (…)": the last line of its error output,
// which run's errors carry after the command. The command and its exit status say nothing useful.
func because(err error) string {
	if err == nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	if len(lines) < 2 {
		return ""
	}
	return " (" + strings.TrimSpace(lines[len(lines)-1]) + ")"
}

// waitFor polls until d is listed and ready again, or ctx ends.
func (w *watch) waitFor(ctx context.Context, d device.Device) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollEvery):
		}
		devs, _ := listDevices(ctx, false)
		for _, x := range devs {
			if x.ID == d.ID && x.Ready {
				return true
			}
		}
	}
}

func (w *watch) say(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fmt.Fprintln(stderr, msg)
}

func (w *watch) setStarted(id string, v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started == nil {
		w.started = map[string]bool{}
	}
	w.started[id] = v
}

func (w *watch) isStarted(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started[id]
}
