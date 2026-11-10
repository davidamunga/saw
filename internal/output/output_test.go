package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

var at = time.Date(2026, 11, 9, 9, 41, 2, 114e6, time.UTC)

func TestPretty(t *testing.T) {
	var b bytes.Buffer
	p := NewPretty(&b, false)
	p.Print(event.Event{Time: at, Level: event.Info, Tag: "Main", Message: "onCreate"})
	p.Print(event.Event{Time: at, Level: event.Error, Tag: "MainActivity", Message: "boom\n  at line 1\n"})
	p.Print(event.Event{Time: at, Level: event.Debug, Tag: strings.Repeat("x", 30), Message: "long label"})
	want := "09:41:02.114 I Main               onCreate\n" +
		"09:41:02.114 E MainActivity       boom\n" +
		strings.Repeat(" ", 34) + "  at line 1\n" +
		"09:41:02.114 D " + strings.Repeat("x", 17) + "~ long label\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestPrettyColor(t *testing.T) {
	var b bytes.Buffer
	p := NewPretty(&b, true)
	for _, l := range []event.Level{event.Verbose, event.Debug, event.Info, event.Warn, event.Error, event.Fatal} {
		p.Print(event.Event{Time: at, Level: l, Tag: "T", Message: "m"})
	}
	out := b.String()
	for _, code := range []string{dim, green, yellow, red, bold, cyan, reset} {
		if !strings.Contains(out, code) {
			t.Errorf("missing color %q", code)
		}
	}
	if strings.Count(out, "\n") != 6 {
		t.Errorf("want 6 lines, got %q", out)
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	NewJSON(&b).Print(event.Event{Time: at, Platform: "android", Level: event.Warn, Tag: "Sync", Message: "retrying"})
	got := b.String()
	for _, part := range []string{`"platform":"android"`, `"level":"warn"`, `"tag":"Sync"`, `"message":"retrying"`} {
		if !strings.Contains(got, part) {
			t.Errorf("missing %s in %s", part, got)
		}
	}
	if strings.Contains(got, "subsystem") {
		t.Error("empty fields should be left out")
	}
}

func TestTerminalChecks(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) || WantColor(f) {
		t.Error("a file is not a terminal")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if IsTerminal(null) || IsTerminal(nil) {
		t.Error("/dev/null is a character device, but not a terminal")
	}
}

func TestDeviceColumn(t *testing.T) {
	if w := DeviceWidth([]string{"Pixel 8", "iPhone 17 Pro Max Ultra"}); w != 14 {
		t.Errorf("got %d", w)
	}
	var b bytes.Buffer
	p := NewPretty(&b, false)
	p.ShowDevice(9)
	p.Print(event.Event{Time: at, Device: "Pixel 8", Level: event.Info, Tag: "Main", Message: "a\nb"})
	p.Print(event.Event{Time: at, Device: "iPhone 17 Pro", Level: event.Info, Tag: "Main", Message: "c"})
	want := "09:41:02.114 Pixel 8   I Main               a\n" +
		strings.Repeat(" ", 44) + "b\n" +
		"09:41:02.114 iPhone 1~ I Main               c\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
