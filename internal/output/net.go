package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/davidamunga/saw/internal/event"
)

type NetPrinter interface{ Print(event.Net) }

// NetJSON writes one network event per line.
type NetJSON struct{ enc *json.Encoder }

func NewNetJSON(w io.Writer) *NetJSON { return &NetJSON{enc: json.NewEncoder(w)} }

func (j *NetJSON) Print(n event.Net) { _ = j.enc.Encode(n) }

// NetPretty writes `15:44:24.150 200  https://example.com/  941ms h2 520 B`.
type NetPretty struct {
	w     io.Writer
	color bool
}

func NewNetPretty(w io.Writer, color bool) *NetPretty { return &NetPretty{w: w, color: color} }

func (p *NetPretty) paint(color, s string) string {
	if !p.color || s == "" {
		return s
	}
	return color + s + reset
}

func (p *NetPretty) Print(n event.Net) {
	label, color := netLabel(n)
	target := n.URL
	if target == "" && n.Host != "" {
		target = hostPort(n.Host, n.Port)
	}
	var details []string
	switch {
	case target == "" && n.IP != "":
		target = hostPort(n.IP, n.Port)
	case n.Host != "" && n.IP != "" && n.Kind != "request":
		details = append(details, n.IP)
	}
	if target == "" && n.Conn > 0 {
		target = fmt.Sprintf("(connection %d)", n.Conn)
	}
	if n.DurationMs > 0 {
		details = append(details, strconv.Itoa(n.DurationMs)+"ms")
	}
	if n.Protocol != "" {
		details = append(details, n.Protocol)
	}
	if n.Bytes > 0 {
		details = append(details, humanBytes(n.Bytes))
	}
	if n.Cached {
		details = append(details, "cached")
	}
	line := p.paint(dim, n.Time.Format("15:04:05.000")) + " " + p.paint(color, fmt.Sprintf("%-4s", label))
	if target != "" {
		line += " " + target
	}
	if len(details) > 0 {
		line += "  " + p.paint(dim, strings.Join(details, " "))
	}
	fmt.Fprintln(p.w, strings.TrimRight(line, " "))
}

// netLabel is the short first column: the status for requests, else the kind of connection.
func netLabel(n event.Net) (string, string) {
	switch {
	case n.Kind == "request" && n.Failed:
		return "ERR", red
	case n.Kind == "request" && n.Status > 0:
		switch {
		case n.Status >= 500:
			return strconv.Itoa(n.Status), red
		case n.Status >= 400:
			return strconv.Itoa(n.Status), yellow
		case n.Status >= 300:
			return strconv.Itoa(n.Status), cyan
		}
		return strconv.Itoa(n.Status), green
	case n.Kind == "request":
		return "REQ", cyan
	case n.Kind == "http":
		return n.Method, cyan
	}
	return strings.ToUpper(n.Kind), dim
}

func hostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	if strings.Contains(host, ":") { // IPv6
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
