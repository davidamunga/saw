package source

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

// Two iOS formats, for apps that send them to the unified log (NSLog, os_log, Logger); by
// default both print to stdout, which saw can't read on a device.
//
// Alamofire's DataResponse.debugDescription: one message for the whole request, logged when it
// finishes, with the time it took.
//
//	[Request]: POST https://api.example.com/v1/orders
//	    [Headers]:
//	        Content-Type: application/json
//	    [Body]:
//	        {"item":"1042"}
//	[Response]:
//	    [Status Code]: 201
//	    [Headers]:
//	        Content-Type: application/json
//	    [Body]:
//	        {"id":"ord_88"}
//	[Network Duration]: 0.30112791061401367s
//	[Serialization Duration]: 0.00012594s
//	[Result]: success(…)
//
// Moya's NetworkLoggerPlugin: one entry per part, the request's when it's sent and the
// response's when it arrives. The method comes last, and the response is Foundation's
// description of it, with its headers on the lines after.
//
//	Moya_Logger: [11/10/26, 10:00 AM] Request: https://api.example.com/v1/orders
//	Moya_Logger: [11/10/26, 10:00 AM] Request Headers: ["Content-Type": "application/json"]
//	Moya_Logger: [11/10/26, 10:00 AM] Request Body: {"item":"1042"}
//	Moya_Logger: [11/10/26, 10:00 AM] HTTP Request Method: POST
//	Moya_Logger: [11/10/26, 10:00 AM] Response: <NSHTTPURLResponse: 0x1008478a0> { URL: https://api.example.com/v1/orders } { Status Code: 201, Headers {
//	    "Content-Type" =     (
//	        "application/json"
//	    );
//	} }
//	Moya_Logger: [11/10/26, 10:00 AM] Response Body: {"id":"ord_88"}

var (
	afReqRe      = regexp.MustCompile(`^\[Request\]: ([A-Z]+) (\S+://\S+)`)
	afStatusRe   = regexp.MustCompile(`^\[Status Code\]: (\d{3})`)
	afDurationRe = regexp.MustCompile(`^\[Network Duration\]: ([0-9.]+)s`)
	afFailureRe  = regexp.MustCompile(`^\[Result\]: failure\((.*)\)?$`)

	moyaRe = regexp.MustCompile(`^Moya_Logger: \[[^\]]*\] (Request|Request Headers|Request Body Stream|Request Body|HTTP Request Method|Response|Response Body|Error): ?(.*)$`)
	// "Accept": "application/json", in a Swift dictionary's description
	swiftPairRe = regexp.MustCompile(`"((?:[^"\\]|\\.)*)": "((?:[^"\\]|\\.)*)"`)
	// <NSHTTPURLResponse: 0x…> { URL: https://… } { Status Code: 201, Headers {
	nsResponseRe = regexp.MustCompile(`\{ URL: (\S+) \} \{ Status Code: (\d{3})`)
	// "Content-Type" =     (      or, on one line, "Content-Type" = "application/json";
	nsHeaderRe = regexp.MustCompile(`^\s+"?([^"=]+?)"?\s+=\s+(.*?);?$`)
)

// alamofireLine takes a line inside an Alamofire block.
func (h *httpLog) alamofireLine(cur *logBlock, msg string) {
	t := strings.TrimSpace(msg)
	switch {
	case cur.section == "result": // the rest of the result's dump
	case t == "[Headers]:":
		cur.section = "headers"
	case t == "[Body]:":
		cur.section = "body"
	case strings.HasPrefix(t, "[Headers]: "), strings.HasPrefix(t, "[Body]: "): // None, or "N bytes"
		cur.section = ""
	case t == "[Response]:" || t == "[Response]: None":
		h.unwait(cur)
		cur.part, cur.section = partResponse, ""
		if t == "[Response]: None" {
			cur.n.Failed, cur.n.Error = true, "no response"
		}
	case afStatusRe.MatchString(t):
		cur.n.Status, cur.section = atoi(afStatusRe.FindStringSubmatch(t)[1]), ""
	case afDurationRe.MatchString(t):
		if s, err := strconv.ParseFloat(afDurationRe.FindStringSubmatch(t)[1], 64); err == nil {
			cur.n.DurationMs = int(s * 1000)
			cur.n.Started = cur.n.Time.Add(-time.Duration(s * float64(time.Second)))
		}
		cur.section = ""
	case strings.HasPrefix(t, "[Serialization Duration]"):
		cur.section = ""
	case strings.HasPrefix(t, "[Result]: "):
		if m := afFailureRe.FindStringSubmatch(t); m != nil && cur.n.Status == 0 {
			cur.n.Failed, cur.n.Error = true, strings.TrimSuffix(m[1], ")")
		}
		cur.section = "result"
	case cur.section == "headers" && headerRe.MatchString(t):
		kv := headerRe.FindStringSubmatch(t)
		cur.addHeader(kv[1], kv[2])
	case cur.section == "body":
		cur.addBody(strings.TrimPrefix(msg, "        "))
	}
}

// moya takes one Moya_Logger entry.
func (h *httpLog) moya(m []string, e event.Event, key string, now time.Time, start func(string, int, event.Net) *logBlock) {
	id, val := m[1], strings.TrimSpace(m[2])
	cur := h.current[key]
	inRequest := cur != nil && cur.format == "moya" && cur.part == partRequest
	switch id {
	case "Request":
		if strings.Contains(val, "://") && !strings.HasPrefix(val, "$ curl") {
			start("moya", partRequest, event.Net{URL: val})
		}
	case "Request Headers":
		if inRequest {
			for _, kv := range swiftPairRe.FindAllStringSubmatch(val, -1) {
				cur.addHeader(unescape(kv[1]), unescape(kv[2]))
			}
		}
	case "Request Body":
		if inRequest {
			cur.section = "body"
			cur.addBody(val)
		}
	case "HTTP Request Method":
		if inRequest {
			cur.n.Method, cur.section = val, ""
			delete(h.current, key) // the request is logged; it waits for its response
		}
	case "Response":
		h.finish(key)
		r := nsResponseRe.FindStringSubmatch(val)
		if r == nil { // "Received empty network response for …"
			h.fail("moya", val, e.Time)
			return
		}
		b := h.take(r[1], key)
		if b == nil {
			b = &logBlock{n: event.Net{Time: e.Time, Kind: "logged", URL: r[1]}, format: "moya"}
		}
		b.n.Status = atoi(r[2])
		b.finishedAt(e.Time)
		if !b.n.Started.IsZero() { // Moya doesn't log the time; the log's timestamps tell it
			b.n.DurationMs = int(b.n.Time.Sub(b.n.Started) / time.Millisecond)
		}
		b.key, b.part, b.section, b.seen, b.lastLen = key, partResponse, "nsheaders", now, 0
		h.current[key] = b
	case "Response Body":
		if cur != nil && cur.format == "moya" && cur.part == partResponse {
			cur.section, cur.lastLen = "body", 0
			cur.addBody(val)
		}
	case "Error":
		h.finish(key)
		h.fail("moya", val, e.Time)
	}
}

// moyaLine takes a line inside a Moya entry: the response's headers, or more of a body.
func (h *httpLog) moyaLine(cur *logBlock, msg string) {
	t := strings.TrimSpace(msg)
	switch {
	case cur.section == "nsheaders" && t == "} }":
		cur.section, cur.pending = "", ""
	case cur.section == "nsheaders" && t == ");":
		cur.pending = ""
	case cur.section == "nsheaders" && cur.pending != "":
		cur.addHeader(cur.pending, strings.Trim(strings.TrimSuffix(t, ","), `"`))
	case cur.section == "nsheaders" && nsHeaderRe.MatchString(msg):
		kv := nsHeaderRe.FindStringSubmatch(msg)
		if kv[2] == "(" {
			cur.pending = kv[1]
		} else {
			cur.addHeader(kv[1], strings.Trim(kv[2], `"`))
		}
	case cur.section == "body":
		cur.addBody(msg)
	}
}

// fail ends the oldest request of format still waiting for its response, as failed.
func (h *httpLog) fail(format, why string, t time.Time) {
	for _, b := range h.waiting {
		if b.format == format {
			h.unwait(b)
			b.n.Failed, b.n.Error = true, why
			b.finishedAt(t)
			h.done(b)
			return
		}
	}
}

// unwait stops b waiting for a response.
func (h *httpLog) unwait(b *logBlock) {
	for i, w := range h.waiting {
		if w == b {
			h.waiting = append(h.waiting[:i], h.waiting[i+1:]...)
			return
		}
	}
}

func unescape(s string) string {
	if u, err := strconv.Unquote(`"` + s + `"`); err == nil {
		return u
	}
	return s
}
