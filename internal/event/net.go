package event

import "time"

// Net is one network request or connection an app made. What's filled in depends on what the
// platform exposes: iOS simulators give requests with URLs, iPhones give requests and the
// hostnames of new connections, Android gives connections. On every platform, requests the
// app logs itself (OkHttp-style, Ktor, Dio) come as kind "logged", with headers and bodies.
type Net struct {
	Time       time.Time `json:"time"`
	Platform   string    `json:"platform"`
	Device     string    `json:"device"`
	Kind       string    `json:"kind"`             // request | logged | http | tls | tcp | udp | dns | log
	URL        string    `json:"url,omitempty"`    // the request's URL, when known
	Host       string    `json:"host,omitempty"`   // the server's name, when known
	IP         string    `json:"ip,omitempty"`     // the server's address, when known
	Port       int       `json:"port,omitempty"`   // the server's port, when known
	Method     string    `json:"method,omitempty"` // when known
	Status     int       `json:"status,omitempty"` // the HTTP status; 0 when there was none
	Failed     bool      `json:"failed,omitempty"` // the request failed without a response
	Error      string    `json:"error,omitempty"`  // why it failed, when known
	DurationMs int       `json:"duration_ms,omitempty"`
	Protocol   string    `json:"protocol,omitempty"` // h2, h3, http/1.1
	Bytes      int       `json:"bytes,omitempty"`    // response bytes
	Conn       int       `json:"connection,omitempty"`
	Cached     bool      `json:"cached,omitempty"` // answered from the app's HTTP cache

	// Kind "logged": what the app's HTTP logging recorded, as much as its log level includes.
	RequestHeaders  []Header `json:"request_headers,omitempty"`
	ResponseHeaders []Header `json:"response_headers,omitempty"`
	RequestBody     string   `json:"request_body,omitempty"`
	ResponseBody    string   `json:"response_body,omitempty"`

	// Kind "log": one of the app's HTTP log lines that wasn't part of a recognized block.
	Log   string `json:"log,omitempty"`
	Tag   string `json:"tag,omitempty"`   // its logcat tag or Logger category
	Level string `json:"level,omitempty"` // its level letter
}

// Header is one HTTP header, in the order it was logged.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
