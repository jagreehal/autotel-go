package mcp

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jagreehal/autotel-go/v2"
)

// ClientSpanOptions hold optional attributes for an MCP client span.
type ClientSpanOptions struct {
	ToolName          string
	PromptName        string
	ResourceURI       string
	RequestID         string
	NetworkTransport  string
	SessionID         string
	ProtocolVersion   string
	ServerAddress     string
	ServerPort        int64
	CaptureToolArgs   bool
	ToolCallArguments string
	CaptureToolResult bool
	ToolCallResult    string
}

// ServerSpanOptions hold optional attributes for an MCP server span.
type ServerSpanOptions struct {
	ToolName          string
	PromptName        string
	ResourceURI       string
	RequestID         string
	NetworkTransport  string
	SessionID         string
	ProtocolVersion   string
	ClientAddress     string
	ClientPort        int64
	CaptureToolArgs   bool
	ToolCallArguments string
	CaptureToolResult bool
	ToolCallResult    string
}

// StartClientSpan starts a CLIENT span for an MCP operation, following OTel MCP semantic conventions.
// Span name is "{methodName}" or "{methodName} {target}" when target (tool/prompt name or resource URI) is set.
// Caller should call span.End() and optionally span.RecordError(err) / span.SetStatus on error.
func StartClientSpan(ctx context.Context, methodName string, opts *ClientSpanOptions) (context.Context, autotel.Span) {
	spanName := methodName
	if opts != nil {
		if opts.ToolName != "" {
			spanName = methodName + " " + opts.ToolName
		} else if opts.PromptName != "" {
			spanName = methodName + " " + opts.PromptName
		}
	}
	ctx, span := autotel.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))
	setClientAttributes(span, methodName, opts)
	return ctx, span
}

// StartServerSpan starts a SERVER span for an MCP operation, following OTel MCP semantic conventions.
// Use after extracting context with ExtractContextFromMeta so the span is a child of the client span.
func StartServerSpan(ctx context.Context, methodName string, opts *ServerSpanOptions) (context.Context, autotel.Span) {
	spanName := methodName
	if opts != nil {
		if opts.ToolName != "" {
			spanName = methodName + " " + opts.ToolName
		} else if opts.PromptName != "" {
			spanName = methodName + " " + opts.PromptName
		}
	}
	ctx, span := autotel.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer))
	setServerAttributes(span, methodName, opts)
	return ctx, span
}

func setClientAttributes(span autotel.Span, methodName string, opts *ClientSpanOptions) {
	span.SetAttribute(AttrMethodName, methodName)
	if opts == nil {
		return
	}
	if opts.ToolName != "" {
		span.SetAttribute(AttrToolName, opts.ToolName)
		span.SetAttribute(AttrOperationName, OperationExecuteTool)
	}
	if opts.PromptName != "" {
		span.SetAttribute(AttrPromptName, opts.PromptName)
	}
	if opts.ResourceURI != "" {
		span.SetAttribute(AttrResourceURI, opts.ResourceURI)
	}
	if opts.RequestID != "" {
		span.SetAttribute(AttrRequestID, opts.RequestID)
	}
	if opts.NetworkTransport != "" {
		span.SetAttribute(AttrNetworkTransport, opts.NetworkTransport)
	}
	if opts.SessionID != "" {
		span.SetAttribute(AttrSessionID, opts.SessionID)
	}
	if opts.ProtocolVersion != "" {
		span.SetAttribute(AttrProtocolVersion, opts.ProtocolVersion)
	}
	if opts.ServerAddress != "" {
		span.SetAttribute(AttrServerAddress, opts.ServerAddress)
	}
	if opts.ServerPort != 0 {
		span.SetAttribute(AttrServerPort, opts.ServerPort)
	}
	if opts.CaptureToolArgs && opts.ToolCallArguments != "" {
		span.SetAttribute(AttrToolCallArguments, opts.ToolCallArguments)
	}
	if opts.CaptureToolResult && opts.ToolCallResult != "" {
		span.SetAttribute(AttrToolCallResult, opts.ToolCallResult)
	}
}

func setServerAttributes(span autotel.Span, methodName string, opts *ServerSpanOptions) {
	span.SetAttribute(AttrMethodName, methodName)
	if opts == nil {
		return
	}
	if opts.ToolName != "" {
		span.SetAttribute(AttrToolName, opts.ToolName)
		span.SetAttribute(AttrOperationName, OperationExecuteTool)
	}
	if opts.PromptName != "" {
		span.SetAttribute(AttrPromptName, opts.PromptName)
	}
	if opts.ResourceURI != "" {
		span.SetAttribute(AttrResourceURI, opts.ResourceURI)
	}
	if opts.RequestID != "" {
		span.SetAttribute(AttrRequestID, opts.RequestID)
	}
	if opts.NetworkTransport != "" {
		span.SetAttribute(AttrNetworkTransport, opts.NetworkTransport)
	}
	if opts.SessionID != "" {
		span.SetAttribute(AttrSessionID, opts.SessionID)
	}
	if opts.ProtocolVersion != "" {
		span.SetAttribute(AttrProtocolVersion, opts.ProtocolVersion)
	}
	if opts.ClientAddress != "" {
		span.SetAttribute(AttrClientAddress, opts.ClientAddress)
	}
	if opts.ClientPort != 0 {
		span.SetAttribute(AttrClientPort, opts.ClientPort)
	}
	if opts.CaptureToolArgs && opts.ToolCallArguments != "" {
		span.SetAttribute(AttrToolCallArguments, opts.ToolCallArguments)
	}
	if opts.CaptureToolResult && opts.ToolCallResult != "" {
		span.SetAttribute(AttrToolCallResult, opts.ToolCallResult)
	}
}

// SetSpanError sets error type and status on the span (call from RecordError then this for spec compliance).
func SetSpanError(span autotel.Span, errType string, message string) {
	span.SetAttribute(AttrErrorType, errType)
	span.SetStatus(codes.Error, message)
}

// ToolErrorType is the recommended error.type value when CallToolResult has isError true.
const ToolErrorType = "tool_error"
