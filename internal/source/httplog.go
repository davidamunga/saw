package source

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

// httpLog assembles the HTTP requests an app logs itself into one "logged" event each, with
// whatever headers and bodies its log level includes. It knows these formats, under any tag or
// category, and Alamofire's and Moya's on iOS (see httplog_swift.go):
//
// OkHttp-style: OkHttp's HttpLoggingInterceptor, and the many loggers that copy it.
//
//	--> POST https://api.example.com/v1/orders
//	Content-Type: application/json
//	{"item":"1042"}
//	--> END POST (15-byte body)
//	<-- 201 https://api.example.com/v1/orders (412ms)
//	content-type: application/json
//	{"id":"ord_88"}
//	<-- END HTTP (15-byte body)
//
// Ktor's Logging plugin: REQUEST: url, METHOD: HttpMethod(value=GET), "-> Name: value" headers,
// BODY START … BODY END, RESPONSE: 200 OK, FROM: url.
//
// Dio's LogInterceptor (Flutter): *** Request ***, uri:, method:, headers:, data:,
// *** Response ***, statusCode:, Response Text:, *** DioException ***.
//
// A block's lines must come from one thread and one tag, and close together in time, so the
// app's other log lines don't leak into a request.
type httpLog struct {
	mu      sync.Mutex
	emit    func(event.Net)
	current map[string]*logBlock // the block each thread is writing, by thread and tag
	waiting []*logBlock          // requests logged, their responses not yet
}

type logBlock struct {
	n       event.Net
	key     string // thread and tag
	format  string // okhttp | ktor | dio | alamofire | moya
	part    int    // partRequest or partResponse
	section string // "", "headers" or "body"
	lastLen int    // length of the last body line, to rejoin lines Android split at 4000 bytes
	pending string // moya: the response header whose value is on the next line
	seen    time.Time
}

const (
	partRequest = iota + 1
	partResponse
)

// How long a block may go quiet before it's taken to be finished, and how long a request may
// wait for its response.
const (
	blockIdle    = 750 * time.Millisecond
	responseWait = 60 * time.Second
)

func newHTTPLog(emit func(event.Net)) *httpLog {
	return &httpLog{emit: emit, current: map[string]*logBlock{}}
}

var (
	okReqRe      = regexp.MustCompile(`^--> ([A-Z]+) (\S+)`)
	okRespRe     = regexp.MustCompile(`^<-- (\d{3})\b.*?(\S+://\S+)\s+\((\d+)ms`)
	okFailRe     = regexp.MustCompile(`^<-- HTTP FAILED: (.*)`)
	headerRe     = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*): ?(.*)$`)
	ktorReqRe    = regexp.MustCompile(`^REQUEST: (\S+)`)
	ktorMethodRe = regexp.MustCompile(`^METHOD: HttpMethod\(value=(\w+)\)`)
	ktorRespRe   = regexp.MustCompile(`^RESPONSE: (\d{3})`)
	ktorFromRe   = regexp.MustCompile(`^FROM: (\S+)`)
	ktorHeaderRe = regexp.MustCompile(`^-> ([^:]+): ?(.*)$`)
	dioURIRe     = regexp.MustCompile(`^uri: (\S+)`)
	dioMethodRe  = regexp.MustCompile(`^method: (\w+)`)
	dioStatusRe  = regexp.MustCompile(`^statusCode: (\d{3})`)
	dioHeaderRe  = regexp.MustCompile(`^\s+([^:\s][^:]*): ?(.*)$`)
)

// line takes one of the app's log lines, and reports whether it belonged to a request.
func (h *httpLog) line(e event.Event) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	key := strconv.FormatInt(e.TID, 10) + "|" + e.Tag + e.Category
	msg := strings.TrimRight(e.Message, " \r")
	cur := h.current[key]
	if cur != nil && now.Sub(cur.seen) > blockIdle {
		h.finish(key)
		cur = nil
	}
	start := func(format string, part int, n event.Net) *logBlock {
		h.finish(key)
		n.Time, n.Kind = e.Time, "logged"
		b := &logBlock{n: n, key: key, format: format, part: part, seen: now}
		h.current[key] = b
		if part == partRequest {
			h.waiting = append(h.waiting, b)
		}
		return b
	}
	// answer turns the current provisional response block into the response of the waiting
	// request for url, when there is one.
	answer := func(url string) {
		if w := h.take(url, key); w != nil {
			w.n.Status, w.n.Failed, w.n.Error = cur.n.Status, cur.n.Failed, cur.n.Error
			w.part, w.section, w.seen = partResponse, cur.section, now
			w.finishedAt(cur.n.Time)
			h.current[key] = w
		} else {
			cur.n.URL = url
		}
	}

	switch m := okReqRe.FindStringSubmatch(msg); {
	// OkHttp-style
	case strings.HasPrefix(msg, "--> END"):
		if cur != nil && cur.format == "okhttp" {
			delete(h.current, key) // the request is logged; it waits for its response
			return true
		}
	case m != nil:
		start("okhttp", partRequest, event.Net{Method: m[1], URL: m[2]})
		return true
	case strings.HasPrefix(msg, "<-- END"):
		if cur != nil && cur.format == "okhttp" {
			h.finish(key)
			return true
		}
	case okFailRe.MatchString(msg):
		f := okFailRe.FindStringSubmatch(msg)
		if b := h.take("", key); b != nil {
			b.n.Failed, b.n.Error = true, f[1]
			b.finishedAt(e.Time)
			h.done(b)
		}
		return true
	case okRespRe.MatchString(msg):
		r := okRespRe.FindStringSubmatch(msg)
		b := h.take(r[2], key)
		if b == nil {
			b = &logBlock{n: event.Net{Time: e.Time, Kind: "logged", URL: r[2]}, key: key, format: "okhttp"}
		}
		h.finish(key)
		b.n.Status, b.n.DurationMs = atoi(r[1]), atoi(r[3])
		b.finishedAt(e.Time)
		b.part, b.section, b.seen, b.lastLen = partResponse, "", now, 0
		h.current[key] = b
		return true

	// Ktor
	case ktorReqRe.MatchString(msg):
		start("ktor", partRequest, event.Net{URL: ktorReqRe.FindStringSubmatch(msg)[1]})
		return true
	case ktorRespRe.MatchString(msg):
		start("ktor", partResponse, event.Net{Status: atoi(ktorRespRe.FindStringSubmatch(msg)[1])})
		return true

	// Alamofire and Moya (see httplog_swift.go)
	case afReqRe.MatchString(msg):
		r := afReqRe.FindStringSubmatch(msg)
		start("alamofire", partRequest, event.Net{Method: r[1], URL: r[2]})
		return true
	case moyaRe.MatchString(msg):
		h.moya(moyaRe.FindStringSubmatch(msg), e, key, now, start)
		return true

	// Dio
	case msg == "*** Request ***":
		start("dio", partRequest, event.Net{})
		return true
	case msg == "*** Response ***":
		start("dio", partResponse, event.Net{})
		return true
	case msg == "*** DioException ***":
		start("dio", partResponse, event.Net{Failed: true})
		return true
	}

	if cur == nil {
		return false
	}
	cur.seen = now
	switch cur.format {
	case "alamofire":
		h.alamofireLine(cur, msg)
	case "moya":
		h.moyaLine(cur, msg)
	case "ktor":
		switch {
		case ktorMethodRe.MatchString(msg):
			cur.n.Method = ktorMethodRe.FindStringSubmatch(msg)[1]
		case ktorFromRe.MatchString(msg):
			answer(ktorFromRe.FindStringSubmatch(msg)[1])
		case msg == "BODY START":
			cur.section = "body"
		case msg == "BODY END":
			cur.section = ""
			if cur.part == partResponse {
				h.finish(key)
			}
		case cur.section == "body":
			cur.addBody(msg)
		case ktorHeaderRe.MatchString(msg):
			kv := ktorHeaderRe.FindStringSubmatch(msg)
			cur.addHeader(kv[1], kv[2])
		}
	case "dio":
		switch {
		case dioURIRe.MatchString(msg):
			url := dioURIRe.FindStringSubmatch(msg)[1]
			if cur.part == partRequest {
				cur.n.URL = url
			} else {
				answer(url)
			}
		case dioMethodRe.MatchString(msg):
			cur.n.Method = dioMethodRe.FindStringSubmatch(msg)[1]
		case dioStatusRe.MatchString(msg):
			cur.n.Status = atoi(dioStatusRe.FindStringSubmatch(msg)[1])
		case msg == "headers:":
			cur.section = "headers"
		case msg == "data:" || msg == "Response Text:":
			cur.section = "body"
		case cur.section == "headers" && dioHeaderRe.MatchString(msg):
			kv := dioHeaderRe.FindStringSubmatch(msg)
			cur.addHeader(kv[1], kv[2])
		case cur.section == "body":
			cur.addBody(msg)
		case cur.n.Failed && strings.HasPrefix(msg, "message:"):
			cur.n.Error = strings.TrimSpace(strings.TrimPrefix(msg, "message:"))
		}
	default: // okhttp: headers, a blank line, then the body
		switch {
		case msg == "":
			cur.section = "body"
		case cur.section != "body" && headerRe.MatchString(msg):
			kv := headerRe.FindStringSubmatch(msg)
			h.balance(cur, kv[1], kv[2]).addHeader(kv[1], kv[2])
		default:
			cur.section = "body"
			cur.addBody(msg)
		}
	}
	return true
}

// balance picks the request a header line belongs to. Logs without thread ids (iPhones)
// interleave requests sent together, so a header the current request already has goes to the
// other open request from the same logger that lacks it.
func (h *httpLog) balance(cur *logBlock, name, value string) *logBlock {
	if cur.part != partRequest || !cur.hasHeader(name, value) {
		return cur
	}
	for _, b := range h.waiting {
		if b != cur && b.key == cur.key && !b.hasHeader(name, value) {
			return b
		}
	}
	return cur
}

func (b *logBlock) hasHeader(name, value string) bool {
	for _, h := range b.n.RequestHeaders {
		if strings.EqualFold(h.Name, name) && h.Value == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

// finishedAt records when a request's response (or failure) was logged.
func (b *logBlock) finishedAt(t time.Time) {
	if b.n.Started.IsZero() && b.n.Method != "" { // a response with no logged request has no start
		b.n.Started = b.n.Time
	}
	b.n.Time = t
}

func (b *logBlock) addHeader(name, value string) {
	h := event.Header{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)}
	if b.part == partResponse {
		b.n.ResponseHeaders = append(b.n.ResponseHeaders, h)
	} else {
		b.n.RequestHeaders = append(b.n.RequestHeaders, h)
	}
}

func (b *logBlock) addBody(line string) {
	body := &b.n.RequestBody
	if b.part == partResponse {
		body = &b.n.ResponseBody
	}
	if *body != "" && b.lastLen < 4000 { // Android splits long log messages every ~4000 bytes
		*body += "\n"
	}
	*body += line
	b.lastLen = len(line)
}

// take removes and returns the waiting request for url (or, with no url, the newest one this
// thread started), or nil.
func (h *httpLog) take(url, key string) *logBlock {
	for i := len(h.waiting) - 1; i >= 0; i-- {
		if b := h.waiting[i]; (url != "" && b.n.URL == url) || (url == "" && b.key == key) {
			h.waiting = append(h.waiting[:i], h.waiting[i+1:]...)
			return b
		}
	}
	return nil
}

// finish ends the block key is writing: a response is complete; a request keeps waiting,
// except Alamofire's, whose response is in the same message: the log cut it short.
func (h *httpLog) finish(key string) {
	b := h.current[key]
	delete(h.current, key)
	switch {
	case b == nil:
	case b.part == partResponse:
		h.done(b)
	case b.format == "alamofire":
		h.unwait(b)
		b.n.Error = "the rest was cut off in the log"
		h.done(b)
	}
}

func (h *httpLog) done(b *logBlock) {
	b.n.RequestBody = strings.TrimRight(b.n.RequestBody, "\n")
	b.n.ResponseBody = strings.TrimRight(b.n.ResponseBody, "\n")
	h.emit(b.n)
}

// flush finishes responses that have gone quiet, and reports requests whose response never
// came; with all, everything still open.
func (h *httpLog) flush(all bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for key, b := range h.current {
		if all || now.Sub(b.seen) > blockIdle {
			h.finish(key)
		}
	}
	kept := h.waiting[:0]
	for _, b := range h.waiting {
		if all || now.Sub(b.seen) > responseWait {
			b.n.Error = "no response logged"
			h.done(b)
		} else {
			kept = append(kept, b)
		}
	}
	h.waiting = kept
}

// start flushes quiet blocks in the background. The returned stop flushes everything left and
// returns once that's been emitted.
func (h *httpLog) start() (stop func()) {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		h.run(done)
		close(finished)
	}()
	return func() {
		close(done)
		<-finished
	}
}

// run flushes quiet blocks until done is closed, then everything left.
func (h *httpLog) run(done <-chan struct{}) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			h.flush(true)
			return
		case <-t.C:
			h.flush(false)
		}
	}
}
