package matter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultPort is where python-matter-server listens unless it was started
// with --port, and DefaultPath is the WebSocket endpoint on it.
const (
	DefaultPort = 5580
	DefaultPath = "/ws"
)

// Error codes the server puts in an error answer. They come from
// matter_server/common/errors.py and are stable across releases; only the
// ones GWatch treats differently are named here.
const (
	ErrCodeUnknown          = 0
	ErrCodeCommissionFailed = 1
	ErrCodeNodeNotReady     = 3 // the node exists but is not reachable right now
	ErrCodeNodeNotResolving = 4 // no CASE session: the node could not be found on the network
	ErrCodeNodeNotExists    = 5 // no node by that id on this fabric
	ErrCodeInvalidArguments = 8
	ErrCodeInvalidCommand   = 9
)

// ServerInfo is the greeting the server sends unprompted the moment a client
// connects. It is what tells GWatch it reached a Matter server rather than
// some other WebSocket, and its fields say whether commissioning can work.
type ServerInfo struct {
	FabricID                  uint64 `json:"fabric_id"`
	CompressedFabricID        uint64 `json:"compressed_fabric_id"`
	SchemaVersion             int    `json:"schema_version"`
	MinSupportedSchemaVersion int    `json:"min_supported_schema_version"`
	SDKVersion                string `json:"sdk_version"`
	WiFiCredentialsSet        bool   `json:"wifi_credentials_set"`
	ThreadCredentialsSet      bool   `json:"thread_credentials_set"`
	BluetoothEnabled          bool   `json:"bluetooth_enabled"`
}

// CommandError is the server's answer to a command it could not carry out.
type CommandError struct {
	Command string
	Code    int
	Details string
}

func (e *CommandError) Error() string {
	d := strings.TrimSpace(e.Details)
	if d == "" {
		d = "no details given"
	}
	return fmt.Sprintf("the Matter server refused %s (error %d, %s): %s", e.Command, e.Code, errorCodeName(e.Code), d)
}

// errorCodeName names a server error code the way its source does, so a
// message can be looked up.
func errorCodeName(code int) string {
	switch code {
	case 1:
		return "NodeCommissionFailed"
	case 2:
		return "NodeInterviewFailed"
	case 3:
		return "NodeNotReady"
	case 4:
		return "NodeNotResolving"
	case 5:
		return "NodeNotExists"
	case 6:
		return "VersionMismatch"
	case 7:
		return "SDKStackError"
	case 8:
		return "InvalidArguments"
	case 9:
		return "InvalidCommand"
	}
	return "UnknownError"
}

// IsCode reports whether err is a server error answer with the given code.
func IsCode(err error, code int) bool {
	var ce *CommandError
	return errors.As(err, &ce) && ce.Code == code
}

// Client is one open conversation with a Matter server. It is not safe for
// concurrent Calls: a monitoring run asks one question at a time, and keeping
// it that way is what lets a reply simply be the next message carrying the
// right id rather than needing a dispatcher goroutine.
type Client struct {
	ws     *wsConn
	Info   ServerInfo
	nextID int
}

// Dial connects to the server at rawURL and waits for its server_info
// greeting. A connection that opens but never greets is not a Matter server,
// and is reported as such once ctx expires.
func Dial(ctx context.Context, rawURL string) (*Client, error) {
	ws, err := dialWebSocket(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	c := &Client{ws: ws}
	if dl, ok := ctx.Deadline(); ok {
		_ = ws.SetDeadline(dl)
	}
	stop := closeOnDone(ctx, ws)
	defer stop()
	msg, err := ws.ReadMessage()
	if err != nil {
		ws.Close()
		return nil, fmt.Errorf("connected, but the server sent no greeting: %w", ctxErr(ctx, err))
	}
	var info ServerInfo
	var probe map[string]json.RawMessage
	if json.Unmarshal(msg, &probe) != nil || probe["schema_version"] == nil {
		ws.Close()
		return nil, errors.New("connected, but the first message was not python-matter-server's server_info — is this the Matter server's address?")
	}
	if err := json.Unmarshal(msg, &info); err != nil {
		ws.Close()
		return nil, fmt.Errorf("could not read the Matter server's server_info: %w", err)
	}
	c.Info = info
	return c, nil
}

// Close ends the conversation.
func (c *Client) Close() error { return c.ws.Close() }

// closeOnDone closes the socket when ctx is cancelled, which is what unblocks
// a read that a deadline alone would not (a cancel with no deadline). The
// returned function stops the watch.
func closeOnDone(ctx context.Context, ws *wsConn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ws.conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	return func() { close(done) }
}

// ctxErr prefers the context's own error over the I/O error it caused, so a
// timeout reads as one.
func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Call sends one command and returns the result of its success answer. Events
// the server pushes in between (it only does so after start_listening, which
// GWatch never sends, but a proxy or a newer server might) and answers to
// other ids are skipped.
func (c *Client) Call(ctx context.Context, command string, args any) (json.RawMessage, error) {
	c.nextID++
	id := strconv.Itoa(c.nextID)
	req := map[string]any{"message_id": id, "command": command}
	if args != nil {
		req["args"] = args
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.ws.SetDeadline(dl)
	} else {
		_ = c.ws.SetDeadline(time.Time{})
	}
	stop := closeOnDone(ctx, c.ws)
	defer stop()
	if err := c.ws.WriteText(body); err != nil {
		return nil, ctxErr(ctx, err)
	}
	for {
		msg, err := c.ws.ReadMessage()
		if err != nil {
			return nil, ctxErr(ctx, err)
		}
		var answer struct {
			MessageID *string         `json:"message_id"`
			Result    json.RawMessage `json:"result"`
			ErrorCode *int            `json:"error_code"`
			Details   string          `json:"details"`
		}
		if err := json.Unmarshal(msg, &answer); err != nil {
			return nil, fmt.Errorf("the Matter server sent something that is not JSON: %w", err)
		}
		if answer.MessageID == nil || *answer.MessageID != id {
			continue
		}
		if answer.ErrorCode != nil {
			return nil, &CommandError{Command: command, Code: *answer.ErrorCode, Details: answer.Details}
		}
		return answer.Result, nil
	}
}

// NodeData is one node as get_nodes and get_node describe it. Attributes is
// the server's cache of every attribute the node reported at its last
// interview or subscription update, keyed "endpoint/cluster/attribute" with
// decimal ids.
type NodeData struct {
	NodeID           uint64                     `json:"node_id"`
	DateCommissioned string                     `json:"date_commissioned"`
	LastInterview    string                     `json:"last_interview"`
	InterviewVersion int                        `json:"interview_version"`
	Available        bool                       `json:"available"`
	IsBridge         bool                       `json:"is_bridge"`
	Attributes       map[string]json.RawMessage `json:"attributes"`
}

// GetNodes lists every node commissioned on the server's fabric.
func (c *Client) GetNodes(ctx context.Context) ([]NodeData, error) {
	raw, err := c.Call(ctx, "get_nodes", nil)
	if err != nil {
		return nil, err
	}
	var nodes []NodeData
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, fmt.Errorf("could not read the node list: %w", err)
	}
	return nodes, nil
}

// GetNode describes one node.
func (c *Client) GetNode(ctx context.Context, nodeID uint64) (NodeData, error) {
	raw, err := c.Call(ctx, "get_node", map[string]any{"node_id": nodeID})
	if err != nil {
		return NodeData{}, err
	}
	var n NodeData
	if err := json.Unmarshal(raw, &n); err != nil {
		return NodeData{}, fmt.Errorf("could not read node %d: %w", nodeID, err)
	}
	return n, nil
}

// ReadAttributes reads the given attribute paths from a node, live: the
// server asks the device rather than answering from its cache. The answer is
// keyed by path; a path the device does not have is absent or null.
//
// Servers since 5.x take a list of paths in one read_attribute; an older one
// accepts only a single string and answers a list with InvalidArguments, so
// that answer falls back to one read per path.
func (c *Client) ReadAttributes(ctx context.Context, nodeID uint64, paths []string) (map[string]json.RawMessage, error) {
	if len(paths) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var arg any = paths
	if len(paths) == 1 {
		arg = paths[0]
	}
	raw, err := c.Call(ctx, "read_attribute", map[string]any{"node_id": nodeID, "attribute_path": arg})
	if err != nil && len(paths) > 1 && IsCode(err, ErrCodeInvalidArguments) {
		out := map[string]json.RawMessage{}
		for _, p := range paths {
			one, err := c.ReadAttributes(ctx, nodeID, []string{p})
			if err != nil {
				return nil, err
			}
			for k, v := range one {
				out[k] = v
			}
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// A very old server answered a single path with the bare value.
		if len(paths) == 1 {
			out[paths[0]] = raw
			return out, nil
		}
		return nil, fmt.Errorf("could not read the attribute values: %w", err)
	}
	return out, nil
}

// Commission joins a device to the server's fabric with a QR code payload
// ("MT:…") or an 11- or 21-digit manual pairing code. networkOnly skips
// Bluetooth discovery, which is what adding a device that is already on the
// network through another controller (multi-admin) needs, and the only thing
// that works on a sidecar without a Bluetooth adapter.
func (c *Client) Commission(ctx context.Context, code string, networkOnly bool) (NodeData, error) {
	raw, err := c.Call(ctx, "commission_with_code", map[string]any{"code": code, "network_only": networkOnly})
	if err != nil {
		return NodeData{}, err
	}
	var n NodeData
	if err := json.Unmarshal(raw, &n); err != nil {
		return NodeData{}, fmt.Errorf("the device was commissioned, but its description could not be read: %w", err)
	}
	return n, nil
}
