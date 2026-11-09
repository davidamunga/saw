package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/davidamunga/saw/internal/event"
)

type NetPrinter interface{ Print(event.Net) }

// NetJSON writes one network event per line.
type NetJSON struct{ enc *json.Encoder }

func NewNetJSON(w io.Writer) *NetJSON { return &NetJSON{enc: json.NewEncoder(w)} }

func (j *NetJSON) Print(n event.Net) { _ = j.enc.Encode(n) }

// NetPretty writes one line per event in fixed columns, the full URL last, so it reads well
// and copies cleanly:
//
//	17:58:39.594  GET     200  1546ms   97 B  https://api.example.com/mobile-config
//	17:58:40.668  TCP                         api.example.com:443  203.0.113.7
//
// With full, each request the app logged is printed as plain HTTP messages in the .http file
// format (VS Code's REST Client, JetBrains' HTTP Client), and everything else as # comments, so
// the output can be saved and the requests sent again.
type NetPretty struct {
	w     io.Writer
	color bool
	full  bool
}

func NewNetPretty(w io.Writer, color, full bool) *NetPretty {
	return &NetPretty{w: w, color: color, full: full}
}

func (p *NetPretty) paint(color, s string) string {
	if !p.color || s == "" {
		return s
	}
	return color + s + reset
}

func (p *NetPretty) Print(n event.Net) {
	if p.full && n.Kind == "logged" {
		p.httpMessage(n)
		return
	}
	line := p.summary(n)
	if p.full {
		line = p.paint(dim, "# ") + line
	}
	fmt.Fprintln(p.w, line)
}

// summary is the event's one line: time, kind or method, status, duration, size, target.
func (p *NetPretty) summary(n event.Net) string {
	label, status, color := netColumns(n)
	duration, size := "", ""
	if n.DurationMs > 0 {
		duration = strconv.Itoa(n.DurationMs) + "ms"
	}
	if b := netSize(n); b > 0 {
		size = humanBytes(b)
	}
	target, extra := p.netTarget(n)
	line := fmt.Sprintf("%s  %s %s %s %s  %s",
		p.paint(dim, n.Time.Format("15:04:05.000")),
		p.paint(cyan, fmt.Sprintf("%-6s", label)),
		p.paint(color, fmt.Sprintf("%3s", status)),
		p.paint(dim, fmt.Sprintf("%7s", duration)),
		p.paint(dim, fmt.Sprintf("%7s", size)),
		target)
	if extra != "" {
		line += "  " + p.paint(dim, extra)
	}
	return strings.TrimRight(line, " ")
}

// netColumns is an event's kind-or-method column, and its status column and color.
func netColumns(n event.Net) (label, status, color string) {
	switch n.Kind {
	case "logged", "http":
		label = n.Method
		if label == "" {
			label = "?"
		}
	case "request":
		label = "REQ"
	default:
		label = strings.ToUpper(n.Kind)
	}
	switch {
	case n.Failed || (n.Kind == "logged" && n.Status == 0 && n.Error != ""):
		return label, "ERR", red
	case n.Status >= 500:
		return label, strconv.Itoa(n.Status), red
	case n.Status >= 400:
		return label, strconv.Itoa(n.Status), yellow
	case n.Status >= 300:
		return label, strconv.Itoa(n.Status), cyan
	case n.Status > 0:
		return label, strconv.Itoa(n.Status), green
	case n.Kind == "logged" || n.Kind == "request":
		return label, "-", dim // no response (yet)
	}
	return label, "", dim
}

// netSize is the response's size, when known.
func netSize(n event.Net) int {
	if n.Bytes > 0 {
		return n.Bytes
	}
	if b := contentLength(n.ResponseHeaders); b > 0 {
		return b
	}
	return len(n.ResponseBody)
}

// netTarget is what the event is about, in full, and dim extras after it.
func (p *NetPretty) netTarget(n event.Net) (string, string) {
	var extras []string
	if n.Protocol != "" {
		extras = append(extras, n.Protocol)
	}
	if n.Cached {
		extras = append(extras, "cached")
	}
	if n.Error != "" {
		extras = append(extras, n.Error)
	}
	switch {
	case n.Kind == "log":
		tag := n.Tag
		if tag != "" {
			tag += ": "
		}
		return p.paint(dim, tag) + n.Log, ""
	case n.URL != "":
		return p.url(n.URL), strings.Join(extras, " ")
	case n.Host != "":
		if n.IP != "" && n.Kind != "request" {
			extras = append([]string{n.IP}, extras...)
		}
		return hostPort(n.Host, n.Port), strings.Join(extras, " ")
	case n.IP != "":
		return hostPort(n.IP, n.Port), strings.Join(extras, " ")
	case n.Conn > 0:
		return fmt.Sprintf("(connection %d)", n.Conn), strings.Join(extras, " ")
	}
	return "", strings.Join(extras, " ")
}

// url shows a URL in full, its query dimmed.
func (p *NetPretty) url(u string) string {
	if base, query, ok := strings.Cut(u, "?"); ok {
		return base + p.paint(dim, "?"+query)
	}
	return u
}

// httpMessage prints a logged request as plain HTTP:
//
//	### 17:58:41.534  POST 200  356ms
//	POST https://api.example.com/pusher/user-auth
//	Content-Type: application/json
//
//	{ "socket_id": "1234.5678" }
//
//	HTTP 200
//	content-type: application/json
//
//	{ "auth": "a1b2" }
func (p *NetPretty) httpMessage(n event.Net) {
	head := fmt.Sprintf("### %s  %s", n.Time.Format("15:04:05.000"), strings.TrimSpace(p.plainColumns(n)))
	fmt.Fprintln(p.w, p.paint(dim, head))
	method := n.Method
	if method == "" {
		method = "GET"
	}
	fmt.Fprintf(p.w, "%s %s\n", p.paint(cyan, method), n.URL)
	p.headersAndBody(n.RequestHeaders, n.RequestBody)
	switch {
	case n.Status > 0:
		fmt.Fprintln(p.w)
		_, status, color := netColumns(n)
		fmt.Fprintln(p.w, p.paint(color, "HTTP "+status))
		p.headersAndBody(n.ResponseHeaders, n.ResponseBody)
	case n.Error != "":
		fmt.Fprintln(p.w)
		fmt.Fprintln(p.w, p.paint(red, "# "+n.Error))
	}
	fmt.Fprintln(p.w)
}

// plainColumns is the method, status and duration for a ### line.
func (p *NetPretty) plainColumns(n event.Net) string {
	label, status, _ := netColumns(n)
	s := label + " " + status
	if n.DurationMs > 0 {
		s += "  " + strconv.Itoa(n.DurationMs) + "ms"
	}
	return s
}

func (p *NetPretty) headersAndBody(headers []event.Header, body string) {
	for _, h := range headers {
		fmt.Fprintf(p.w, "%s %s\n", p.paint(dim, h.Name+":"), h.Value)
	}
	if body == "" {
		return
	}
	fmt.Fprintln(p.w)
	fmt.Fprintln(p.w, prettyBody(body))
	// Logs cap message sizes (Android about 4 KB, iOS about 1 KB), so say when a body is short.
	if want := contentLength(headers); want > len(body) {
		fmt.Fprintln(p.w, p.paint(yellow, fmt.Sprintf("# body cut short in the app's log: %d of %d bytes", len(body), want)))
	}
}

// contentLength is the Content-Length header's value, or 0.
func contentLength(headers []event.Header) int {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "content-length") {
			if n, err := strconv.Atoi(strings.TrimSpace(h.Value)); err == nil {
				return n
			}
		}
	}
	return 0
}

// prettyBody indents a JSON body, and leaves anything else as it was logged.
func prettyBody(body string) string {
	t := strings.TrimSpace(body)
	if (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)) {
		var b bytes.Buffer
		if json.Indent(&b, []byte(t), "", "  ") == nil {
			return b.String()
		}
	}
	return body
}

// NetCurl prints each request the app logged as a curl command, to paste and send again, and
// everything else as # comments.
type NetCurl struct{ w io.Writer }

func NewNetCurl(w io.Writer) *NetCurl { return &NetCurl{w: w} }

func (c *NetCurl) Print(n event.Net) {
	plain := NewNetPretty(c.w, false, false)
	if n.Kind != "logged" || n.URL == "" {
		fmt.Fprintln(c.w, "# "+plain.summary(n))
		return
	}
	fmt.Fprintf(c.w, "# %s  %s\n", n.Time.Format("15:04:05.000"), plain.plainColumns(n))
	first := "curl"
	if n.Method != "" && n.Method != "GET" {
		first += " -X " + n.Method
	}
	parts := []string{first + " " + shellQuote(n.URL)}
	for _, h := range n.RequestHeaders {
		switch strings.ToLower(h.Name) {
		case "content-length", "host": // curl sets these itself
			continue
		}
		parts = append(parts, "-H "+shellQuote(h.Name+": "+h.Value))
	}
	if n.RequestBody != "" {
		parts = append(parts, "--data-raw "+shellQuote(n.RequestBody))
	}
	fmt.Fprintln(c.w, strings.Join(parts, " \\\n  "))
	fmt.Fprintln(c.w)
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func hostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
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
