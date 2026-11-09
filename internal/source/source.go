// Package source streams a running app's logs from one device, as events.
package source

import (
	"context"
	"fmt"

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
}

// Stream sends each of the app's log lines to emit until ctx ends.
func Stream(ctx context.Context, s Spec, emit func(event.Event)) error {
	switch {
	case s.Device.Platform == "android":
		return streamAndroid(ctx, s, emit)
	}
	return fmt.Errorf("unsupported device %s", s.Device)
}
