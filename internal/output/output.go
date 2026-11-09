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
}

// labelWidth is the width of the tag column; longer tags are cut, ending in ~.
const labelWidth = 18

func NewPretty(w io.Writer, color bool) *Pretty {
	return &Pretty{w: w, color: color, width: labelWidth}
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
	label := e.Label()
	if len(label) > p.width {
		label = label[:p.width-1] + "~"
	}
	msgColor := ""
	if e.Level >= event.Warn {
		msgColor = levelColor(e.Level)
	}
	head := fmt.Sprintf("%s %s %s ",
		p.paint(dim, e.Time.Format("15:04:05.000")),
		p.paint(levelColor(e.Level), e.Level.Letter()),
		p.paint(cyan, fmt.Sprintf("%-*s", p.width, label)))
	indent := strings.Repeat(" ", 12+1+1+1+p.width+1)
	for i, line := range strings.Split(strings.TrimRight(e.Message, "\n"), "\n") {
		if i == 0 {
			fmt.Fprintln(p.w, head+p.paint(msgColor, line))
		} else {
			fmt.Fprintln(p.w, indent+p.paint(msgColor, line))
		}
	}
}

// IsTerminal reports whether f is a terminal, and color is wanted (NO_COLOR unset).
func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func WantColor(f *os.File) bool { return IsTerminal(f) && os.Getenv("NO_COLOR") == "" }
