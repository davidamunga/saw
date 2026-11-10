package source

import (
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

// timedLine is one app log line, at ms after t0. A message with newlines is one log call.
type timedLine struct {
	ms  int
	tid int64
	msg string
}

var t0 = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

// assembleTimed feeds lines as appHTTPLog does, a message's lines one by one.
func assembleTimed(t *testing.T, lines []timedLine) ([]event.Net, []string) {
	t.Helper()
	var got []event.Net
	var loose []string
	h := newHTTPLog(func(n event.Net) { got = append(got, n) })
	for _, l := range lines {
		e := event.Event{TID: l.tid, Message: l.msg, Time: t0.Add(time.Duration(l.ms) * time.Millisecond)}
		appHTTPLog(e, h, func(n event.Net) { loose = append(loose, n.Log) })
	}
	h.flush(true)
	return got, loose
}

const afSuccess = `[Request]: POST https://api.example.com/v1/orders?src=app
    [Headers]:
        Accept: application/json
        Content-Type: application/json
    [Body]:
        {"item":"1042"}
[Response]:
    [Status Code]: 201
    [Headers]:
        Content-Length: 28
        Content-Type: application/json; charset=utf-8
    [Body]:
        {
            "id": "ord_88"
        }
[Network Duration]: 0.30112791061401367s
[Serialization Duration]: 0.00012594s
[Result]: success(Order(
    id: "ord_88", url: https://api.example.com/v1/orders/ord_88
))`

func TestAlamofire(t *testing.T) {
	got, loose := assembleTimed(t, []timedLine{
		{1000, 1, afSuccess},
		{1100, 1, "[Request]: GET https://down.example.com/\n    [Headers]: None\n    [Body]: None\n[Response]: None\n[Network Duration]: None\n[Serialization Duration]: 0.0s\n[Result]: failure(Alamofire.AFError.sessionTaskFailed(error: The Internet connection appears to be offline.))"},
		{1200, 1, "[Request]: PUT https://api.example.com/v1/cart\n    [Headers]:\n        Content-Type: application/json\n    [Body]:\n        {\"items\":[1,2,"},
	})
	if len(got) != 3 || len(loose) != 0 {
		t.Fatalf("got %d requests, loose %q", len(got), loose)
	}
	n := got[0]
	if n.Method != "POST" || n.URL != "https://api.example.com/v1/orders?src=app" || n.Status != 201 || n.DurationMs != 301 || n.Failed {
		t.Errorf("got %+v", n)
	}
	if !n.Time.Equal(t0.Add(time.Second)) || !n.Started.Equal(n.Time.Add(-301127910*time.Nanosecond)) {
		t.Errorf("times: %v %v", n.Started, n.Time)
	}
	if headers(n.RequestHeaders) != "Accept=application/json,Content-Type=application/json" ||
		headers(n.ResponseHeaders) != "Content-Length=28,Content-Type=application/json; charset=utf-8" {
		t.Errorf("headers: %q %q", headers(n.RequestHeaders), headers(n.ResponseHeaders))
	}
	if n.RequestBody != `{"item":"1042"}` || n.ResponseBody != "{\n    \"id\": \"ord_88\"\n}" {
		t.Errorf("bodies: %q %q", n.RequestBody, n.ResponseBody)
	}
	if f := got[1]; !f.Failed || f.Status != 0 || !strings.Contains(f.Error, "Internet connection appears to be offline") || f.RequestHeaders != nil {
		t.Errorf("failure: %+v", f)
	}
	// The log cut the message off before the response: the request alone, saying so.
	if c := got[2]; c.Method != "PUT" || c.Status != 0 || c.Error != "the rest was cut off in the log" || c.RequestBody != `{"items":[1,2,` {
		t.Errorf("cut short: %+v", c)
	}
}

// moyaAt is a Moya_Logger entry, with an en_US date (its space before AM is U+202F).
func moyaAt(entry string) string {
	return "Moya_Logger: [11/10/26, 10:00 AM] " + entry
}

func TestMoya(t *testing.T) {
	got, loose := assembleTimed(t, []timedLine{
		{0, 1, moyaAt("Request: https://api.example.com/v1/orders")},
		{0, 1, moyaAt(`Request Headers: ["Content-Type": "application/json", "Accept": "application/json", "X-Note": "say \"hi\""]`)},
		{0, 1, moyaAt(`Request Body: {"item":"1042"}`)},
		{0, 1, moyaAt("HTTP Request Method: POST")},
		{5, 1, moyaAt("Request: https://api.example.com/v1/me")},
		{5, 1, moyaAt("HTTP Request Method: GET")},
		// The responses come on another thread, the second request's first.
		{120, 7, moyaAt("Response: <NSHTTPURLResponse: 0x1008478a0> { URL: https://api.example.com/v1/me } { Status Code: 200, Headers {\n" +
			"    \"Content-Type\" = \"application/json\";\n} }")},
		{350, 7, moyaAt("Response: <NSHTTPURLResponse: 0x1008478b0> { URL: https://api.example.com/v1/orders } { Status Code: 201, Headers {\n" +
			"    \"Content-Length\" =     (\n        28\n    );\n" +
			"    \"Content-Type\" =     (\n        \"application/json; charset=utf-8\"\n    );\n} }")},
		{350, 7, moyaAt("Response Body: {\n  \"id\": \"ord_88\"\n}")},
		{400, 1, moyaAt("Request: https://down.example.com/")},
		{400, 1, moyaAt("HTTP Request Method: GET")},
		{900, 7, moyaAt("Error: Error calling Down.ping : underlying(The Internet connection appears to be offline.)")},
	})
	if len(got) != 3 || len(loose) != 0 {
		t.Fatalf("got %d requests %+v, loose %q", len(got), got, loose)
	}
	me, orders, down := got[0], got[1], got[2]
	if me.Method != "GET" || me.URL != "https://api.example.com/v1/me" || me.Status != 200 || me.DurationMs != 115 || headers(me.ResponseHeaders) != "Content-Type=application/json" {
		t.Errorf("me: %+v", me)
	}
	if orders.Method != "POST" || orders.Status != 201 || orders.DurationMs != 350 || orders.RequestBody != `{"item":"1042"}` || orders.ResponseBody != "{\n  \"id\": \"ord_88\"\n}" {
		t.Errorf("orders: %+v", orders)
	}
	if headers(orders.RequestHeaders) != `Content-Type=application/json,Accept=application/json,X-Note=say "hi"` ||
		headers(orders.ResponseHeaders) != "Content-Length=28,Content-Type=application/json; charset=utf-8" {
		t.Errorf("headers: %q / %q", headers(orders.RequestHeaders), headers(orders.ResponseHeaders))
	}
	if !down.Failed || !strings.Contains(down.Error, "offline") || down.URL != "https://down.example.com/" {
		t.Errorf("down: %+v", down)
	}
}

func TestMoyaOddEntries(t *testing.T) {
	got, _ := assembleTimed(t, []timedLine{
		{0, 1, moyaAt("Request: (invalid request)")},
		{0, 1, moyaAt("Request: https://api.example.com/v1/a")},
		{0, 1, moyaAt("HTTP Request Method: GET")},
		{50, 1, moyaAt("Response: Received empty network response for A.get.")},
		// A response with no logged request still shows.
		{60, 2, moyaAt("Response: <NSHTTPURLResponse: 0x1> { URL: https://api.example.com/v1/b } { Status Code: 204, Headers {\n} }")},
		{60, 2, moyaAt("Response Body: ")},
		{70, 3, moyaAt("Request Headers: [\"Stray\": \"1\"]")}, // not inside a request: ignored
	})
	if len(got) != 2 || !got[0].Failed || !strings.HasPrefix(got[0].Error, "Received empty network response") || got[1].Status != 204 || got[1].URL != "https://api.example.com/v1/b" {
		t.Errorf("got %+v", got)
	}
}
