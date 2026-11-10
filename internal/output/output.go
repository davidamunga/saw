// Package output prints events: readable lines for people, JSON lines for tools.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davidamunga/saw/internal/event"
)

type Printer interface{ Print(event.Event) }

// JSON writes one event per line.
type JSON struct{ enc *json.Encoder }

func NewJSON(w io.Writer) *JSON { return &JSON{enc: json.NewEncoder(w)} }

func (j *JSON) Print(e event.Event) { _ = j.enc.Encode(e) }

// Pretty writes `11:49:49.142 I api      message`, colored when w is a terminal.
type Pretty struct {
	w     io.Writer
	color bool
	width int
	dev   int // the device column's width; 0 = no column
}

// labelWidth is the width of the tag column; longer tags are cut, ending in ~.
const labelWidth = 18

func NewPretty(w io.Writer, color bool) *Pretty {
	return &Pretty{w: w, color: color, width: labelWidth}
}

// ShowDevice adds a column naming each line's device, for streams from several devices.
func (p *Pretty) ShowDevice(width int) { p.dev = width }

// DeviceWidth is the device column's width for these names: the longest, up to 14.
func DeviceWidth(names []string) int {
	w := 0
	for _, n := range names {
		w = max(w, len(n))
	}
	return min(w, 14)
}

// fit pads s to width, or cuts it to width ending in ~.
func fit(s string, width int) string {
	if len(s) > width {
		return s[:width-1] + "~"
	}
	return fmt.Sprintf("%-*s", width, s)
}

const (
	reset  = "\x1b[0m"
	dim    = "\x1b[2m"
	cyan   = "\x1b[36m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[31m"
	bold   = "\x1b[1;31m"
)

func levelColor(l event.Level) string {
	switch l {
	case event.Verbose, event.Debug:
		return dim
	case event.Info:
		return green
	case event.Warn:
		return yellow
	case event.Error:
		return red
	}
	return bold
}

func (p *Pretty) paint(color, s string) string {
	if !p.color || s == "" {
		return s
	}
	return color + s + reset
}

func (p *Pretty) Print(e event.Event) {
	msgColor := ""
	if e.Level >= event.Warn {
		msgColor = levelColor(e.Level)
	}
	head := p.paint(dim, e.Time.Format("15:04:05.000")) + " "
	indent := 12 + 1
	if p.dev > 0 {
		head += fit(e.Device, p.dev) + " "
		indent += p.dev + 1
	}
	head += fmt.Sprintf("%s %s ", p.paint(levelColor(e.Level), e.Level.Letter()), p.paint(cyan, fit(e.Label(), p.width)))
	pad := strings.Repeat(" ", indent+1+1+p.width+1)
	for i, line := range strings.Split(strings.TrimRight(e.Message, "\n"), "\n") {
		if i == 0 {
			fmt.Fprintln(p.w, head+p.paint(msgColor, line))
		} else {
			fmt.Fprintln(p.w, pad+p.paint(msgColor, line))
		}
	}
}

// IsTerminal reports whether f is a terminal. A character device isn't enough: /dev/null is
// one too, and that's what an agent's or a script's stdin often is.
func IsTerminal(f *os.File) bool {
	return f != nil && isatty(f)
}

// WantColor reports whether f is a terminal and color is wanted (NO_COLOR unset).
func WantColor(f *os.File) bool { return IsTerminal(f) && os.Getenv("NO_COLOR") == "" }
