package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

func TestNetPrettyColumns(t *testing.T) {
	var b bytes.Buffer
	p := NewNetPretty(&b, false, false)
	for _, n := range []event.Net{
		{Time: at, Kind: "logged", Method: "GET", URL: "https://api.example.com/mobile-config", Status: 200, DurationMs: 1546,
			ResponseHeaders: []event.Header{{Name: "Content-Length", Value: "97"}}},
		{Time: at, Kind: "logged", Method: "DELETE", URL: "https://api.example.com/x?id=1", Status: 404, DurationMs: 31, ResponseBody: "nope"},
		{Time: at, Kind: "logged", URL: "https://api.example.com/y", Error: "no response logged"},
		{Time: at, Kind: "request", URL: "https://example.com/", Status: 200, DurationMs: 941, Protocol: "h2", Bytes: 520},
		{Time: at, Kind: "request", Status: 503, Conn: 3, Bytes: 3 << 20, Cached: true},
		{Time: at, Kind: "request", Failed: true, DurationMs: 60000},
		{Time: at, Kind: "tls", Host: "api.example.com", IP: "203.0.113.7", Port: 443},
		{Time: at, Kind: "http", Method: "GET", URL: "http://plain.example.com/a", Host: "plain.example.com", Port: 80},
		{Time: at, Kind: "tcp", IP: "2001:db8::7", Port: 443},
		{Time: at, Kind: "dns", IP: "10.0.0.1", Port: 53},
		{Time: at, Kind: "log", Tag: "Net", Log: "fetched https://cdn.example.com/a.png"},
	} {
		p.Print(n)
	}
	want := `09:41:02.114  GET    200  1546ms    97 B  https://api.example.com/mobile-config
09:41:02.114  DELETE 404    31ms     4 B  https://api.example.com/x?id=1
09:41:02.114  ?      ERR                  https://api.example.com/y  no response logged
09:41:02.114  REQ    200   941ms   520 B  https://example.com/  h2
09:41:02.114  REQ    503          3.0 MB  (connection 3)  cached
09:41:02.114  REQ    ERR 60000ms
09:41:02.114  TLS                         api.example.com:443  203.0.113.7
09:41:02.114  GET                         http://plain.example.com/a
09:41:02.114  TCP                         [2001:db8::7]:443
09:41:02.114  DNS                         10.0.0.1:53
09:41:02.114  LOG                         Net: fetched https://cdn.example.com/a.png
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestNetPrettyColor(t *testing.T) {
	for n, color := range map[*event.Net]string{
		{Kind: "logged", Status: 500}: red, {Kind: "logged", Status: 404}: yellow,
		{Kind: "logged", Status: 302}: cyan, {Kind: "logged", Status: 200}: green,
	} {
		var b bytes.Buffer
		NewNetPretty(&b, true, false).Print(*n)
		if !strings.Contains(b.String(), color) {
			t.Errorf("%d should be colored", n.Status)
		}
	}
	var b bytes.Buffer
	NewNetPretty(&b, true, false).Print(event.Net{Kind: "logged", URL: "https://a.example.com/p?q=1", Status: 200})
	if !strings.Contains(b.String(), "https://a.example.com/p"+dim+"?q=1") {
		t.Errorf("the query should be dimmed: %q", b.String())
	}
}

var logged = event.Net{Time: at, Kind: "logged", Method: "POST", URL: "https://api.example.com/pusher/user-auth", Status: 200, DurationMs: 356,
	RequestHeaders:  []event.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "Content-Length", Value: "23"}, {Name: "Authorization", Value: "██"}},
	RequestBody:     `{"socket_id":"1234.5678"}`,
	ResponseHeaders: []event.Header{{Name: "content-type", Value: "application/json"}, {Name: "content-length", Value: "300"}},
	ResponseBody:    `{"auth":"a1b2:c3d4"}`}

func TestNetPrettyHTTPFile(t *testing.T) {
	var b bytes.Buffer
	p := NewNetPretty(&b, false, true)
	p.Print(logged)
	p.Print(event.Net{Time: at, Kind: "logged", Method: "GET", URL: "https://down.example.com/", Failed: true, Error: "UnknownHostException"})
	p.Print(event.Net{Time: at, Kind: "tcp", IP: "203.0.113.7", Port: 443})
	want := `### 09:41:02.114  POST 200  356ms
POST https://api.example.com/pusher/user-auth
Content-Type: application/json
Content-Length: 23
Authorization: ██

{
  "socket_id": "1234.5678"
}

HTTP 200
content-type: application/json
content-length: 300

{
  "auth": "a1b2:c3d4"
}
# body cut short in the app's log: 20 of 300 bytes

### 09:41:02.114  GET ERR
GET https://down.example.com/

# UnknownHostException

# 09:41:02.114  TCP                         203.0.113.7:443
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
	if got := prettyBody("not json {"); got != "not json {" {
		t.Error(got)
	}
}

func TestNetCurl(t *testing.T) {
	var b bytes.Buffer
	c := NewNetCurl(&b)
	c.Print(logged)
	c.Print(event.Net{Time: at, Kind: "logged", Method: "GET", URL: "https://api.example.com/it's", Status: 200,
		RequestHeaders: []event.Header{{Name: "Host", Value: "api.example.com"}}})
	c.Print(event.Net{Time: at, Kind: "dns", IP: "10.0.0.1", Port: 53})
	want := `# 09:41:02.114  POST 200  356ms
curl -X POST 'https://api.example.com/pusher/user-auth' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: ██' \
  --data-raw '{"socket_id":"1234.5678"}'

# 09:41:02.114  GET 200
curl 'https://api.example.com/it'\''s'

# 09:41:02.114  DNS                         10.0.0.1:53
`
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestNetJSON(t *testing.T) {
	var b bytes.Buffer
	NewNetJSON(&b).Print(event.Net{Time: at, Started: at.Add(-time.Second), Kind: "logged", URL: "https://api.example.com/", Status: 200})
	got := b.String()
	for _, part := range []string{`"kind":"logged"`, `"started":"2026-11-09T09:41:01.114Z"`, `"status":200`} {
		if !strings.Contains(got, part) {
			t.Errorf("missing %s in %s", part, got)
		}
	}
	b.Reset()
	NewNetJSON(&b).Print(event.Net{Time: at, Kind: "tls", Host: "api.example.com", Port: 443})
	if strings.Contains(b.String(), "started") || strings.Contains(b.String(), "status") {
		t.Errorf("empty fields should be left out: %s", b.String())
	}
}

func TestNetDeviceColumn(t *testing.T) {
	var b bytes.Buffer
	p := NewNetPretty(&b, false, false)
	p.ShowDevice(9)
	n := logged
	n.Device = "Pixel 8"
	p.Print(n)
	full := NewNetPretty(&b, false, true)
	full.ShowDevice(9)
	full.Print(n)
	c := NewNetCurl(&b)
	c.ShowDevice(9)
	c.Print(n)
	got := b.String()
	for _, want := range []string{
		"09:41:02.114  Pixel 8    POST   200   356ms   300 B  https://",
		"### 09:41:02.114  Pixel 8  POST 200  356ms\n",
		"# 09:41:02.114  Pixel 8  POST 200  356ms\ncurl",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
