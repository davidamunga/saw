package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/event"
)

func TestHAR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.har")
	h := NewHAR(path, "1.2.3")
	if err := h.Write(); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(path)
		var doc map[string]any
		if err != nil || json.Unmarshal(data, &doc) != nil {
			t.Fatalf("bad HAR: %v %s", err, data)
		}
		return doc["log"].(map[string]any)
	}
	if log := read(); len(log["entries"].([]any)) != 0 || log["version"] != "1.2" {
		t.Errorf("empty HAR: %v", log)
	}

	post := logged
	post.Device, post.Platform = "Pixel 8", "android"
	post.Started = at.Add(-356 * time.Millisecond)
	for _, n := range []event.Net{
		post,
		{Time: at, Kind: "request", URL: "https://api.example.com/search?q=a%20b&page=2#top", Status: 200, DurationMs: 90, Protocol: "h2", Bytes: 1200},
		{Time: at, Kind: "logged", Method: "GET", URL: "https://down.example.com/", Failed: true, Error: "UnknownHostException"},
		{Time: at, Kind: "tcp", IP: "203.0.113.7", Port: 443}, // not a request
		{Time: at, Kind: "request", Conn: 3}, // no URL
	} {
		if _, err := h.Add(n); err != nil {
			t.Fatal(err)
		}
	}
	if h.Count() != 3 {
		t.Fatalf("got %d entries", h.Count())
	}
	entries := read()["entries"].([]any)
	first := entries[0].(map[string]any)
	req, resp := first["request"].(map[string]any), first["response"].(map[string]any)
	if first["startedDateTime"] != "2026-11-09T09:41:01.758Z" || first["time"].(float64) != 356 || first["_device"] != "Pixel 8" {
		t.Errorf("entry: %v", first)
	}
	if req["method"] != "POST" || req["postData"].(map[string]any)["mimeType"] != "application/json" || len(req["headers"].([]any)) != 3 {
		t.Errorf("request: %v", req)
	}
	content := resp["content"].(map[string]any)
	if resp["status"].(float64) != 200 || resp["statusText"] != "OK" || content["text"] != `{"auth":"a1b2:c3d4"}` || content["size"].(float64) != 300 {
		t.Errorf("response: %v", resp)
	}
	second := entries[1].(map[string]any)
	q := second["request"].(map[string]any)["queryString"].([]any)
	if len(q) != 2 || q[0].(map[string]any)["value"] != "a b" || second["request"].(map[string]any)["httpVersion"] != "HTTP/2" {
		t.Errorf("query: %v", second)
	}
	third := entries[2].(map[string]any)
	if third["_error"] != "UnknownHostException" || third["response"].(map[string]any)["status"].(float64) != 0 {
		t.Errorf("failed: %v", third)
	}

	if err := NewHAR(filepath.Join(t.TempDir(), "missing", "s.har"), "x").Write(); err == nil {
		t.Error("want an error for a folder that doesn't exist")
	}
}
