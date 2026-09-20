// Package mcpserver binds the GWatch tool layer to the Model Context Protocol.
//
// It is the only package that knows about MCP at all: it turns a tools.Set into
// registered MCP tools and answers tools/call by handing the raw arguments to
// the matching handler. The protocol itself — initialize, notifications,
// tools/list, ping, JSON-RPC framing over stdio — is the official Go SDK's job
// (github.com/modelcontextprotocol/go-sdk). Its go.mod requirement decides
// this module's own Go floor, and CI's govulncheck is what says when a newer
// release is due.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jxburros/GWatch/mcp/internal/tools"
)

// Instructions is the hint shown to clients after initialize. It says out loud
// what the trust boundary is, because the model on the other side is the one
// deciding which tool to call.
const Instructions = `GWatch is a local network monitor. Use these tools to read what it is watching ` +
	`(gwatch_overview first, then gwatch_list_nodes, gwatch_history and gwatch_events) and, where write tools ` +
	`are present, to manage nodes and their checks.

Write tools only appear when the operator started this server with --allow-write and the API key has the ` +
	`readwrite scope. GWatch also enforces this itself: a read-only key is refused with HTTP 403 whatever is ` +
	`called, and that refusal is final — do not look for another way around it. Deleting a node destroys its ` +
	`history, so gwatch_delete_node requires confirm: true and should be agreed with the person first.

GWatch never exposes its settings, backups, service log, automation triggers, endpoints, user accounts or ` +
	`API keys to an API key, so there are no tools for them.`

// New builds an MCP server exposing set. name/version identify this server to
// the client.
func New(set *tools.Set, name, version string) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: name, Title: "GWatch", Version: version},
		&mcp.ServerOptions{Instructions: Instructions, HasTools: true},
	)
	for _, t := range set.Tools() {
		srv.AddTool(mcpTool(t), handler(t))
	}
	return srv
}

// mcpTool describes one tool to the protocol.
func mcpTool(t tools.Tool) *mcp.Tool {
	return &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    !t.Write,
			DestructiveHint: destructive(t),
			IdempotentHint:  t.Name == "gwatch_set_node_enabled" || t.Name == "gwatch_silence_node",
			OpenWorldHint:   ptr(true),
		},
	}
}

func ptr[T any](v T) *T { return &v }

// destructive marks the tools that can lose data or change the world
// irreversibly, so a client that asks for confirmation knows when to.
func destructive(t tools.Tool) *bool {
	switch t.Name {
	case "gwatch_delete_node", "gwatch_update_node":
		return ptr(true)
	default:
		return ptr(false)
	}
}

// handler runs a tool call. Every failure is reported as a tool error
// (IsError, with the message as text) rather than a protocol error: the model
// needs to see that GWatch said "this API key is read-only" so it can tell the
// person, instead of the session failing with an opaque JSON-RPC fault. An
// unknown tool name never reaches here — the SDK answers that with a
// protocol-level error of its own.
func handler(t tools.Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args []byte
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		res, err := t.Handler(ctx, args)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: res.Text()}},
		}, nil
	}
}

// RunStdio serves the MCP session on stdin/stdout until the client
// disconnects or ctx is cancelled.
func RunStdio(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}
