package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/event"
)

func TestNetPretty(t *testing.T) {
	var b bytes.Buffer
	p := NewNetPretty(&b, false, false)
	for _, n := range []event.Net{
		{Time: at, Kind: "request", URL: "https://example.com/", Host: "example.com", Status: 200, DurationMs: 941, Protocol: "h2", Bytes: 520},
		{Time: at, Kind: "request", Host: "example.com", Status: 404, Bytes: 2048, Cached: true},
		{Time: at, Kind: "request", Status: 503, Conn: 3, Bytes: 3 << 20},
		{Time: at, Kind: "request", Failed: true, DurationMs: 60000},
		{Time: at, Kind: "request", Status: 301},
		{Time: at, Kind: "request"},
		{Time: at, Kind: "tls", Host: "api.example.com", IP: "203.0.113.7", Port: 443},
		{Time: at, Kind: "http", Method: "GET", URL: "http://plain.example.com/a", Host: "plain.example.com", IP: "203.0.113.8", Port: 80},
		{Time: at, Kind: "tcp", IP: "2001:db8::7", Port: 443},
		{Time: at, Kind: "dns", IP: "10.0.0.1", Port: 53},
	} {
		p.Print(n)
	}
	want := `09:41:02.114 200  https://example.com/  941ms h2 520 B
09:41:02.114 404  example.com  2.0 KB cached
09:41:02.114 503  (connection 3)  3.0 MB
09:41:02.114 ERR   60000ms
09:41:02.114 301
09:41:02.114 REQ
09:41:02.114 TLS  api.example.com:443  203.0.113.7
09:41:02.114 GET  http://plain.example.com/a  203.0.113.8
09:41:02.114 TCP  [2001:db8::7]:443
09:41:02.114 DNS  10.0.0.1:53
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	NewNetPretty(&b, true, false).Print(event.Net{Time: at, Kind: "request", Status: 500})
	if !strings.Contains(b.String(), red) {
		t.Errorf("a 5xx should be red: %q", b.String())
	}
}

func TestNetJSON(t *testing.T) {
	var b bytes.Buffer
	NewNetJSON(&b).Print(event.Net{Time: at, Kind: "tls", Host: "api.example.com", Port: 443})
	if got := b.String(); !strings.Contains(got, `"kind":"tls"`) || !strings.Contains(got, `"host":"api.example.com"`) || strings.Contains(got, "status") {
		t.Errorf("got %s", got)
	}
}

func TestNetPrettyLogged(t *testing.T) {
	var b bytes.Buffer
	req := event.Net{Time: at, Kind: "logged", Method: "POST", URL: "https://api.example.com/v1/orders", Status: 201, DurationMs: 412,
		RequestHeaders: []event.Header{{Name: "Content-Type", Value: "application/json"}}, RequestBody: `{"item":"1042"}`,
		ResponseHeaders: []event.Header{{Name: "content-type", Value: "application/json"}}, ResponseBody: "{\n  \"id\": \"ord_88\"\n}"}
	NewNetPretty(&b, false, false).Print(req)
	if b.String() != "09:41:02.114 POST https://api.example.com/v1/orders  201  412ms\n" {
		t.Errorf("one line without --full: %q", b.String())
	}
	b.Reset()
	NewNetPretty(&b, false, true).Print(req)
	want := `09:41:02.114 POST https://api.example.com/v1/orders  201  412ms
    → Content-Type: application/json
    → {"item":"1042"}
    ← content-type: application/json
    ← {
    ←   "id": "ord_88"
    ← }

`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
	for n, label := range map[*event.Net]string{
		{Kind: "logged", URL: "u", Failed: true, Error: "UnknownHost"}: "ERR",
		{Kind: "logged", URL: "u", Error: "no response logged"}:        "ERR",
		{Kind: "logged", URL: "u"}:                                     "…",
		{Kind: "logged", URL: "u", Status: 503}:                        "503",
		{Kind: "logged", URL: "u", Status: 404}:                        "404",
		{Kind: "logged", URL: "u", Status: 302}:                        "302",
	} {
		b.Reset()
		NewNetPretty(&b, true, false).Print(*n)
		if !strings.Contains(b.String(), label) {
			t.Errorf("%+v: want %s in %q", *n, label, b.String())
		}
	}
}
