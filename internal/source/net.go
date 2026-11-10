package source

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/run"
)

// StreamNet sends the app's network requests and connections to emit until ctx ends. Nothing
// is decrypted and nothing on the device is changed:
//   - iOS simulator: Apple's networking logs from the app's process: URL, status, timing, size.
//   - iPhone/iPad: the same logs (status, timing, size; iOS hides URLs on devices), plus a
//     packet capture of the app for the hostname of each new connection (TLS server name).
//   - Android: the app's TCP and UDP connections, from the kernel's socket table.
func StreamNet(ctx context.Context, s Spec, emit func(event.Net)) error {
	switch {
	case s.Device.Platform == "android":
		return netAndroid(ctx, s, emit)
	case s.Device.Kind == "simulator":
		return netSimulator(ctx, s, emit)
	case s.Device.Platform == "ios":
		return netIOSDevice(ctx, s, emit)
	}
	return fmt.Errorf("unsupported device %s", s.Device)
}

// ---- iOS: Apple's networking logs ----

func netSimulator(ctx context.Context, s Spec, emit func(event.Net)) error {
	if err := s.checkIOS(); err != nil {
		return err
	}
	process, err := simProcess(ctx, s)
	if err != nil {
		return err
	}
	predicate := fmt.Sprintf(`process == %q AND (subsystem == "com.apple.CFNetwork" OR subsystem == "com.apple.network"`+
		` OR NOT (subsystem BEGINSWITH "com.apple."))`, process)
	var mu sync.Mutex
	send := func(n event.Net) {
		mu.Lock()
		defer mu.Unlock()
		n.Platform, n.Device = "ios", s.Device.Name
		emit(n)
	}
	cf, logged := newCFNetwork(), newHTTPLog(send)
	defer logged.start()()
	s.started()
	return simLog(ctx, s, predicate, func(e event.Event) {
		s.netIOSLine(e, cf, logged, send)
	})
}

// netIOSLine routes one iOS log line: Apple's networking logs become requests; the app's own
// lines feed its logged requests.
func (s Spec) netIOSLine(e event.Event, cf *cfNetwork, logged *httpLog, send func(event.Net)) {
	if n, ok := cf.event(e); ok {
		send(n)
		return
	}
	// The app's HTTP logging may go through NSLog even when it uses a Logger subsystem otherwise.
	if !strings.HasPrefix(e.Subsystem, "com.apple.") && (s.keepIOS(e) || nsLog(e)) {
		appHTTPLog(e, logged, send)
	}
}

// appHTTPLog passes one of the app's own log lines to logged, and shows it on its own when it
// looks like HTTP but isn't part of a request block logged's formats know.
func appHTTPLog(e event.Event, logged *httpLog, send func(event.Net)) {
	tag := e.Tag
	if tag == "" {
		tag = e.Category
	}
	for _, l := range strings.Split(e.Message, "\n") {
		one := e
		one.Message = l
		if !logged.line(one) && httpLogLine(l) {
			send(event.Net{Time: e.Time, Kind: "log", Log: l, Tag: tag, Level: e.Level.Letter()})
		}
	}
}

func netIOSDevice(ctx context.Context, s Spec, emit func(event.Net)) error {
	if err := s.checkIOS(); err != nil {
		return err
	}
	process, err := deviceProcess(ctx, s)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	send := func(n event.Net) {
		mu.Lock()
		defer mu.Unlock()
		n.Platform, n.Device = "ios", s.Device.Name
		emit(n)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	udid := s.Device.ID
	cf, pc, logged := newCFNetwork(), &pcapParser{}, newHTTPLog(send)
	defer logged.start()()
	s.started()
	errc := make(chan error, 2)
	go func() {
		errc <- run.Lines(ctx, func(line string) {
			if e, ok := ParseDeviceLog([]byte(line)); ok {
				s.netIOSLine(e, cf, logged, send)
			}
		}, "pymobiledevice3", "syslog", "live", "--udid", udid, "--format", "json", "--process-name", process)
	}()
	go func() {
		errc <- run.Lines(ctx, func(line string) {
			if n, ok := pc.line(line); ok {
				send(n)
			}
		}, "pymobiledevice3", "pcap", "--udid", udid, "--process", process)
	}()
	err = <-errc // the first to stop ends both
	cancel()
	<-errc
	return err
}

var (
	// [C1 EB11… Hostname#30b4cfe5:443 quic-connection, url: https://example.com/, …] start
	connStartRe = regexp.MustCompile(`^\[C(\d+) .*?url: ([^,\s]+)`)
	// Task <…>.<1> summary for task success {transaction_duration_ms=941, response_status=200, …}
	summaryRe = regexp.MustCompile(`summary for task (\w+) \{(.*)\}`)
)

// cfNetwork turns CFNetwork's per-request summaries into requests, naming each by the URL its
// connection was opened for.
type cfNetwork struct{ urls map[int]string }

func newCFNetwork() *cfNetwork { return &cfNetwork{urls: map[int]string{}} }

func (c *cfNetwork) event(e event.Event) (event.Net, bool) {
	switch e.Subsystem {
	case "com.apple.network":
		if m := connStartRe.FindStringSubmatch(e.Message); m != nil && strings.HasPrefix(m[2], "http") {
			n, _ := strconv.Atoi(m[1])
			c.urls[n] = m[2]
		}
	case "com.apple.CFNetwork":
		m := summaryRe.FindStringSubmatch(e.Message)
		if m == nil {
			return event.Net{}, false
		}
		kv := parseKV(m[2])
		if kv["response_status"] == "-1" && kv["connection"] == "" {
			return event.Net{}, false // a wrapper task's twin of a request already reported
		}
		n := event.Net{Time: e.Time, Kind: "request",
			Status: atoi(kv["response_status"]), DurationMs: atoi(kv["transaction_duration_ms"]),
			Bytes: atoi(kv["response_bytes"]), Protocol: kv["protocol"], Conn: atoi(kv["connection"]), Cached: kv["cache_hit"] == "true"}
		n.Failed = m[1] != "success" && n.Status == 0
		if u, ok := c.urls[n.Conn]; ok {
			if pu, err := url.Parse(u); err == nil {
				n.Host = pu.Hostname()
			}
			// A reused connection was opened for an earlier request, so only its host applies.
			if kv["reused"] != "1" {
				n.URL = u
			}
		}
		return n, true
	}
	return event.Net{}, false
}

// parseKV reads `a=1, b="h2", c=false`.
func parseKV(s string) map[string]string {
	kv := map[string]string{}
	for _, part := range strings.Split(s, ", ") {
		if k, v, ok := strings.Cut(part, "="); ok {
			kv[strings.TrimSpace(k)] = strings.Trim(v, `"`)
		}
	}
	return kv
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ---- iPhone/iPad: packet capture ----

// pymobiledevice3 pcap prints each packet as a header line, a hex dump, and a blank line:
//
//	2026-11-09 15:42:12.801991: Process Example (3126), Interface: en (ethernetCsmacd), Family: AF_INET
//	00000000: 48 3A 02 52 D5 34 7A 5F  91 D6 DF 65 08 00 45 00  H:.R.4z_...e..E.
var pcapHeadRe = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+): Process `)

type pcapParser struct {
	t   time.Time
	buf []byte
	in  bool
}

func (p *pcapParser) line(l string) (event.Net, bool) {
	if m := pcapHeadRe.FindStringSubmatch(l); m != nil {
		n, ok := p.flush()
		p.t, _ = time.ParseInLocation("2006-01-02 15:04:05.999999", m[1], time.Local)
		p.buf, p.in = p.buf[:0], true
		return n, ok
	}
	if p.in && len(l) > 10 && l[8] == ':' {
		end := min(58, len(l)) // the hex columns; the ASCII column starts after them
		if b, err := hex.DecodeString(strings.ReplaceAll(l[10:end], " ", "")); err == nil {
			p.buf = append(p.buf, b...)
		}
		return event.Net{}, false
	}
	if strings.TrimSpace(l) == "" {
		return p.flush()
	}
	return event.Net{}, false
}

func (p *pcapParser) flush() (event.Net, bool) {
	if !p.in {
		return event.Net{}, false
	}
	p.in = false
	n, ok := parsePacket(p.buf)
	n.Time = p.t
	return n, ok
}

// parsePacket finds a new HTTPS connection (its TLS server name) or a plain-HTTP request in
// one packet: an Ethernet frame (Wi-Fi) or a bare IP packet (cellular).
func parsePacket(b []byte) (event.Net, bool) {
	ip := b
	if len(b) >= 14 && (b[12] == 0x08 && b[13] == 0x00 || b[12] == 0x86 && b[13] == 0xDD) {
		ip = b[14:]
	}
	var dst net.IP
	var proto byte
	var seg []byte
	switch {
	case len(ip) >= 20 && ip[0]>>4 == 4:
		ihl := int(ip[0]&15) * 4
		if len(ip) < ihl {
			return event.Net{}, false
		}
		proto, dst, seg = ip[9], net.IP(ip[16:20]), ip[ihl:]
	case len(ip) >= 40 && ip[0]>>4 == 6:
		proto, dst, seg = ip[6], net.IP(ip[24:40]), ip[40:]
	default:
		return event.Net{}, false
	}
	if proto != 6 || len(seg) < 20 {
		return event.Net{}, false
	}
	port := int(seg[2])<<8 | int(seg[3])
	off := int(seg[12]>>4) * 4
	if len(seg) < off {
		return event.Net{}, false
	}
	data := seg[off:]
	if host := tlsServerName(data); host != "" {
		return event.Net{Kind: "tls", Host: host, IP: dst.String(), Port: port}, true
	}
	if method, host, path, ok := httpRequest(data); ok {
		u := "http://" + host + path
		if port != 80 {
			u = "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
		}
		return event.Net{Kind: "http", Method: method, URL: u, Host: host, IP: dst.String(), Port: port}, true
	}
	return event.Net{}, false
}

// tlsServerName reads the server name from a TLS ClientHello, or "".
func tlsServerName(d []byte) string {
	// record header (5), handshake type 1 (ClientHello), length (3), version (2), random (32)
	if len(d) < 44 || d[0] != 0x16 || d[5] != 1 {
		return ""
	}
	i := 43
	i += 1 + int(d[i]) // session id
	if i+2 > len(d) {
		return ""
	}
	i += 2 + (int(d[i])<<8 | int(d[i+1])) // cipher suites
	if i >= len(d) {
		return ""
	}
	i += 1 + int(d[i]) // compression methods
	if i+2 > len(d) {
		return ""
	}
	end := i + 2 + (int(d[i])<<8 | int(d[i+1]))
	i += 2
	for i+4 <= end && i+4 <= len(d) {
		typ, n := int(d[i])<<8|int(d[i+1]), int(d[i+2])<<8|int(d[i+3])
		if typ == 0 && i+9 <= len(d) { // server_name: list length (2), name type (1), name length (2), name
			ln := int(d[i+7])<<8 | int(d[i+8])
			if i+9+ln <= len(d) {
				return string(d[i+9 : i+9+ln])
			}
			return ""
		}
		i += 4 + n
	}
	return ""
}

var httpMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

// httpRequest reads a plain-HTTP request's method, Host header and path.
func httpRequest(d []byte) (method, host, path string, ok bool) {
	head, _, _ := strings.Cut(string(d), "\r\n\r\n")
	lines := strings.Split(head, "\r\n")
	parts := strings.Fields(lines[0])
	if len(parts) != 3 || !strings.HasPrefix(parts[2], "HTTP/") {
		return "", "", "", false
	}
	for _, m := range httpMethods {
		if parts[0] == m {
			for _, l := range lines[1:] {
				if k, v, found := strings.Cut(l, ":"); found && strings.EqualFold(k, "host") {
					host = strings.TrimSpace(v)
				}
			}
			return m, host, parts[1], host != ""
		}
	}
	return "", "", "", false
}

// ---- Android: the kernel's socket table ----

// netAndroid polls /proc/net for the app's sockets (adb's shell user may read them, with each
// socket's owning uid) and reports each new connection once. It also reads the app's own log:
// hostnames in its URLs name the connections they resolve to, and its HTTP log lines (URLs,
// OkHttp's --> and <--) are shown, since Android gives no other view of requests.
func netAndroid(ctx context.Context, s Spec, emit func(event.Net)) error {
	if s.App.Android == "" {
		return fmt.Errorf("app %q has no android package in saw.json", s.App.Name)
	}
	serial := s.Device.ID
	uid, err := packageUID(ctx, serial, s.App.Android)
	if err != nil {
		return err
	}
	uids := uidNames(uid)
	logcat := []string{"-s", serial, "logcat", "-v", "threadtime", "-v", "year", "-v", "uid"}
	names := newHostNames()

	// Learn hostnames from recent history first, so already-open connections get names.
	if hist, err := run.Output(ctx, "adb", append(logcat, "-d", "-t", "3000")...); err == nil {
		for _, line := range strings.Split(hist, "\n") {
			if e, lineUID, ok := ParseLogcat(line); ok && uids[lineUID] {
				names.learnFrom(ctx, e.Message)
			}
		}
		names.wait(1500 * time.Millisecond)
	}

	var mu sync.Mutex
	send := func(n event.Net) {
		mu.Lock()
		defer mu.Unlock()
		n.Platform, n.Device = "android", s.Device.Name
		emit(n)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logged := newHTTPLog(send)
	defer logged.start()()
	s.started()
	errc := make(chan error, 2)
	go func() {
		errc <- run.Lines(ctx, func(line string) {
			e, lineUID, ok := ParseLogcat(line)
			if !ok || !uids[lineUID] {
				return
			}
			names.learnFrom(ctx, e.Message)
			appHTTPLog(e, logged, send)
		}, "adb", append(logcat, "-T", s.logcatStart())...)
	}()
	go func() {
		const script = "while true; do echo SAW-TCP; cat /proc/net/tcp /proc/net/tcp6 2>/dev/null; " +
			"echo SAW-UDP; cat /proc/net/udp /proc/net/udp6 2>/dev/null; echo SAW-END; sleep 0.25; done"
		open, now := map[string]bool{}, map[string]bool{}
		proto := ""
		errc <- run.Lines(ctx, func(line string) {
			switch strings.TrimSpace(line) {
			case "SAW-TCP":
				proto = "tcp"
				return
			case "SAW-UDP":
				proto = "udp"
				return
			case "SAW-END":
				open, now = now, map[string]bool{}
				return
			}
			c, ok := parseProcNet(line, proto)
			if !ok || c.uid != uid {
				return
			}
			key := proto + " " + c.local + " " + c.remote
			if now[key] {
				return
			}
			now[key] = true
			if open[key] { // still open since the last look
				return
			}
			kind := proto
			if proto == "udp" && c.port == 53 {
				kind = "dns" // the lookup itself isn't visible, only that one was made
			}
			send(event.Net{Time: time.Now(), Kind: kind, IP: c.ip, Port: c.port, Host: names.name(ctx, c.ip)})
		}, "adb", "-s", serial, "shell", script)
	}()
	err = <-errc // the first to stop ends both
	cancel()
	<-errc
	return err
}

var (
	urlHostRe = regexp.MustCompile(`(?i)\b(?:https?|wss?)://([a-z0-9.-]+\.[a-z]{2,})`)
	okhttpRe  = regexp.MustCompile(`^(-->|<--) `)
	// "Response Code 200", "status: 404", "HTTP 503"
	statusRe = regexp.MustCompile(`(?i)\b(?:response(?:\s*code)?|status(?:\s*code)?|http)\s*[:=]?\s*[1-5]\d\d\b`)
)

// httpLogLine is an app log line about an HTTP request: one with a URL or a status code in it,
// or OkHttp's request (-->) and response (<--) lines, but not their END markers.
func httpLogLine(msg string) bool {
	if okhttpRe.MatchString(msg) {
		return !strings.HasPrefix(msg[4:], "END")
	}
	return urlHostRe.MatchString(msg) || statusRe.MatchString(msg)
}

type procConn struct {
	local, remote string
	ip            string
	port, uid     int
}

// parseProcNet reads one /proc/net/{tcp,tcp6,udp,udp6} row, keeping connected sockets to
// non-loopback addresses:
//
//	0: 0000000000000000FFFF00000E00A8C0:B7E0 0000000000000000FFFF00000876BE23:01BB 01 … 10710 …
func parseProcNet(line, proto string) (procConn, bool) {
	f := strings.Fields(line)
	if len(f) < 8 || !strings.HasSuffix(f[0], ":") {
		return procConn{}, false
	}
	state := f[3]
	if proto == "tcp" && state != "01" && state != "02" { // established, SYN sent
		return procConn{}, false
	}
	addr, portHex, ok := strings.Cut(f[2], ":")
	if !ok {
		return procConn{}, false
	}
	port, err1 := strconv.ParseUint(portHex, 16, 16)
	uid, err2 := strconv.Atoi(f[7])
	ip := procIP(addr)
	if err1 != nil || err2 != nil || ip == nil || port == 0 || ip.IsLoopback() || ip.IsUnspecified() {
		return procConn{}, false
	}
	return procConn{local: f[1], remote: f[2], ip: ip.String(), port: int(port), uid: uid}, true
}

// procIP decodes /proc/net's addresses: 32-bit words, each in little-endian order.
func procIP(h string) net.IP {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	ip := net.IP(b)
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// lookupAddr and lookupHost are DNS lookups; tests replace them.
var (
	lookupAddr = net.DefaultResolver.LookupAddr
	lookupHost = net.DefaultResolver.LookupHost
)

// hostNames names connection addresses: first by hostnames the app mentioned in its log that
// resolve (here, on this computer) to the same address, then by reverse DNS. A name is only
// ever shown for an exact address match.
type hostNames struct {
	mu      sync.Mutex
	byIP    map[string]string // from the app's own hostnames
	ptr     map[string]string // from reverse DNS
	tried   map[string]bool
	pending sync.WaitGroup
}

func newHostNames() *hostNames {
	return &hostNames{byIP: map[string]string{}, ptr: map[string]string{}, tried: map[string]bool{}}
}

// learnFrom resolves, in the background, any new hostnames in an app's log message.
func (h *hostNames) learnFrom(ctx context.Context, msg string) {
	for _, m := range urlHostRe.FindAllStringSubmatch(msg, -1) {
		host := strings.ToLower(m[1])
		h.mu.Lock()
		seen := h.tried[host]
		h.tried[host] = true
		h.mu.Unlock()
		if seen {
			continue
		}
		h.pending.Add(1)
		go func() {
			defer h.pending.Done()
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			ips, err := lookupHost(ctx, host)
			if err != nil {
				return
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, ip := range ips {
				if p := net.ParseIP(ip); p != nil {
					h.byIP[p.String()] = host
				}
			}
		}()
	}
}

// wait gives in-flight lookups up to d to finish.
func (h *hostNames) wait(d time.Duration) {
	done := make(chan struct{})
	go func() { h.pending.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (h *hostNames) name(ctx context.Context, ip string) string {
	h.wait(300 * time.Millisecond) // a URL logged just before connecting may still be resolving
	h.mu.Lock()
	if name := h.byIP[ip]; name != "" {
		h.mu.Unlock()
		return name
	}
	name, done := h.ptr[ip]
	h.mu.Unlock()
	if done {
		return name
	}
	ctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	if names, err := lookupAddr(ctx, ip); err == nil && len(names) > 0 {
		name = strings.TrimSuffix(names[0], ".")
	}
	h.mu.Lock()
	h.ptr[ip] = name
	h.mu.Unlock()
	return name
}
