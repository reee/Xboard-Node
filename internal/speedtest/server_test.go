package speedtest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var socketSeq int64

// testSocket returns a unique abstract socket name for the current process.
func testSocket() string {
	return fmt.Sprintf("@xboard-test-%d-%d", os.Getpid(), atomic.AddInt64(&socketSeq, 1))
}

func startTestServer(t *testing.T, cfg Config, socket string) *Server {
	t.Helper()
	s, err := Start(cfg, socket)
	if err != nil {
		t.Fatalf("Start(%q): %v", socket, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testClient talks HTTP/1.1 to the server over its Unix socket.
func testClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socket)
			},
		},
	}
}

func TestStartAbstractSocket(t *testing.T) {
	sock := testSocket()
	s := startTestServer(t, Config{}, sock)
	if s.Socket() != sock {
		t.Fatalf("Socket() = %q, want %q", s.Socket(), sock)
	}
	resp, err := testClient(sock).Get("http://unix/api/ping")
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping status = %d, want 200", resp.StatusCode)
	}
}

func TestIndex(t *testing.T) {
	s := startTestServer(t, Config{Title: "A&B"}, testSocket())
	resp, err := testClient(s.Socket()).Get("http://unix/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "A&amp;B") {
		t.Fatalf("body does not contain escaped title")
	}
	if strings.Contains(string(body), "{{TITLE}}") {
		t.Fatalf("body still contains {{TITLE}} placeholder")
	}
	if !strings.Contains(string(body), "ENP2S") {
		t.Fatalf("body does not contain brand ENP2S")
	}
	if !strings.Contains(string(body), "© 2026") {
		t.Fatalf("body does not contain copyright notice")
	}
}

func TestInfoEndpoint(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	resp, err := testClient(s.Socket()).Get("http://unix/api/info")
	if err != nil {
		t.Fatalf("GET /api/info: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q, want no-store", cc)
	}
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"ip", "ipVersion", "userAgent", "httpProto"} {
		if _, ok := v[key]; !ok {
			t.Errorf("missing field %q in %v", key, v)
		}
	}
	if got, want := v["httpProto"], resp.Proto; got != want {
		t.Errorf("httpProto = %v, want %q", got, want)
	}
	if ua, _ := v["userAgent"].(string); ua == "" {
		t.Errorf("userAgent = %q, want non-empty", ua)
	}
}

func TestSanitizeUA(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"Mozilla/5.0 (X11; Linux x86_64)", "Mozilla/5.0 (X11; Linux x86_64)"},
		{"a\x00b\x01c\x1fd\x7fe", "a b c d e"},
		{"Chrome\t\t141\n\nMacOS", "Chrome 141 MacOS"},
		{"  leading and trailing  ", "leading and trailing"},
		{strings.Repeat("a", 400), strings.Repeat("a", 300)},
	}
	for _, tc := range cases {
		if got := sanitizeUA(tc.in); got != tc.want {
			t.Errorf("sanitizeUA(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Truncation counts runes, not bytes.
	got := sanitizeUA(strings.Repeat("あ", 400))
	if len([]rune(got)) != 300 {
		t.Errorf("rune length = %d, want 300", len([]rune(got)))
	}
	if got != strings.Repeat("あ", 300) {
		t.Errorf("rune truncation = %q, want 300 intact runes", got)
	}
}

func TestClientIP(t *testing.T) {
	unixAddr := &net.UnixAddr{Name: "@/xboard-speedtest", Net: "unix"}
	cases := []struct {
		name     string
		addr     net.Addr
		wantHost string
		wantVer  int
	}{
		{"tcp4", &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 443}, "203.0.113.7", 4},
		{"tcp6", &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 80}, "2001:db8::1", 6},
		{"unix", unixAddr, "", 0},
		{"nil", nil, "", 0},
	}
	for _, tc := range cases {
		host, ver := clientIP(tc.addr)
		if host != tc.wantHost || ver != tc.wantVer {
			t.Errorf("clientIP(%s) = (%q, %d), want (%q, %d)", tc.name, host, ver, tc.wantHost, tc.wantVer)
		}
	}
}

func TestPing(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	resp, err := testClient(s.Socket()).Get("http://unix/api/ping")
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var v struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !v.OK {
		t.Fatalf("ok = false, want true")
	}
}

func TestDownload(t *testing.T) {
	s := startTestServer(t, Config{MaxTransferMB: 1}, testSocket())
	c := testClient(s.Socket())

	cases := []struct {
		query string
		want  int64
	}{
		{"", 1 << 20},               // default 1 MiB
		{"?bytes=1024", 1024},       // explicit size
		{"?bytes=4194304", 1 << 20}, // clamped to MaxTransferMB
	}
	for _, tc := range cases {
		resp, err := c.Get("http://unix/api/download" + tc.query)
		if err != nil {
			t.Fatalf("GET /api/download%s: %v", tc.query, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("status = %d, want 200 (%q)", resp.StatusCode, tc.query)
		}
		if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprint(tc.want) {
			t.Errorf("content-length = %s, want %d (%q)", cl, tc.want, tc.query)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("content-type = %q (%q)", ct, tc.query)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("cache-control = %q (%q)", cc, tc.query)
		}
		if ns := resp.Header.Get("X-Content-Type-Options"); ns != "nosniff" {
			t.Errorf("x-content-type-options = %q (%q)", ns, tc.query)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body (%q): %v", tc.query, err)
		}
		if int64(len(body)) != tc.want {
			t.Errorf("body size = %d, want %d (%q)", len(body), tc.want, tc.query)
		}
	}

	for _, q := range []string{"?bytes=0", "?bytes=-1", "?bytes=abc"} {
		resp, err := c.Get("http://unix/api/download" + q)
		if err != nil {
			t.Fatalf("GET /api/download%s: %v", q, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 (%q)", resp.StatusCode, q)
		}
	}
}

func TestUpload(t *testing.T) {
	s := startTestServer(t, Config{MaxTransferMB: 1}, testSocket())
	resp, err := testClient(s.Socket()).Post("http://unix/api/upload", "application/octet-stream", strings.NewReader(strings.Repeat("x", 1000)))
	if err != nil {
		t.Fatalf("POST /api/upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var v struct {
		Received int64 `json:"received"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.Received != 1000 {
		t.Fatalf("received = %d, want 1000", v.Received)
	}
}

// TestUploadTooLarge drives a raw connection so the client keeps sending past
// the limit while the server responds 413 mid-body.
func TestUploadTooLarge(t *testing.T) {
	s := startTestServer(t, Config{MaxTransferMB: 1}, testSocket())
	conn, err := net.Dial("unix", s.Socket())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	body := strings.Repeat("y", (1<<20)+1024)
	go io.Copy(conn, io.MultiReader(
		strings.NewReader(fmt.Sprintf("POST /api/upload HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(body))),
		strings.NewReader(body),
	))

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestMethodMismatchAndUnknownPath(t *testing.T) {
	s := startTestServer(t, Config{}, testSocket())
	c := testClient(s.Socket())

	cases := []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/ping", http.StatusMethodNotAllowed},
		{"GET", "/api/upload", http.StatusMethodNotAllowed},
		{"POST", "/api/download", http.StatusMethodNotAllowed},
		{"GET", "/api/nope", http.StatusNotFound},
		{"GET", "/nope", http.StatusNotFound},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, "http://unix"+tc.path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}

// TestConcurrentLimit verifies the shared download/upload semaphore: with
// MaxConcurrent=1 the second in-flight transfer gets 503.
func TestConcurrentLimit(t *testing.T) {
	s := startTestServer(t, Config{MaxConcurrent: 1}, testSocket())

	// Occupy the only slot with a large download the client never reads, so
	// the handler stays blocked in Write.
	conn, err := net.Dial("unix", s.Socket())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	go conn.Write([]byte("GET /api/download?bytes=67108864 HTTP/1.1\r\nHost: x\r\n\r\n"))
	time.Sleep(200 * time.Millisecond)

	resp, err := testClient(s.Socket()).Get("http://unix/api/download?bytes=1024")
	if err != nil {
		t.Fatalf("GET /api/download: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second download status = %d, want 503", resp.StatusCode)
	}

	up, err := testClient(s.Socket()).Post("http://unix/api/upload", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("POST /api/upload: %v", err)
	}
	up.Body.Close()
	if up.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upload status = %d, want 503", up.StatusCode)
	}
}
