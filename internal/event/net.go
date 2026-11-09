package event

import "time"

// Net is one network request or connection an app made. What's filled in depends on what the
// platform exposes: iOS simulators give requests with URLs, iPhones give requests and the
// hostnames of new connections, Android gives connections.
type Net struct {
	Time       time.Time `json:"time"`
	Platform   string    `json:"platform"`
	Device     string    `json:"device"`
	Kind       string    `json:"kind"`             // request | http | tls | tcp | udp | dns | log
	URL        string    `json:"url,omitempty"`    // the request's URL, when known
	Host       string    `json:"host,omitempty"`   // the server's name, when known
	IP         string    `json:"ip,omitempty"`     // the server's address, when known
	Port       int       `json:"port,omitempty"`   // the server's port, when known
	Method     string    `json:"method,omitempty"` // plain-HTTP requests
	Status     int       `json:"status,omitempty"` // the HTTP status; 0 when there was none
	Failed     bool      `json:"failed,omitempty"` // the request failed without a response
	DurationMs int       `json:"duration_ms,omitempty"`
	Protocol   string    `json:"protocol,omitempty"` // h2, h3, http/1.1
	Bytes      int       `json:"bytes,omitempty"`    // response bytes
	Conn       int       `json:"connection,omitempty"`
	Cached     bool      `json:"cached,omitempty"` // answered from the app's HTTP cache
	Log        string    `json:"log,omitempty"`    // kind "log": the app's own HTTP log line
	Tag        string    `json:"tag,omitempty"`    // kind "log": its logcat tag
	Level      string    `json:"level,omitempty"`  // kind "log": its level letter
}
