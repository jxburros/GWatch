// Package mattertest is a fake python-matter-server for tests. It speaks the
// server side of the WebSocket protocol over an httptest server and answers
// the handful of commands GWatch sends from an in-memory fabric, so the
// client, the check runner and the API can each be tested end to end without
// a Matter stack, a device or a network.
//
// It is written from the server's source (matter_server/server/
// client_handler.py and device_controller.py): a server_info greeting on
// connect, {message_id, result} for a success, {message_id, error_code,
// details} for a failure, NodeNotReady (3) for a read from a node that is not
// available and NodeNotExists (5) for one that was never commissioned.
package mattertest

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Node is one device on the fake fabric. Attributes holds JSON values keyed
// "endpoint/cluster/attribute", exactly as the real server caches them.
type Node struct {
	ID         uint64
	Available  bool
	IsBridge   bool
	Attributes map[string]any
}

// Server is the fake. Its fields may be changed between runs under Lock.
type Server struct {
	*httptest.Server

	mu    sync.Mutex
	nodes map[uint64]*Node

	// Commands records every command received, in order.
	Commands []string
	// LastArgs is the args object of the most recent command.
	LastArgs map[string]any

	// NoGreeting makes the server stay silent on connect, the way an
	// unrelated WebSocket service would.
	NoGreeting bool
	// ListReads refuses a list of attribute paths with InvalidArguments, the
	// way a server from before list reads did.
	SingleReadsOnly bool
	// Chatty sends an unrelated event, a ping and a fragmented answer, to
	// exercise the client's tolerance.
	Chatty bool
	// Bluetooth is what server_info reports; commissioning without
	// network_only needs it.
	Bluetooth bool
	// NextNodeID is the id the next commissioned device receives.
	NextNodeID uint64
}

// New starts a fake server holding the given nodes.
func New(nodes ...Node) *Server {
	s := &Server{nodes: map[uint64]*Node{}, NextNodeID: 100}
	for i := range nodes {
		n := nodes[i]
		s.nodes[n.ID] = &n
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// WSURL is the ws:// address of the fake.
func (s *Server) WSURL() string {
	return "ws" + strings.TrimPrefix(s.Server.URL, "http") + "/ws"
}

// SetAvailable marks a node reachable or not.
func (s *Server) SetAvailable(id uint64, available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.nodes[id]; n != nil {
		n.Available = available
	}
}

// SetAttribute changes one attribute value.
func (s *Server) SetAttribute(id uint64, path string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.nodes[id]; n != nil {
		n.Attributes[path] = v
	}
}

// CommandLog returns a copy of the commands received so far.
func (s *Server) CommandLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Commands...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "not a websocket", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+
		base64.StdEncoding.EncodeToString(h[:])+"\r\n\r\n")

	if !s.NoGreeting {
		s.send(conn, map[string]any{
			"fabric_id": 1, "compressed_fabric_id": 1234567890, "schema_version": 11,
			"min_supported_schema_version": 9, "sdk_version": "fake-2024.1",
			"wifi_credentials_set": false, "thread_credentials_set": false, "bluetooth_enabled": s.Bluetooth,
		})
	}
	for {
		op, payload, err := readFrame(brw.Reader)
		if err != nil {
			return
		}
		switch op {
		case 0x8:
			writeFrame(conn, 0x8, true, payload)
			return
		case 0x9:
			writeFrame(conn, 0xA, true, payload)
			continue
		case 0xA:
			continue
		}
		var msg struct {
			MessageID string         `json:"message_id"`
			Command   string         `json:"command"`
			Args      map[string]any `json:"args"`
		}
		if json.Unmarshal(payload, &msg) != nil {
			return
		}
		s.mu.Lock()
		s.Commands = append(s.Commands, msg.Command)
		s.LastArgs = msg.Args
		s.mu.Unlock()
		if s.Chatty {
			s.send(conn, map[string]any{"event": "attribute_updated", "data": []any{1, "1/6/0", true}})
			writeFrame(conn, 0x9, true, []byte("are you there"))
		}
		result, code, details := s.handle(msg.Command, msg.Args)
		var answer map[string]any
		if code >= 0 {
			answer = map[string]any{"message_id": msg.MessageID, "error_code": code, "details": details}
		} else {
			answer = map[string]any{"message_id": msg.MessageID, "result": result}
		}
		if s.Chatty {
			b, _ := json.Marshal(answer)
			half := len(b) / 2
			writeFrame(conn, 0x1, false, b[:half])
			writeFrame(conn, 0x0, true, b[half:])
			continue
		}
		s.send(conn, answer)
	}
}

// handle answers one command. code is -1 for success.
func (s *Server) handle(command string, args map[string]any) (any, int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch command {
	case "get_nodes":
		out := []any{}
		for _, n := range s.sortedNodes() {
			out = append(out, nodeJSON(n))
		}
		return out, -1, ""
	case "get_node":
		id, _ := args["node_id"].(float64)
		n := s.nodes[uint64(id)]
		if n == nil {
			return nil, 5, "Node does not exist or is not yet interviewed"
		}
		return nodeJSON(n), -1, ""
	case "read_attribute":
		id, _ := args["node_id"].(float64)
		n := s.nodes[uint64(id)]
		if n == nil || !n.Available {
			return nil, 3, "Node is not (yet) available."
		}
		var paths []string
		switch p := args["attribute_path"].(type) {
		case string:
			paths = []string{p}
		case []any:
			if s.SingleReadsOnly {
				return nil, 8, "attribute_path: expected str"
			}
			for _, x := range p {
				if str, ok := x.(string); ok {
					paths = append(paths, str)
				}
			}
		default:
			return nil, 8, "attribute_path is required"
		}
		out := map[string]any{}
		for _, p := range paths {
			if v, ok := n.Attributes[p]; ok {
				out[p] = v
			}
		}
		return out, -1, ""
	case "commission_with_code":
		code, _ := args["code"].(string)
		networkOnly, _ := args["network_only"].(bool)
		if !networkOnly && !s.Bluetooth {
			return nil, 1, "Bluetooth commissioning is not available."
		}
		if code == "" || code == "00000000000" {
			return nil, 1, "Commission with code failed for node 100."
		}
		n := &Node{ID: s.NextNodeID, Available: true, Attributes: map[string]any{
			"0/40/1": "Acme", "0/40/3": "Smart Plug", "1/6/0": false,
		}}
		s.nodes[n.ID] = n
		s.NextNodeID++
		return nodeJSON(n), -1, ""
	}
	return nil, 9, "Invalid command: " + command
}

func (s *Server) sortedNodes() []*Node {
	out := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func nodeJSON(n *Node) map[string]any {
	attrs := map[string]any{}
	for k, v := range n.Attributes {
		attrs[k] = v
	}
	return map[string]any{
		"node_id": n.ID, "date_commissioned": "2024-01-01T00:00:00", "last_interview": "2024-01-01T00:00:00",
		"interview_version": 5, "available": n.Available, "is_bridge": n.IsBridge, "attributes": attrs,
	}
}

func (s *Server) send(conn net.Conn, v any) {
	b, _ := json.Marshal(v)
	writeFrame(conn, 0x1, true, b)
}

// writeFrame writes one unmasked server frame.
func writeFrame(w io.Writer, op byte, fin bool, p []byte) {
	first := op
	if fin {
		first |= 0x80
	}
	hdr := []byte{first}
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	_, _ = w.Write(append(hdr, p...))
}

// readFrame reads one client frame, which must be masked.
func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	op := h[0] & 0x0F
	if h[1]&0x80 == 0 {
		return 0, nil, errors.New("client frame not masked")
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return op, p, nil
}
