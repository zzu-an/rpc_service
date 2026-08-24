package main

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type CallStats struct {
	UnaryCalls  atomic.Int64
	StreamCalls atomic.Int64
}

type echoServer struct{}

func (echoServer) UnaryEcho(ctx context.Context, request *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	if request.Value == "invalid" {
		return nil, status.Error(codes.InvalidArgument, "value must not be invalid")
	}
	if request.Value == "unavailable" {
		return nil, status.Error(codes.Unavailable, "temporary dependency failure")
	}
	if strings.HasPrefix(request.Value, "delay:") {
		delay, err := time.ParseDuration(strings.TrimPrefix(request.Value, "delay:"))
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "bad delay")
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}

	requestID := "missing"
	if incoming, ok := metadata.FromIncomingContext(ctx); ok && len(incoming.Get("x-request-id")) > 0 {
		requestID = incoming.Get("x-request-id")[0]
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-server", "grpc-lab"))
	grpc.SetTrailer(ctx, metadata.Pairs("x-request-id-seen", requestID))
	return wrapperspb.String("echo:" + request.Value), nil
}

func (echoServer) ServerStream(request *wrapperspb.StringValue, stream EchoServiceServerStreamServer) error {
	for i := 1; i <= 3; i++ {
		select {
		case <-stream.Context().Done():
			return status.FromContextError(stream.Context().Err()).Err()
		case <-time.After(5 * time.Millisecond):
		}
		if err := stream.Send(wrapperspb.String(request.Value + ":part" + string(rune('0'+i)))); err != nil {
			return err
		}
	}
	return nil
}

func (echoServer) ClientStream(stream EchoServiceClientStreamServer) error {
	var values []string
	for {
		message, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(wrapperspb.String(strings.Join(values, ",")))
		}
		if err != nil {
			return err
		}
		values = append(values, message.Value)
	}
}

func NewServer(stats *CallStats) *grpc.Server {
	server := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 30 * time.Second,
			Time:              2 * time.Hour,
			Timeout:           20 * time.Second,
		}),
		grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			stats.UnaryCalls.Add(1)
			return handler(ctx, request)
		}),
		grpc.StreamInterceptor(func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			stats.StreamCalls.Add(1)
			return handler(server, stream)
		}),
	)
	RegisterEchoServiceServer(server, echoServer{})
	return server
}

func Serve(server *grpc.Server, listener net.Listener) <-chan error {
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	return done
}

// GracefulStop adds a deadline to grpc.GracefulStop, whose API otherwise has
// no context. On timeout it forces all remaining RPCs to end.
func GracefulStop(ctx context.Context, server *grpc.Server) error {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		server.Stop()
		<-done
		return context.Cause(ctx)
	}
}
