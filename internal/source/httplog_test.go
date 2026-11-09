package source

import (
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

// logLine is one app log line: thread, tag, message.
type logLine struct {
	tid int64
	tag string
	msg string
}

func assemble(t *testing.T, lines []logLine) ([]event.Net, []string) {
	t.Helper()
	var got []event.Net
	var loose []string
	h := newHTTPLog(func(n event.Net) { got = append(got, n) })
	for _, l := range lines {
		if !h.line(event.Event{TID: l.tid, Tag: l.tag, Message: l.msg, Time: time.Unix(1, 0)}) {
			loose = append(loose, l.msg)
		}
	}
	h.flush(true)
	return got, loose
}

func headers(hs []event.Header) string {
	var s []string
	for _, h := range hs {
		s = append(s, h.Name+"="+h.Value)
	}
	return strings.Join(s, ",")
}

func TestOkHttpBlocks(t *testing.T) {
	got, loose := assemble(t, []logLine{
		{1, "OkHttp", "--> POST https://api.example.com/v1/orders"},
		{1, "OkHttp", "Content-Type: application/json"},
		{1, "OkHttp", ""},
		{1, "OkHttp", `{"item":"1042"}`},
		{1, "OkHttp", "--> END POST (15-byte body)"},
		{1, "Main", "unrelated line on the same thread"},
		{1, "OkHttp", "<-- 201 https://api.example.com/v1/orders (412ms)"},
		{1, "OkHttp", "content-type: application/json"},
		{1, "OkHttp", ""},
		{1, "OkHttp", `{"id":"ord_88"}`},
		{1, "OkHttp", "<-- END HTTP (15-byte body)"},
	})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	n := got[0]
	if n.Kind != "logged" || n.Method != "POST" || n.URL != "https://api.example.com/v1/orders" || n.Status != 201 || n.DurationMs != 412 {
		t.Errorf("got %+v", n)
	}
	if headers(n.RequestHeaders) != "Content-Type=application/json" || n.RequestBody != `{"item":"1042"}` ||
		headers(n.ResponseHeaders) != "content-type=application/json" || n.ResponseBody != `{"id":"ord_88"}` {
		t.Errorf("headers and bodies: %+v", n)
	}
	if strings.Join(loose, "|") != "unrelated line on the same thread" {
		t.Errorf("other lines should be left alone: %v", loose)
	}
}

func TestOkHttpThreadsAndBasicLevel(t *testing.T) {
	got, _ := assemble(t, []logLine{
		{1, "Http", "--> GET https://a.example.com/one"},
		{2, "Http", "--> GET https://a.example.com/two http/1.1"},
		{1, "Http", "Accept: one"},
		{2, "Http", "Accept: two"},
		{2, "Http", "--> END GET"},
		{1, "Http", "--> END GET"},
		{2, "Http", "<-- 200 OK https://a.example.com/two (20ms, 2-byte body)"}, // BASIC: no END HTTP
		{1, "Http", "<-- 404 https://a.example.com/one (30ms)"},
		{1, "Http", "<-- END HTTP"},
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	byURL := map[string]event.Net{}
	for _, n := range got {
		byURL[n.URL] = n
	}
	if n := byURL["https://a.example.com/one"]; n.Status != 404 || headers(n.RequestHeaders) != "Accept=one" {
		t.Errorf("one: %+v", n)
	}
	if n := byURL["https://a.example.com/two"]; n.Status != 200 || headers(n.RequestHeaders) != "Accept=two" {
		t.Errorf("two: %+v", n)
	}
}

func TestOkHttpInterleavedWithoutThreads(t *testing.T) {
	// iPhone logs have no thread ids: two requests sent together interleave their headers.
	var lines []logLine
	for _, m := range []string{"--> GET https://a/config", "--> GET https://a/user", "Accept: json", "Authorization: x",
		"Accept: json", "Authorization: x", "--> END GET", "--> END GET",
		"<-- 200 https://a/config (9ms)", "<-- END HTTP", "<-- 200 https://a/user (10ms)", "<-- END HTTP"} {
		lines = append(lines, logLine{0, "http", m})
	}
	got, _ := assemble(t, lines)
	if len(got) != 2 || len(got[0].RequestHeaders) != 2 || len(got[1].RequestHeaders) != 2 {
		t.Errorf("each request should get one copy of the headers: %+v", got)
	}
}

func TestOkHttpFailuresAndMissingResponses(t *testing.T) {
	got, _ := assemble(t, []logLine{
		{1, "OkHttp", "--> GET https://down.example.com/"},
		{1, "OkHttp", "--> END GET"},
		{1, "OkHttp", "<-- HTTP FAILED: java.net.UnknownHostException: down.example.com"},
		{2, "OkHttp", "--> GET https://slow.example.com/"},
		{3, "OkHttp", "<-- 200 https://orphan.example.com/ (5ms)"}, // a response with no logged request
	})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	byURL := map[string]event.Net{}
	for _, n := range got {
		byURL[n.URL] = n
	}
	if n := byURL["https://down.example.com/"]; !n.Failed || !strings.Contains(n.Error, "UnknownHost") {
		t.Errorf("failure: %+v", n)
	}
	if n := byURL["https://slow.example.com/"]; n.Error != "no response logged" {
		t.Errorf("no response: %+v", n)
	}
	if n := byURL["https://orphan.example.com/"]; n.Status != 200 || n.Method != "" {
		t.Errorf("orphan response: %+v", n)
	}
}

func TestOkHttpRejoinsSplitBodies(t *testing.T) {
	long := strings.Repeat("a", 4000)
	got, _ := assemble(t, []logLine{
		{1, "OkHttp", "<-- 200 https://big.example.com/ (5ms)"},
		{1, "OkHttp", ""},
		{1, "OkHttp", long},
		{1, "OkHttp", "tail"},
		{1, "OkHttp", "second line"},
	})
	if len(got) != 1 || got[0].ResponseBody != long+"tail\nsecond line" {
		t.Errorf("Android's 4000-byte pieces should be rejoined: %d bytes", len(got[0].ResponseBody))
	}
}

func TestKtorBlocks(t *testing.T) {
	var lines []logLine
	for _, m := range []string{"REQUEST: https://ktor.example.com/items", "METHOD: HttpMethod(value=POST)", "COMMON HEADERS",
		"-> Accept: application/json", "BODY Content-Type: application/json", "BODY START", `{"a":1}`, "BODY END",
		"RESPONSE: 200 OK", "METHOD: HttpMethod(value=POST)", "FROM: https://ktor.example.com/items", "COMMON HEADERS",
		"-> Content-Type: application/json", "BODY START", `{"ok":true}`, "BODY END"} {
		lines = append(lines, logLine{7, "HttpClient", m})
	}
	got, loose := assemble(t, lines)
	if len(got) != 1 || len(loose) != 0 {
		t.Fatalf("got %+v, loose %v", got, loose)
	}
	n := got[0]
	if n.Method != "POST" || n.Status != 200 || n.RequestBody != `{"a":1}` || n.ResponseBody != `{"ok":true}` ||
		headers(n.RequestHeaders) != "Accept=application/json" || headers(n.ResponseHeaders) != "Content-Type=application/json" {
		t.Errorf("got %+v", n)
	}
}

func TestDioBlocks(t *testing.T) {
	var lines []logLine
	for _, m := range []string{"*** Request ***", "uri: https://dio.example.com/login", "method: POST", "responseType: ResponseType.json",
		"headers:", " content-type: application/json", "data:", `{"user":"ann"}`,
		"*** Response ***", "uri: https://dio.example.com/login", "statusCode: 401", "headers:", " www-authenticate: Bearer",
		"Response Text:", `{"error":"nope"}`,
		"*** Request ***", "uri: https://dio.example.com/down", "method: GET",
		"*** DioException ***", "uri: https://dio.example.com/down", "message: connection refused"} {
		lines = append(lines, logLine{0, "flutter", m})
	}
	got, _ := assemble(t, lines)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if n := got[0]; n.Method != "POST" || n.Status != 401 || n.RequestBody != `{"user":"ann"}` || n.ResponseBody != `{"error":"nope"}` ||
		headers(n.RequestHeaders) != "content-type=application/json" || headers(n.ResponseHeaders) != "www-authenticate=Bearer" {
		t.Errorf("login: %+v", n)
	}
	if n := got[1]; !n.Failed || n.Error != "connection refused" || n.URL != "https://dio.example.com/down" {
		t.Errorf("down: %+v", n)
	}
}

func TestQuietBlocksFlush(t *testing.T) {
	var got []event.Net
	h := newHTTPLog(func(n event.Net) { got = append(got, n) })
	h.line(event.Event{TID: 1, Tag: "OkHttp", Message: "<-- 200 https://a.example.com/ (5ms)"})
	h.flush(false)
	if len(got) != 0 {
		t.Fatal("a response still being written isn't done yet")
	}
	stop := h.start()
	h.mu.Lock()
	for _, b := range h.current {
		b.seen = time.Now().Add(-time.Second)
	}
	h.mu.Unlock()
	time.Sleep(400 * time.Millisecond)
	stop()
	if len(got) != 1 {
		t.Errorf("a quiet response should be emitted: %+v", got)
	}
}
