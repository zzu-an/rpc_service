package main

import (
	"context"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const echoServiceName = "lab.echo.v1.EchoService"

// The small adapter below is the code shape protoc-gen-go-grpc would generate.
// Keeping it here makes the lab runnable without a system protoc installation.
type EchoServiceServer interface {
	UnaryEcho(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
	ServerStream(*wrapperspb.StringValue, EchoServiceServerStreamServer) error
	ClientStream(EchoServiceClientStreamServer) error
}

type EchoServiceServerStreamServer interface {
	Send(*wrapperspb.StringValue) error
	grpc.ServerStream
}

type EchoServiceClientStreamServer interface {
	Recv() (*wrapperspb.StringValue, error)
	SendAndClose(*wrapperspb.StringValue) error
	grpc.ServerStream
}

func RegisterEchoServiceServer(registrar grpc.ServiceRegistrar, server EchoServiceServer) {
	registrar.RegisterService(&EchoService_ServiceDesc, server)
}

func unaryEchoHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(wrapperspb.StringValue)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(EchoServiceServer).UnaryEcho(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: "/" + echoServiceName + "/UnaryEcho"}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(EchoServiceServer).UnaryEcho(ctx, request.(*wrapperspb.StringValue))
	}
	return interceptor(ctx, request, info, handler)
}

type serverStreamServer struct{ grpc.ServerStream }

func (s *serverStreamServer) Send(message *wrapperspb.StringValue) error {
	return s.ServerStream.SendMsg(message)
}

func serverStreamHandler(server any, stream grpc.ServerStream) error {
	request := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	return server.(EchoServiceServer).ServerStream(request, &serverStreamServer{ServerStream: stream})
}

type clientStreamServer struct{ grpc.ServerStream }

func (s *clientStreamServer) Recv() (*wrapperspb.StringValue, error) {
	message := new(wrapperspb.StringValue)
	if err := s.ServerStream.RecvMsg(message); err != nil {
		return nil, err
	}
	return message, nil
}

func (s *clientStreamServer) SendAndClose(message *wrapperspb.StringValue) error {
	return s.ServerStream.SendMsg(message)
}

func clientStreamHandler(server any, stream grpc.ServerStream) error {
	return server.(EchoServiceServer).ClientStream(&clientStreamServer{ServerStream: stream})
}

var EchoService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: echoServiceName,
	HandlerType: (*EchoServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "UnaryEcho",
		Handler:    unaryEchoHandler,
	}},
	Streams: []grpc.StreamDesc{
		{StreamName: "ServerStream", Handler: serverStreamHandler, ServerStreams: true},
		{StreamName: "ClientStream", Handler: clientStreamHandler, ClientStreams: true},
	},
	Metadata: "echo.proto",
}

type EchoServiceClient struct{ connection grpc.ClientConnInterface }

func NewEchoServiceClient(connection grpc.ClientConnInterface) *EchoServiceClient {
	return &EchoServiceClient{connection: connection}
}

func (c *EchoServiceClient) UnaryEcho(ctx context.Context, request *wrapperspb.StringValue, options ...grpc.CallOption) (*wrapperspb.StringValue, error) {
	response := new(wrapperspb.StringValue)
	err := c.connection.Invoke(ctx, "/"+echoServiceName+"/UnaryEcho", request, response, options...)
	return response, err
}

type EchoServiceServerStreamClient struct{ grpc.ClientStream }

func (c *EchoServiceClient) ServerStream(ctx context.Context, request *wrapperspb.StringValue, options ...grpc.CallOption) (*EchoServiceServerStreamClient, error) {
	stream, err := c.connection.NewStream(ctx, &EchoService_ServiceDesc.Streams[0], "/"+echoServiceName+"/ServerStream", options...)
	if err != nil {
		return nil, err
	}
	client := &EchoServiceServerStreamClient{ClientStream: stream}
	if err := client.SendMsg(request); err != nil {
		return nil, err
	}
	if err := client.CloseSend(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *EchoServiceServerStreamClient) Recv() (*wrapperspb.StringValue, error) {
	message := new(wrapperspb.StringValue)
	if err := c.ClientStream.RecvMsg(message); err != nil {
		return nil, err
	}
	return message, nil
}

type EchoServiceClientStreamClient struct{ grpc.ClientStream }

func (c *EchoServiceClient) ClientStream(ctx context.Context, options ...grpc.CallOption) (*EchoServiceClientStreamClient, error) {
	stream, err := c.connection.NewStream(ctx, &EchoService_ServiceDesc.Streams[1], "/"+echoServiceName+"/ClientStream", options...)
	if err != nil {
		return nil, err
	}
	return &EchoServiceClientStreamClient{ClientStream: stream}, nil
}

func (c *EchoServiceClientStreamClient) Send(message *wrapperspb.StringValue) error {
	return c.ClientStream.SendMsg(message)
}

func (c *EchoServiceClientStreamClient) CloseAndRecv() (*wrapperspb.StringValue, error) {
	if err := c.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	message := new(wrapperspb.StringValue)
	if err := c.ClientStream.RecvMsg(message); err != nil && err != io.EOF {
		return nil, err
	}
	return message, nil
}
