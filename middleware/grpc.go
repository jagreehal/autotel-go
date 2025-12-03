package middleware

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc/stats"
)

// GRPCServerHandler returns a gRPC stats handler for server-side instrumentation.
// The newer otelgrpc API uses stats handlers instead of interceptors.
//
// Example:
//
//	server := grpc.NewServer(
//		grpc.StatsHandler(middleware.GRPCServerHandler()),
//	)
func GRPCServerHandler(opts ...otelgrpc.Option) stats.Handler {
	return otelgrpc.NewServerHandler(opts...)
}

// GRPCClientHandler returns a gRPC stats handler for client-side instrumentation.
// The newer otelgrpc API uses stats handlers instead of interceptors.
//
// Example:
//
//	conn, err := grpc.NewClient("localhost:50051",
//		grpc.WithStatsHandler(middleware.GRPCClientHandler()),
//	)
func GRPCClientHandler(opts ...otelgrpc.Option) stats.Handler {
	return otelgrpc.NewClientHandler(opts...)
}
