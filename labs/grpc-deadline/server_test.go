package main

import (
	"context"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type testRPC struct {
	client     *EchoServiceClient
	connection *grpc.ClientConn
	server     *grpc.Server
	stats      *CallStats
}

func startTestRPC(t *testing.T) *testRPC {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	stats := &CallStats{}
	server := NewServer(stats)
	Serve(server, listener)

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 5 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		connection.Close()
		server.Stop()
		listener.Close()
	})
	return &testRPC{client: NewEchoServiceClient(connection), connection: connection, server: server, stats: stats}
}

func TestUnaryMetadataAndInterceptor(t *testing.T) {
	rpc := startTestRPC(t)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-request-id", "req-42"))
	var header, trailer metadata.MD
	response, err := rpc.client.UnaryEcho(ctx, wrapperspb.String("hello"), grpc.Header(&header), grpc.Trailer(&trailer))
	if err != nil {
		t.Fatal(err)
	}
	if response.Value != "echo:hello" || header.Get("x-server")[0] != "grpc-lab" || trailer.Get("x-request-id-seen")[0] != "req-42" {
		t.Fatalf("response=%q header=%v trailer=%v", response.Value, header, trailer)
	}
	if rpc.stats.UnaryCalls.Load() != 1 {
		t.Fatal("unary interceptor did not run")
	}
}

func TestUnaryStatusCode(t *testing.T) {
	rpc := startTestRPC(t)
	_, err := rpc.client.UnaryEcho(context.Background(), wrapperspb.String("invalid"))
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%s err=%v", status.Code(err), err)
	}
}

func TestDeadlinePropagatesToServer(t *testing.T) {
	rpc := startTestRPC(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := rpc.client.UnaryEcho(ctx, wrapperspb.String("delay:200ms"))
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code=%s err=%v", status.Code(err), err)
	}
}

func TestServerStreaming(t *testing.T) {
	rpc := startTestRPC(t)
	stream, err := rpc.client.ServerStream(context.Background(), wrapperspb.String("item"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		message, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, message.Value)
	}
	want := []string{"item:part1", "item:part2", "item:part3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	if rpc.stats.StreamCalls.Load() != 1 {
		t.Fatal("stream interceptor did not run")
	}
}

func TestClientStreaming(t *testing.T) {
	rpc := startTestRPC(t)
	stream, err := rpc.client.ClientStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"a", "b", "c"} {
		if err := stream.Send(wrapperspb.String(value)); err != nil {
			t.Fatal(err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if response.Value != "a,b,c" {
		t.Fatalf("response=%q", response.Value)
	}
}

func TestGracefulStop(t *testing.T) {
	rpc := startTestRPC(t)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := GracefulStop(shutdownCtx, rpc.server); err != nil {
		t.Fatal(err)
	}
}
