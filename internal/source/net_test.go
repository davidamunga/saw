package source

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/runtest"
)

// clientHello is a real TLS ClientHello for host, from Go's TLS client.
func clientHello(t *testing.T, host string) []byte {
	t.Helper()
	c, s := net.Pipe()
	go tls.Client(c, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}).Handshake()
	buf := make([]byte, 8192)
	s.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := s.Read(buf)
	c.Close()
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

// packet wraps a TCP payload to dst:port in IPv4 or IPv6, inside Ethernet when eth is set.
func packet(payload []byte, dst net.IP, port int, eth bool) []byte {
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:], 51234)
	binary.BigEndian.PutUint16(tcp[2:], uint16(port))
	tcp[12] = 5 << 4
	seg := append(tcp, payload...)
	var ip []byte
	etherType := []byte{0x08, 0x00}
	if v4 := dst.To4(); v4 != nil {
		ip = make([]byte, 20)
		ip[0], ip[9] = 0x45, 6
		copy(ip[12:], net.IPv4(192, 168, 1, 101).To4())
		copy(ip[16:], v4)
	} else {
		ip = make([]byte, 40)
		ip[0], ip[6] = 0x60, 6
		copy(ip[24:], dst.To16())
		etherType = []byte{0x86, 0xDD}
	}
	pkt := append(ip, seg...)
	if eth {
		frame := append(make([]byte, 12), etherType...)
		pkt = append(frame, pkt...)
	}
	return pkt
}

// hexdump prints a packet as `pymobiledevice3 pcap` does.
func hexdump(when string, b []byte) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s: Process Example (3126), Interface: en (ethernetCsmacd), Family: AF_INET\n", when)
	for i := 0; i < len(b); i += 16 {
		row := b[i:min(i+16, len(b))]
		var hexs strings.Builder
		for j, c := range row {
			if j == 8 {
				hexs.WriteString(" ")
			}
			fmt.Fprintf(&hexs, "%02X ", c)
		}
		fmt.Fprintf(&s, "%08X: %-49s %s\n", i, hexs.String(), strings.Repeat(".", len(row)))
	}
	return s.String() + "\n"
}

func feed(p *pcapParser, text string) []event.Net {
	var got []event.Net
	for _, l := range strings.Split(text, "\n") {
		if n, ok := p.line(l); ok {
			got = append(got, n)
		}
	}
	return got
}

func TestPcapParser(t *testing.T) {
	hello := clientHello(t, "api.example.com")
	httpReq := []byte("GET /v1/orders?page=2 HTTP/1.1\r\nHost: plain.example.com\r\nAccept: */*\r\n\r\n")
	dump := hexdump("2026-11-09 15:42:12.801991", packet(hello, net.IPv4(203, 0, 113, 7), 443, true)) +
		hexdump("2026-11-09 15:42:12.900000", packet(hello, net.ParseIP("2001:db8::7"), 443, false)) +
		hexdump("2026-11-09 15:42:13.000000", packet(httpReq, net.IPv4(203, 0, 113, 8), 80, true)) +
		hexdump("2026-11-09 15:42:13.100000", packet(httpReq, net.IPv4(203, 0, 113, 8), 8080, true)) +
		hexdump("2026-11-09 15:42:13.200000", packet([]byte("not interesting"), net.IPv4(203, 0, 113, 9), 443, true))
	got := feed(&pcapParser{}, dump)
	if len(got) != 4 {
		t.Fatalf("want 4 events, got %+v", got)
	}
	if n := got[0]; n.Kind != "tls" || n.Host != "api.example.com" || n.IP != "203.0.113.7" || n.Port != 443 || n.Time.Format("15:04:05.000") != "15:42:12.801" {
		t.Errorf("Wi-Fi TLS: %+v", n)
	}
	if n := got[1]; n.Kind != "tls" || n.IP != "2001:db8::7" {
		t.Errorf("cellular IPv6 TLS: %+v", n)
	}
	if n := got[2]; n.Kind != "http" || n.Method != "GET" || n.URL != "http://plain.example.com/v1/orders?page=2" {
		t.Errorf("plain HTTP: %+v", n)
	}
	if n := got[3]; n.URL != "http://plain.example.com:8080/v1/orders?page=2" {
		t.Errorf("plain HTTP on another port: %+v", n)
	}
}

func TestPacketJunk(t *testing.T) {
	hello := clientHello(t, "api.example.com")
	for name, b := range map[string][]byte{
		"empty":      nil,
		"short ipv4": {0x45, 0, 0},
		"udp":        append(packet(hello, net.IPv4(1, 2, 3, 4), 443, false)[:9], 17),
		"truncated":  packet(hello[:60], net.IPv4(1, 2, 3, 4), 443, false),
	} {
		if n, ok := parsePacket(b); ok {
			t.Errorf("%s: want nothing, got %+v", name, n)
		}
	}
	for _, d := range [][]byte{hello[:44], hello[:50], {0x16, 3, 1, 0, 0, 2}} {
		if tlsServerName(d) != "" {
			t.Error("truncated hello should give no name")
		}
	}
	for _, r := range []string{"GET / HTTP/1.1\r\n\r\n", "BREW /pot HTTP/1.1\r\nHost: x\r\n\r\n", "hello"} {
		if _, _, _, ok := httpRequest([]byte(r)); ok {
			t.Errorf("%q should not parse", r)
		}
	}
}

func TestCFNetwork(t *testing.T) {
	cf := newCFNetwork()
	at := time.Date(2026, 11, 9, 15, 44, 24, 0, time.UTC)
	ev := func(sub, msg string) (event.Net, bool) {
		return cf.event(event.Event{Time: at, Subsystem: sub, Message: msg})
	}
	if _, ok := ev("com.apple.network", "[C1 EB11 Hostname#30b4cfe5:443 quic-connection, url: https://example.com/a, definite] start"); ok {
		t.Error("a connection start is not a request")
	}
	n, ok := ev("com.apple.CFNetwork", `Task <5B59>.<1> summary for task success {transaction_duration_ms=941, response_status=200, connection=1, protocol="h2", response_bytes=520, cache_hit=false}`)
	if !ok || n.URL != "https://example.com/a" || n.Host != "example.com" || n.Status != 200 || n.DurationMs != 941 || n.Protocol != "h2" || n.Bytes != 520 || n.Kind != "request" {
		t.Errorf("got %+v", n)
	}
	n, _ = ev("com.apple.CFNetwork", "Task <5B59>.<2> summary for task success {transaction_duration_ms=80, response_status=404, connection=1, reused=1}")
	if n.URL != "" || n.Host != "example.com" || n.Status != 404 {
		t.Errorf("a reused connection's URL belongs to its first request: %+v", n)
	}
	n, _ = ev("com.apple.CFNetwork", "Task <A>.<3> summary for task failure {transaction_duration_ms=60000, connection=2}")
	if !n.Failed || n.Host != "" || n.Conn != 2 {
		t.Errorf("failure: %+v", n)
	}
	n, _ = ev("com.apple.CFNetwork", "Task <B>.<4> summary for task success {transaction_duration_ms=4, response_status=200, cache_hit=true}")
	if !n.Cached {
		t.Errorf("cache hit: %+v", n)
	}
	for _, m := range []string{
		"Task <C>.<5> summary for task success {transaction_duration_ms=841, response_status=-1, cache_hit=false}",
		"Task <C>.<5> received response, status 200 content U",
	} {
		if n, ok := ev("com.apple.CFNetwork", m); ok {
			t.Errorf("%q should be skipped, got %+v", m, n)
		}
	}
	if _, ok := ev("com.apple.UIKit", "summary for task success {}"); ok {
		t.Error("other subsystems are ignored")
	}
}

func TestProcNet(t *testing.T) {
	for _, c := range []struct {
		line, proto, ip string
		port, uid       int
		ok              bool
	}{
		{"   1: 0000000000000000FFFF00000E00A8C0:B7E0 0000000000000000FFFF00000876BE23:01BB 01 0000004E:00000000 00:00000000 00000003 10710 0 0 1", "tcp", "35.190.118.8", 443, 10710, true},
		{"   0: 0E00A8C0:D906 0E2CE8C5:01BB 01 00000001:00000000 00:00000000 00000000 10710 0 0", "tcp", "197.232.44.14", 443, 10710, true},
		{"   2: 00000000000000000000000000000000:0000 B80D0120000000000000000007000000:01BB 01 0 0 0 10710 0 0", "udp", "2001:db8::7", 443, 10710, true},
		{"   3: 0E00A8C0:D906 0E2CE8C5:01BB 0A 0 0 0 10710 0 0", "tcp", "", 0, 0, false}, // listening
		{"   4: 0E00A8C0:D906 0100007F:1F90 01 0 0 0 10710 0 0", "tcp", "", 0, 0, false}, // loopback
		{"   5: 0E00A8C0:D906 00000000:0000 07 0 0 0 10710 0 0", "udp", "", 0, 0, false}, // not connected
		{"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid", "tcp", "", 0, 0, false},
		{"   6: 0E00A8C0:D906 ZZZZ:01BB 01 0 0 0 10710 0 0", "tcp", "", 0, 0, false},
	} {
		got, ok := parseProcNet(c.line, c.proto)
		if ok != c.ok || (ok && (got.ip != c.ip || got.port != c.port || got.uid != c.uid)) {
			t.Errorf("%q: got %+v %v", c.line, got, ok)
		}
	}
}

func fakeDNS(t *testing.T, names map[string]string) {
	old := lookupAddr
	lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
		if n, ok := names[ip]; ok {
			return []string{n}, nil
		}
		return nil, errors.New("no name")
	}
	t.Cleanup(func() { lookupAddr = old })
}

func collectNet(t *testing.T, s Spec) ([]event.Net, error) {
	t.Helper()
	var got []event.Net
	err := StreamNet(context.Background(), s, func(n event.Net) { got = append(got, n) })
	return got, err
}

func TestNetAndroid(t *testing.T) {
	fakeDNS(t, map[string]string{"35.190.118.8": "8.118.190.35.bc.googleusercontent.com."})
	open := "   1: 0E00A8C0:B7E0 0876BE23:01BB 01 0 0 0 10710 0 0\n"
	snapshot := func(tcp, udp string) string {
		return "SAW-TCP\n  sl local_address rem_address st\n" + tcp + "SAW-UDP\n" + udp + "SAW-END\n"
	}
	runtest.Install(t,
		runtest.Rule{Cmd: "adb -s S1 shell pm list packages -U com.example.app", Stdout: "package:com.example.app uid:10710\n"},
		runtest.Rule{Cmd: "adb -s S1 shell while", Stdout: snapshot(open+"   2: 0E00A8C0:B7E1 0876BE24:01BB 01 0 0 0 10999 0 0\n", "") +
			snapshot(open+open+"   3: 0E00A8C0:B7E2 090000CB:0050 02 0 0 0 10710 0 0\n", "   4: 0E00A8C0:C000 0100800A:0035 01 0 0 0 10710 0 0\n")},
	)
	got, err := collectNet(t, Spec{Device: pixel, App: app})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, n := range got {
		lines = append(lines, fmt.Sprintf("%s %s:%d %s", n.Kind, n.IP, n.Port, n.Host))
	}
	// The connection open across both looks is reported once; other apps' sockets never.
	want := "tcp 35.190.118.8:443 8.118.190.35.bc.googleusercontent.com|tcp 203.0.0.9:80 |dns 10.128.0.1:53 "
	if strings.Join(lines, "|") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(lines, "|"), want)
	}
	if _, err := collectNet(t, Spec{Device: pixel, App: config.App{Name: "x", IOS: "com.x"}}); err == nil {
		t.Error("want an error without an android package")
	}
}

func TestNetSimulator(t *testing.T) {
	logs := `{"eventType":"logEvent","messageType":"Default","subsystem":"com.apple.network","category":"connection","processImagePath":"/x/Example.app/Example","timestamp":"2026-11-09 15:44:23.199000+0300","eventMessage":"[C1 EB11 Hostname#30b4cfe5:443 quic-connection, url: https://example.com/, definite] start"}
{"eventType":"logEvent","messageType":"Default","subsystem":"com.apple.CFNetwork","category":"Summary","processImagePath":"/x/Example.app/Example","timestamp":"2026-11-09 15:44:24.150000+0300","eventMessage":"Task <5B59>.<1> summary for task success {transaction_duration_ms=941, response_status=200, connection=1, protocol=\"h2\", response_bytes=520}"}
`
	runtest.Install(t,
		runtest.Rule{Cmd: "xcrun simctl appinfo SIM-1 com.example.app", Stdout: "{ CFBundleExecutable = Example; }"},
		runtest.Rule{Cmd: "xcrun simctl spawn SIM-1 log stream", Stdout: "Filtering the log data\n" + logs},
	)
	got, err := collectNet(t, Spec{Device: sim, App: app})
	if err != nil || len(got) != 1 || got[0].URL != "https://example.com/" || got[0].Device != "iPhone 17 Pro" || got[0].Platform != "ios" {
		t.Errorf("got %+v %v", got, err)
	}
	runtest.Install(t, runtest.Rule{Cmd: "xcrun simctl appinfo", Exit: 1})
	if _, err := collectNet(t, Spec{Device: sim, App: app}); err == nil {
		t.Error("want not installed")
	}
}

func TestNetIOSDevice(t *testing.T) {
	summary := `{"pid": 1, "timestamp": "2026-11-09T15:50:55.052000", "level": "DEFAULT", "image_name": "/x", "filename": "/x/Example.app/Example", "message": "Task <A>.<1> summary for task success {transaction_duration_ms=605, response_status=201, connection=1, protocol=\"h2\", response_bytes=3400}", "label": {"subsystem": "com.apple.CFNetwork", "category": "Summary"}}` + "\n"
	runtest.Install(t,
		runtest.Rule{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`},
		runtest.Rule{Cmd: "pymobiledevice3 syslog live", Stdout: summary, Hang: true},
		runtest.Rule{Cmd: "pymobiledevice3 pcap", Stdout: hexdump("2026-11-09 15:50:54.474000", packet(clientHello(t, "api.example.com"), net.IPv4(203, 0, 113, 7), 443, true))},
	)
	// pcap ends first here, which ends the whole stream.
	got, err := collectNet(t, Spec{Device: iphone, App: app})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, n := range got {
		kinds[n.Kind] = true
		if n.Device != "iPhone" || n.Platform != "ios" {
			t.Errorf("got %+v", n)
		}
	}
	if !kinds["tls"] {
		t.Errorf("want the TLS connection, got %+v", got)
	}

	runtest.Install(t, runtest.Rule{Cmd: "pymobiledevice3 apps query", Stdout: "{}"})
	if _, err := collectNet(t, Spec{Device: iphone, App: app}); err == nil {
		t.Error("want not installed")
	}
	if _, err := collectNet(t, Spec{Device: iphone, App: config.App{Name: "x", Android: "com.x"}}); err == nil {
		t.Error("want no bundle id")
	}
	if _, err := collectNet(t, Spec{Device: device.Device{Platform: "web"}}); err == nil {
		t.Error("want unsupported device")
	}
}

func TestReverseDNS(t *testing.T) {
	calls := 0
	old := lookupAddr
	lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
		calls++
		return []string{"host.example."}, nil
	}
	t.Cleanup(func() { lookupAddr = old })
	r := newReverseDNS()
	if r.lookup(context.Background(), "1.2.3.4") != "host.example" || r.lookup(context.Background(), "1.2.3.4") != "host.example" || calls != 1 {
		t.Errorf("want one cached lookup, got %d calls", calls)
	}
}
