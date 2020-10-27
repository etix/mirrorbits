// Copyright (c) 2014-2020 Ludovic Fauvet
// Licensed under the MIT license

package rpc

import (
	"context"

	. "github.com/etix/mirrorbits/config"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func StreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := authorize(stream.Context()); err != nil {
		return err
	}

	return handler(srv, stream)
}

func UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if err := authorize(ctx); err != nil {
		return nil, err
	}

	return handler(ctx, req)
}

func authorize(ctx context.Context) error {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if md["password"][0] == GetConfig().RPCPassword {
			return nil
		}
	}

	return status.Error(codes.Unauthenticated, "access denied")
}
