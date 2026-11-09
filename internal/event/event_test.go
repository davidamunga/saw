package event

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"verbose": Verbose, "D": Debug, " Info ": Info, "warn": Warn, "e": Error, "FATAL": Fatal} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil || !strings.Contains(err.Error(), "verbose, debug") {
		t.Errorf("want an error listing the levels, got %v", err)
	}
}

func TestLevelForms(t *testing.T) {
	if Warn.String() != "warn" || Warn.Letter() != "W" || Fatal.Letter() != "F" {
		t.Error("level names")
	}
	b, _ := json.Marshal(Event{Level: Error})
	if !strings.Contains(string(b), `"level":"error"`) {
		t.Errorf("level should marshal as its name: %s", b)
	}
}

func TestLabel(t *testing.T) {
	for _, c := range []struct {
		e    Event
		want string
	}{
		{Event{Area: "api", Category: "http", Tag: "T"}, "api"},
		{Event{Category: "http", Tag: "T"}, "http"},
		{Event{Tag: "OkHttp"}, "OkHttp"},
		{Event{Subsystem: "com.x"}, "com.x"},
		{Event{Process: "MyApp"}, "MyApp"},
	} {
		if got := c.e.Label(); got != c.want {
			t.Errorf("%+v: got %q", c.e, got)
		}
	}
}
