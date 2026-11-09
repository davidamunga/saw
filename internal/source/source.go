// Package source streams a running app's logs from one device, as events.
package source

import (
	"context"
	"fmt"
	"strings"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/event"
)

// Spec says what to stream.
type Spec struct {
	Device    device.Device
	App       config.App
	Subsystem string              // iOS: the app's Logger subsystem; "" = all but Apple's own lines
	Areas     []string            // the project's areas; empty means no area filter
	Chosen    bool                // Areas were picked with --area, not just every configured area
	TagFor    func(string) string // Android: area → logcat tag; nil when the project has none
	All       bool                // everything the app's process logs, not only its own lines
	OnStart   func()              // called once the app is found, just before lines start
}

func (s Spec) started() {
	if s.OnStart != nil {
		s.OnStart()
	}
}

// containsFold reports whether list holds v, ignoring case.
func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// Stream sends each of the app's log lines to emit until ctx ends.
func Stream(ctx context.Context, s Spec, emit func(event.Event)) error {
	switch {
	case s.Device.Platform == "android":
		return streamAndroid(ctx, s, emit)
	case s.Device.Kind == "simulator":
		return streamSimulator(ctx, s, emit)
	case s.Device.Platform == "ios":
		return streamIOSDevice(ctx, s, emit)
	}
	return fmt.Errorf("unsupported device %s", s.Device)
}

func (s Spec) checkIOS() error {
	if s.App.IOS == "" {
		return fmt.Errorf("app %q has no ios bundle id in saw.json", s.App.Name)
	}
	return nil
}

// setArea names an iOS line's area: its category, when it's in the app's own subsystem.
func (s Spec) setArea(e *event.Event) {
	if s.Subsystem != "" && e.Subsystem == s.Subsystem {
		e.Area = e.Category
	}
}

// keepIOS decides whether an iOS line from the app's process is the app's own.
// With a subsystem: that subsystem (and the chosen categories). Without one, for apps that log with
// NSLog, os_log or a third-party logger: lines sent from code inside the app bundle (its binary and
// the frameworks it ships), minus Apple subsystems. That drops Apple's frameworks, which log a lot
// from inside every app, often with no subsystem.
func (s Spec) keepIOS(e event.Event) bool {
	if s.All {
		return true
	}
	if s.Chosen && !containsFold(s.Areas, e.Category) {
		return false
	}
	if s.Subsystem != "" {
		return e.Subsystem == s.Subsystem
	}
	// Apple subsystems are dropped even from inside the bundle (Xcode's debug stub logs there).
	return strings.Contains(e.Sender, ".app/") && !strings.HasPrefix(e.Subsystem, "com.apple.")
}
