// Package mcp provides OpenTelemetry instrumentation for Model Context Protocol (MCP).
//
// It follows the OTel MCP semantic conventions:
// https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
//
// Use ExtractContextFromMeta / InjectContextToMeta for trace context propagation
// via the MCP params._meta field, and the span helpers or attributes when tracing
// MCP client/server operations.
package mcp

// Attribute names from OTel MCP semantic conventions.
// https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
const (
	AttrMethodName         = "mcp.method.name"
	AttrErrorType          = "error.type"
	AttrToolName           = "gen_ai.tool.name"
	AttrPromptName         = "gen_ai.prompt.name"
	AttrResourceURI        = "mcp.resource.uri"
	AttrRequestID          = "jsonrpc.request.id"
	AttrResponseStatusCode = "rpc.response.status_code"
	AttrOperationName      = "gen_ai.operation.name"
	AttrProtocolVersion    = "mcp.protocol.version"
	AttrSessionID          = "mcp.session.id"
	AttrNetworkTransport   = "network.transport"
	AttrServerAddress      = "server.address"
	AttrServerPort         = "server.port"
	AttrClientAddress      = "client.address"
	AttrClientPort         = "client.port"
	AttrToolCallArguments  = "gen_ai.tool.call.arguments"
	AttrToolCallResult     = "gen_ai.tool.call.result"
)

// Well-known MCP method names (request/notification methods).
const (
	MethodToolsCall     = "tools/call"
	MethodToolsList     = "tools/list"
	MethodResourcesRead = "resources/read"
	MethodResourcesList = "resources/list"
	MethodPromptsGet    = "prompts/get"
	MethodPromptsList   = "prompts/list"
	MethodPing          = "ping"
	MethodInitialize    = "initialize"
)

// Gen AI operation name for tool execution (used with AttrOperationName).
const OperationExecuteTool = "execute_tool"

// Metric names from OTel MCP semantic conventions.
const (
	MetricClientOperationDuration = "mcp.client.operation.duration"
	MetricServerOperationDuration = "mcp.server.operation.duration"
	MetricClientSessionDuration   = "mcp.client.session.duration"
	MetricServerSessionDuration   = "mcp.server.session.duration"
)
