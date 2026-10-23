// Package matter talks to python-matter-server, the Matter controller the Home
// Assistant project maintains, over its WebSocket API. GWatch is not a Matter
// controller itself: commissioning, the fabric's certificates and the CASE
// sessions to every device all live in that sidecar, and GWatch only asks it
// questions — which nodes it knows, and what one attribute of one of them
// currently reads. docs/MATTER.md describes the protocol and the deployment.
//
// The package has two layers. websocket.go is a deliberately small RFC 6455
// client: it dials, performs the opening handshake, and exchanges text
// messages, which is all the sidecar ever sends. client.go is the Matter
// layer on top: the server_info greeting, numbered commands, and the shape of
// a success or error answer.
package matter

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The standard library has no WebSocket client and the module does not depend
// on golang.org/x/net, so rather than add a dependency for a protocol whose
// client side fits in a page, the handshake and the framing are written out
// here. Only what a JSON request/response conversation needs is implemented:
// text and binary data frames (fragmented or not), and the three control
// frames — close, ping and pong — that a well-behaved peer may send at any
// moment. Extensions (permessage-deflate) are never offered, so the server
// never uses them, and the reserved bits must therefore always be clear.

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	// websocketGUID is the fixed string RFC 6455 section 1.3 appends to the
	// client's key before hashing it, which is how the client knows the
	// server really speaks WebSocket rather than echoing headers back.
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// DefaultMaxMessage bounds one incoming message. A get_nodes answer from
	// a sidecar holding a large bridge is the biggest thing the API produces
	// — every cached attribute of every endpoint of every node — and a few
	// hundred kilobytes is typical, so 32 MiB leaves plenty of room while
	// still stopping a misbehaving peer from exhausting memory.
	DefaultMaxMessage = 32 << 20
)

// ErrClosed is returned once the peer has closed the connection.
var ErrClosed = errors.New("the WebSocket connection was closed")

// wsConn is one client connection. Reads happen on one goroutine at a time
// (the Matter client's), but a ping can arrive mid-read and must be answered,
// so writes are serialised with a mutex.
type wsConn struct {
	conn       net.Conn
	br         *bufio.Reader
	writeMu    sync.Mutex
	maxMessage int
	closed     bool
}

// dialWebSocket opens a connection to a ws:// or wss:// URL and completes the
// opening handshake. The context bounds the whole of it, dial included; once
// it returns, the caller sets deadlines per exchange.
func dialWebSocket(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Matter server URL %q: %w", rawURL, err)
	}
	var secure bool
	switch strings.ToLower(u.Scheme) {
	case "ws":
	case "wss":
		secure = true
	default:
		return nil, fmt.Errorf("the Matter server URL must start with ws:// or wss://, not %q", u.Scheme+"://")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("the Matter server URL %q has no host", rawURL)
	}
	addr := u.Host
	if u.Port() == "" {
		if secure {
			addr = net.JoinHostPort(u.Hostname(), "443")
		} else {
			addr = net.JoinHostPort(u.Hostname(), "80")
		}
	}

	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := raw
	if secure {
		tc := tls.Client(raw, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"User-Agent: GWatch\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("no WebSocket handshake answer: %w", err)
	}
	// The body of a 101 is the WebSocket stream itself, so it is not read or
	// closed here; anything else is an HTTP answer that explains itself.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("the server answered the WebSocket upgrade with HTTP %s — is this the Matter server's /ws address?", resp.Status)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		conn.Close()
		return nil, errors.New("the server did not switch to the WebSocket protocol")
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		conn.Close()
		return nil, errors.New("the server's WebSocket handshake did not match the request (Sec-WebSocket-Accept)")
	}
	return &wsConn{conn: conn, br: br, maxMessage: DefaultMaxMessage}, nil
}

// acceptKey is the value a genuine server returns for a client key.
func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// SetDeadline bounds the next reads and writes.
func (c *wsConn) SetDeadline(t time.Time) error { return c.conn.SetDeadline(t) }

// WriteText sends one text message as a single, masked frame. A client must
// mask every frame it sends (RFC 6455 section 5.3); a server that receives an
// unmasked one closes the connection.
func (c *wsConn) WriteText(p []byte) error {
	return c.writeFrame(opText, p)
}

func (c *wsConn) writeFrame(op byte, p []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return ErrClosed
	}
	hdr := make([]byte, 0, 14)
	hdr = append(hdr, 0x80|op) // FIN set: every frame this client sends is whole
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr = append(hdr, mask[:]...)
	buf := make([]byte, len(hdr)+len(p))
	copy(buf, hdr)
	for i, b := range p {
		buf[len(hdr)+i] = b ^ mask[i%4]
	}
	_, err := c.conn.Write(buf)
	return err
}

// ReadMessage returns the next whole data message, reassembling fragments and
// answering pings on the way. A close frame from the peer is answered and
// reported as ErrClosed.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	started := false
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			// Echo the status code back, as the closing handshake asks, and
			// stop: there is nothing more to read.
			echo := payload
			if len(echo) > 2 {
				echo = echo[:2]
			}
			_ = c.writeFrame(opClose, echo)
			c.writeMu.Lock()
			c.closed = true
			c.writeMu.Unlock()
			return nil, ErrClosed
		case opText, opBinary:
			if started {
				return nil, errors.New("WebSocket protocol error: a new message began before the last one finished")
			}
			started = true
		case opContinuation:
			if !started {
				return nil, errors.New("WebSocket protocol error: a continuation frame with no message to continue")
			}
		default:
			return nil, fmt.Errorf("WebSocket protocol error: unknown opcode %#x", op)
		}
		if len(msg)+len(payload) > c.maxMessage {
			return nil, fmt.Errorf("a message from the Matter server exceeded %d bytes", c.maxMessage)
		}
		msg = append(msg, payload...)
		if fin {
			return msg, nil
		}
	}
}

// readFrame reads one frame. Server frames are never masked; one that is
// would be a protocol error, but unmasking it costs nothing and keeps the
// reader tolerant, so it is accepted.
func (c *wsConn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		err = errors.New("WebSocket protocol error: reserved bits set without a negotiated extension")
		return
	}
	op = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if op >= opClose && (n > 125 || !fin) {
		err = errors.New("WebSocket protocol error: an oversized or fragmented control frame")
		return
	}
	if n > uint64(c.maxMessage) {
		err = fmt.Errorf("a message from the Matter server exceeded %d bytes", c.maxMessage)
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// Close sends a normal-closure frame, best effort, and closes the socket. It
// does not wait for the server's answer: a monitoring run that has what it
// came for has no reason to linger.
func (c *wsConn) Close() error {
	c.writeMu.Lock()
	already := c.closed
	c.closed = true
	c.writeMu.Unlock()
	if !already {
		_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
		c.writeMu.Lock()
		// 1000 is "normal closure".
		_ = writeRawClose(c.conn)
		c.writeMu.Unlock()
	}
	return c.conn.Close()
}

// writeRawClose writes a masked close frame carrying status 1000 without
// going through writeFrame, which refuses once the connection is marked
// closed.
func writeRawClose(w io.Writer) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	body := []byte{0x03, 0xE8}
	frame := []byte{0x80 | opClose, 0x80 | byte(len(body)), mask[0], mask[1], mask[2], mask[3]}
	for i, b := range body {
		frame = append(frame, b^mask[i%4])
	}
	_, err := w.Write(frame)
	return err
}
