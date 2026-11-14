package speedtest

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// requestViaConn writes a raw HTTP/1.1 request (optionally preceded by a
// prefix such as a PROXY header) and returns the parsed response body JSON.
func requestViaConn(t *testing.T, socket, prefix, req string) (map[string]any, int) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(prefix + req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v, resp.StatusCode
}

const ipReq = "GET /api/ip HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"

func TestProxyProtoTCP4(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	v, status := requestViaConn(t, s.Socket(),
		"PROXY TCP4 203.0.113.9 127.0.0.1 4567 80\r\n", ipReq)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if v["ip"] != "203.0.113.9" {
		t.Fatalf("ip = %v, want 203.0.113.9", v["ip"])
	}
}

func TestProxyProtoTCP6(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	v, status := requestViaConn(t, s.Socket(),
		"PROXY TCP6 2001:db8::1 2001:db8::2 4567 80\r\n", ipReq)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if v["ip"] != "2001:db8::1" {
		t.Fatalf("ip = %v, want 2001:db8::1", v["ip"])
	}
}

func TestProxyProtoNone(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	v, status := requestViaConn(t, s.Socket(), "", ipReq)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if v["ip"] == "" || v["ip"] == nil {
		t.Fatalf("ip = %v, want underlying socket address", v["ip"])
	}
}

func TestPeekLineDoesNotConsume(t *testing.T) {
	data := "PROXY TCP4 1.2.3.4 5.6.7.8 1 2\r\nGET / HTTP/1.1\r\n"
	br := bufio.NewReader(strings.NewReader(data))
	line := peekLine(br, proxyHeaderMax)
	if string(line) != "PROXY TCP4 1.2.3.4 5.6.7.8 1 2\r\n" {
		t.Fatalf("peekLine returned %q", line)
	}
	rest := make([]byte, len(data))
	if _, err := io.ReadFull(br, rest); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(rest) != data {
		t.Fatalf("data was consumed by peekLine: %q", rest)
	}
}

// Only TCP4/TCP6 headers parse; PROXY UNKNOWN and malformed lines are kept
// verbatim and fall back to the underlying RemoteAddr.
func TestParseProxyHeader(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		ip   string
		port int
	}{
		{"PROXY TCP4 203.0.113.9 127.0.0.1 4567 80\r\n", true, "203.0.113.9", 4567},
		{"PROXY TCP6 2001:db8::1 ::1 4567 80\r\n", true, "2001:db8::1", 4567},
		{"PROXY UNKNOWN\r\n", false, "", 0},
		{"GET / HTTP/1.1\r\n", false, "", 0},
		{"PROXY TCP4 not-an-ip 127.0.0.1 4567 80\r\n", false, "", 0},
		{"PROXY TCP4 1.2.3.4 127.0.0.1 notaport 80\r\n", false, "", 0},
		{"", false, "", 0},
	}
	for _, tc := range cases {
		ip, port, ok := parseProxyHeader([]byte(tc.line))
		if ok != tc.ok {
			t.Errorf("parseProxyHeader(%q): ok = %v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if ok && (ip.String() != tc.ip || port != tc.port) {
			t.Errorf("parseProxyHeader(%q): got %v:%d, want %s:%d", tc.line, ip, port, tc.ip, tc.port)
		}
	}
}
