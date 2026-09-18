// Package tools holds the GWatch tool layer: what each MCP tool is called,
// what arguments it takes and what it does against the GWatch API.
//
// Nothing in this package knows about MCP, JSON-RPC or stdio. The transport
// binding lives in internal/mcpserver and does nothing but hand arguments to a
// Tool and turn its Result (or its error) into protocol content, so the tools
// can be exercised in tests without a protocol session at all.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jxburros/GWatch/mcp/internal/gwatch"
)

// Result is what a tool produces: a one-line summary a person can read at a
// glance, and the data behind it.
type Result struct {
	// Summary is one line, no trailing newline.
	Summary string
	// Data marshals to the compact JSON that follows the summary. A nil Data
	// means the summary is the whole answer.
	Data any
}

// Text renders the result the way it is returned to an MCP client: the human
// summary first, then compact JSON on the following line.
func (r Result) Text() string {
	if r.Data == nil {
		return r.Summary
	}
	buf, err := json.Marshal(r.Data)
	if err != nil {
		return r.Summary + "\n(the result could not be encoded as JSON: " + err.Error() + ")"
	}
	return r.Summary + "\n" + string(buf)
}

// Handler runs one tool call. args is the raw JSON object the client sent; it
// may be empty for a tool that takes no arguments.
type Handler func(ctx context.Context, args json.RawMessage) (Result, error)

// Tool is one callable tool, transport-independent.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema (2020-12) object describing Arguments.
	InputSchema map[string]any
	// Write marks a tool that changes GWatch state. Write tools are only
	// registered when the operator passed --allow-write *and* the API key's
	// scope is readwrite; GWatch enforces the same boundary server-side.
	Write   bool
	Handler Handler
}

// ErrUsage is a tool-argument problem: the caller can fix it and try again.
// It is reported as a tool error, never as a protocol error.
type ErrUsage struct{ Msg string }

func (e *ErrUsage) Error() string { return e.Msg }

func usage(format string, a ...any) error { return &ErrUsage{Msg: fmt.Sprintf(format, a...)} }

// decode unmarshals tool arguments, turning a malformed payload into a usage
// error rather than an internal one.
func decode(args json.RawMessage, into any) error {
	if len(strings.TrimSpace(string(args))) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, into); err != nil {
		return usage("the arguments are not valid for this tool: %v", err)
	}
	return nil
}

// Set is the registered tool surface for one session.
type Set struct {
	client     *gwatch.Client
	allowWrite bool
	tools      []Tool
}

// New builds the tool set for client. Write tools are included only when
// allowWrite is true — which the caller sets from `--allow-write` *and* the
// scope GET /api/v1/me reports. Hiding them is a convenience for the model;
// the real boundary is GWatch's own route policy, which answers 403 to a
// read-only key whatever this server registers.
func New(client *gwatch.Client, allowWrite bool) *Set {
	s := &Set{client: client, allowWrite: allowWrite}
	s.tools = append(s.tools, s.readTools()...)
	if allowWrite {
		s.tools = append(s.tools, s.writeTools()...)
	}
	return s
}

// Tools returns the registered tools in a stable order.
func (s *Set) Tools() []Tool { return s.tools }

// AllowWrite reports whether write tools were registered.
func (s *Set) AllowWrite() bool { return s.allowWrite }

// Call runs a tool by name. A missing name is reported with ErrNotFound so the
// transport can answer with a protocol-level error.
func (s *Set) Call(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	for _, t := range s.tools {
		if t.Name == name {
			return t.Handler(ctx, args)
		}
	}
	return Result{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// ErrNotFound means no tool by that name is registered.
var ErrNotFound = errors.New("unknown tool")

// ---- JSON Schema helpers -------------------------------------------------

func object(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func enum(desc string, values ...string) map[string]any {
	vs := make([]any, len(values))
	for i, v := range values {
		vs[i] = v
	}
	return map[string]any{"type": "string", "description": desc, "enum": vs}
}

func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func arrayOf(item map[string]any, desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": item}
}

// freeObject is an object whose keys are not known ahead of time, such as a
// check's type-specific config.
func freeObject(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc, "additionalProperties": true}
}

// plural renders "1 node" / "3 nodes".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
