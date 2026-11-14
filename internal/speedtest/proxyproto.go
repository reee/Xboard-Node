package speedtest

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"time"
)

// PROXY protocol v1 headers are at most 107 chars plus CRLF.
const proxyHeaderMax = 108

// proxyDeadline bounds the wait for a PROXY header on a new connection.
const proxyDeadline = 3 * time.Second

// proxyListener wraps a listener and strips PROXY protocol v1 headers during
// Accept. It must run in Accept (not lazily on first Read) because net/http
// snapshots conn.RemoteAddr before reading anything from the connection.
type proxyListener struct {
	net.Listener
}

func wrapProxyProto(l net.Listener) net.Listener {
	return &proxyListener{Listener: l}
}

func (l *proxyListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(proxyDeadline))
	line := peekLine(br, proxyHeaderMax)
	_ = c.SetReadDeadline(time.Time{}) // hand the conn back to net/http

	pc := &proxyConn{Conn: c, br: br}
	if ip, port, ok := parseProxyHeader(line); ok {
		// Strip the header; the peeked-but-unparsed bytes otherwise reach
		// net/http as a malformed request line.
		_, _ = br.Discard(len(line))
		pc.remoteAddr = &net.TCPAddr{IP: ip, Port: port}
	}
	return pc, nil
}

// peekLine returns the bytes up to and including the first '\n' (at most max
// bytes) without consuming them, so non-PROXY traffic passes through intact.
func peekLine(br *bufio.Reader, max int) []byte {
	for i := 1; i <= max; i++ {
		b, err := br.Peek(i)
		if len(b) > 0 && b[len(b)-1] == '\n' {
			return b
		}
		if err != nil {
			return b
		}
	}
	return nil
}

// parseProxyHeader parses "PROXY TCP4|TCP6 src dst sport dport\r\n".
// Absent headers, PROXY UNKNOWN and malformed lines all report ok=false.
func parseProxyHeader(line []byte) (net.IP, int, bool) {
	fields := strings.Fields(string(line))
	if len(fields) != 6 || fields[0] != "PROXY" {
		return nil, 0, false
	}
	if fields[1] != "TCP4" && fields[1] != "TCP6" {
		return nil, 0, false
	}
	ip := net.ParseIP(fields[2])
	if ip == nil {
		return nil, 0, false
	}
	port, err := strconv.Atoi(fields[4])
	if err != nil || port < 0 || port > 65535 {
		return nil, 0, false
	}
	return ip, port, true
}

// proxyConn reports the real client address learned from the PROXY header and
// serves reads from the bufio.Reader used during parsing so buffered bytes are
// not lost. Writes go straight through.
type proxyConn struct {
	net.Conn
	br         *bufio.Reader
	remoteAddr net.Addr
}

func (c *proxyConn) Read(p []byte) (int, error) { return c.br.Read(p) }

func (c *proxyConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return c.Conn.RemoteAddr()
}
