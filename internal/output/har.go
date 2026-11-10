package output

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

// HAR saves requests to a HAR file (HTTP Archive 1.2), which Chrome DevTools, Proxyman, Charles
// and Insomnia open. The file is rewritten after each request, so it's complete whenever saw
// stops. Only requests with a URL are saved; connections aren't requests.
type HAR struct {
	path    string
	creator harCreator
	mu      sync.Mutex
	entries []harEntry
}

func NewHAR(path, version string) *HAR {
	return &HAR{path: path, creator: harCreator{Name: "saw", Version: version}}
}

type (
	harCreator struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	harEntry struct {
		Started  string      `json:"startedDateTime"`
		Time     int         `json:"time"`
		Request  harRequest  `json:"request"`
		Response harResponse `json:"response"`
		Cache    struct{}    `json:"cache"`
		Timings  harTimings  `json:"timings"`
		Platform string      `json:"_platform,omitempty"`
		Device   string      `json:"_device,omitempty"`
		Error    string      `json:"_error,omitempty"`
	}
	harRequest struct {
		Method      string     `json:"method"`
		URL         string     `json:"url"`
		HTTPVersion string     `json:"httpVersion"`
		Cookies     []struct{} `json:"cookies"`
		Headers     []harPair  `json:"headers"`
		QueryString []harPair  `json:"queryString"`
		PostData    *harPost   `json:"postData,omitempty"`
		HeadersSize int        `json:"headersSize"`
		BodySize    int        `json:"bodySize"`
	}
	harResponse struct {
		Status      int        `json:"status"`
		StatusText  string     `json:"statusText"`
		HTTPVersion string     `json:"httpVersion"`
		Cookies     []struct{} `json:"cookies"`
		Headers     []harPair  `json:"headers"`
		Content     harContent `json:"content"`
		RedirectURL string     `json:"redirectURL"`
		HeadersSize int        `json:"headersSize"`
		BodySize    int        `json:"bodySize"`
	}
	harPair struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	harPost struct {
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	}
	harContent struct {
		Size     int    `json:"size"`
		MimeType string `json:"mimeType"`
		Text     string `json:"text,omitempty"`
	}
	harTimings struct {
		Send    int `json:"send"`
		Wait    int `json:"wait"`
		Receive int `json:"receive"`
	}
)

// Add saves a request, reporting whether n was one, and rewrites the file.
func (h *HAR) Add(n event.Net) (bool, error) {
	if n.URL == "" || (n.Kind != "logged" && n.Kind != "request" && n.Kind != "http") {
		return false, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, harFrom(n))
	return true, h.write()
}

// Count is how many requests are saved.
func (h *HAR) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

// Write writes the file as it stands, even with no requests in it.
func (h *HAR) Write() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.write()
}

// write replaces the file through a temporary one, so it's never seen half-written.
func (h *HAR) write() error {
	entries := h.entries
	if entries == nil {
		entries = []harEntry{}
	}
	doc := map[string]any{"log": map[string]any{"version": "1.2", "creator": h.creator, "entries": entries}}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".saw-*.har")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), h.path)
}

func harFrom(n event.Net) harEntry {
	started := n.Started
	if started.IsZero() {
		started = n.Time.Add(-time.Duration(n.DurationMs) * time.Millisecond)
	}
	method := n.Method
	if method == "" {
		method = "GET"
	}
	version := "HTTP/1.1"
	switch n.Protocol {
	case "h2":
		version = "HTTP/2"
	case "h3":
		version = "HTTP/3"
	}
	e := harEntry{
		Started: started.Format(time.RFC3339Nano),
		Time:    n.DurationMs,
		Request: harRequest{
			Method: method, URL: n.URL, HTTPVersion: version, Cookies: []struct{}{},
			Headers: harHeaders(n.RequestHeaders), QueryString: harQuery(n.URL),
			HeadersSize: -1, BodySize: len(n.RequestBody),
		},
		Response: harResponse{
			Status: n.Status, StatusText: http.StatusText(n.Status), HTTPVersion: version, Cookies: []struct{}{},
			Headers:     harHeaders(n.ResponseHeaders),
			Content:     harContent{Size: netSize(n), MimeType: contentType(n.ResponseHeaders), Text: n.ResponseBody},
			HeadersSize: -1, BodySize: -1,
		},
		Timings:  harTimings{Wait: n.DurationMs},
		Platform: n.Platform, Device: n.Device, Error: n.Error,
	}
	if n.RequestBody != "" {
		e.Request.PostData = &harPost{MimeType: contentType(n.RequestHeaders), Text: n.RequestBody}
	}
	if n.Status == 0 {
		e.Response.HTTPVersion = ""
	}
	return e
}

func harHeaders(hs []event.Header) []harPair {
	out := make([]harPair, 0, len(hs))
	for _, h := range hs {
		out = append(out, harPair{Name: h.Name, Value: h.Value})
	}
	return out
}

// harQuery is the URL's query parameters, in order.
func harQuery(u string) []harPair {
	out := []harPair{}
	_, query, ok := strings.Cut(u, "?")
	if !ok {
		return out
	}
	query, _, _ = strings.Cut(query, "#")
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if uv, err := url.QueryUnescape(v); err == nil {
			v = uv
		}
		out = append(out, harPair{Name: k, Value: v})
	}
	return out
}

// contentType is the Content-Type header's value, or "".
func contentType(hs []event.Header) string {
	for _, h := range hs {
		if strings.EqualFold(h.Name, "content-type") {
			return strings.TrimSpace(h.Value)
		}
	}
	return ""
}
