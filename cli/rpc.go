// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/rpc"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (c *cli) GetRPC() rpc.CLIClient {
	c.Lock()
	defer c.Unlock()

	if c.rpcconn == nil {
		conn, err := grpc.Dial(core.RPCHost+":"+strconv.FormatUint(uint64(core.RPCPort), 10),
			grpc.WithInsecure(),
			grpc.WithBlock(),
			grpc.FailOnNonTempDialError(true),
			grpc.WithPerRPCCredentials(c.creds))
		if err != nil {
			fmt.Fprintf(os.Stderr, "rpc: %s\n", err)
			os.Exit(1)
		}
		c.rpcconn = conn
		client := rpc.NewCLIClient(c.rpcconn)
		_, err = client.Ping(context.Background(), &empty.Empty{})
		s := status.Convert(err)
		if s.Code() == codes.Unauthenticated {
			if len(c.creds.Password) == 0 {
				fmt.Fprintf(os.Stderr, "Please set the server password with the -P option.\n")
			} else {
				fmt.Fprintf(os.Stderr, "Password refused\n")
			}
			os.Exit(1)
		}
	}

	return rpc.NewCLIClient(c.rpcconn)
}

type loginCreds struct {
	Password string
}

func (c *loginCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"password": c.Password,
	}, nil
}

func (c *loginCreds) RequireTransportSecurity() bool {
	return false
}
