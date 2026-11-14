// Package speedtest serves an HTTP speed test over a Unix domain socket so
// that xray can expose it via a VLESS fallback (xver:1, PROXY protocol v1).
package speedtest

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

//go:embed index.html
var indexHTML string

const (
	DefaultTitle         = "Speed Test"
	DefaultMaxTransferMB = 256
	DefaultMaxConcurrent = 32

	downloadChunk = 32 << 10 // pre-generated random block size
	defaultBytes  = 1 << 20  // /api/download default transfer size
)

// Config controls the speed test server. Zero or negative values fall back to
// the defaults.
type Config struct {
	Title         string
	MaxTransferMB int
	MaxConcurrent int
}

// Server is a running speed test listener.
type Server struct {
	cfg    Config
	socket string
	ln     net.Listener
	http   *http.Server
	sem    chan struct{} // download/upload concurrency limiter
	block  []byte        // pre-generated random data for downloads
}

// Start listens on socket (which must be an abstract Unix domain socket name
// starting with '@') and serves the speed test endpoints in the background.
func Start(cfg Config, socket string) (*Server, error) {
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}
	if cfg.MaxTransferMB <= 0 {
		cfg.MaxTransferMB = DefaultMaxTransferMB
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}

	block := make([]byte, downloadChunk)
	if _, err := rand.Read(block); err != nil {
		ln.Close()
		return nil, err
	}

	s := &Server{
		cfg:    cfg,
		socket: socket,
		ln:     ln,
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		block:  block,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/ping", s.handlePing)
	mux.HandleFunc("GET /api/ip", s.handleIP)
	mux.HandleFunc("GET /api/download", s.handleDownload)
	mux.HandleFunc("POST /api/upload", s.handleUpload)

	s.http = &http.Server{
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	// PROXY protocol v1 headers must be parsed in Accept: net/http caches
	// RemoteAddr before its first Read, so lazy parsing would lose the real IP.
	go s.http.Serve(wrapProxyProto(ln))
	return s, nil
}

// Socket returns the address the server is listening on.
func (s *Server) Socket() string { return s.socket }

// Config returns the effective configuration of the running server.
func (s *Server) Config() Config { return s.cfg }

// Close stops the server and releases the socket. The raw listener is closed
// directly too: http.Server.Close only closes listeners Serve has registered,
// and Serve starts in a goroutine that may not have run yet, which would leak
// the socket name and break an immediate re-bind on the same name.
func (s *Server) Close() error {
	err := s.http.Close()
	if lerr := s.ln.Close(); lerr != nil && !errors.Is(lerr, net.ErrClosed) {
		err = lerr
	}
	return err
}

func (s *Server) maxBytes() int64 { return int64(s.cfg.MaxTransferMB) << 20 }

// acquire takes one download/upload slot without blocking; false means full.
func (s *Server) acquire() bool {
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) release() { <-s.sem }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, strings.ReplaceAll(indexHTML, "{{TITLE}}", html.EscapeString(s.cfg.Title)))
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleIP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": host})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	n := int64(defaultBytes)
	if v := r.URL.Query().Get("bytes"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid bytes", http.StatusBadRequest)
			return
		}
		n = parsed
	}
	if n <= 0 {
		http.Error(w, "bytes must be positive", http.StatusBadRequest)
		return
	}
	if max := s.maxBytes(); n > max {
		n = max
	}
	if !s.acquire() {
		http.Error(w, "too many concurrent transfers", http.StatusServiceUnavailable)
		return
	}
	defer s.release()

	w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	for written := int64(0); written < n; {
		chunk := int64(len(s.block))
		if n-written < chunk {
			chunk = n - written
		}
		if _, err := w.Write(s.block[:chunk]); err != nil {
			return // client gone
		}
		written += chunk
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.acquire() {
		http.Error(w, "too many concurrent transfers", http.StatusServiceUnavailable)
		return
	}
	defer s.release()

	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes())
	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		return // client gone
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": n})
}
