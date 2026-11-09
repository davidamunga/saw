// Package event is the one log line shape every source produces and every output consumes.
package event

import (
	"fmt"
	"strings"
	"time"
)

// Level is a log level common to both platforms, ordered from least to most severe.
type Level int

const (
	Verbose Level = iota
	Debug
	Info
	Warn
	Error
	Fatal
)

var levelNames = [...]string{"verbose", "debug", "info", "warn", "error", "fatal"}

func (l Level) String() string { return levelNames[l] }

// Letter is the one-letter form logcat uses: V, D, I, W, E, F.
func (l Level) Letter() string { return "VDIWEF"[l : l+1] }

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// ParseLevel accepts a level name or its letter, in any case.
func ParseLevel(s string) (Level, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, name := range levelNames {
		if s == name || s == name[:1] {
			return Level(i), nil
		}
	}
	return 0, fmt.Errorf("unknown level %q (want one of %s)", s, strings.Join(levelNames[:], ", "))
}

// Event is one log line from any device.
type Event struct {
	Time      time.Time `json:"time"`
	Platform  string    `json:"platform"` // android | ios
	Device    string    `json:"device"`
	Process   string    `json:"process,omitempty"`
	Sender    string    `json:"sender,omitempty"` // iOS: the binary or library that logged the line
	PID       int       `json:"pid,omitempty"`
	TID       int64     `json:"tid,omitempty"`
	Level     Level     `json:"level"`
	Tag       string    `json:"tag,omitempty"`       // Android logcat tag
	Subsystem string    `json:"subsystem,omitempty"` // iOS
	Category  string    `json:"category,omitempty"`  // iOS
	Scope     string    `json:"scope,omitempty"`     // the project's scope, from saw.json
	Message   string    `json:"message"`
}

// Label is the short name shown beside a line: its scope, else its category or tag.
func (e Event) Label() string {
	switch {
	case e.Scope != "":
		return e.Scope
	case e.Category != "":
		return e.Category
	case e.Tag != "":
		return e.Tag
	case e.Subsystem != "":
		return e.Subsystem
	default:
		return e.Process // e.g. NSLog, which has no subsystem or category
	}
}
