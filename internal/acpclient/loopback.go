package acpclient

import (
	"context"
	"fmt"
	"io"
)

func StartLoopback(ctx context.Context, cfg Config, reader io.Reader, writer io.Writer) (*Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acpclient: context is required")
	}
	if reader == nil || writer == nil {
		return nil, fmt.Errorf("acpclient: loopback reader and writer are required")
	}
	serveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	conn := NewConn(reader, writer)
	coreCfg := CoreClientConfig{
		MCPServers:          cfg.MCPServers,
		ClientInfo:          cfg.ClientInfo,
		OnUpdate:            cfg.OnUpdate,
		OnPermissionRequest: cfg.OnPermissionRequest,
	}
	local := NewLocalClient(conn, coreCfg, LocalClientConfig{Runtime: cfg.Runtime})
	client := &Client{
		core:   local.core,
		local:  local,
		conn:   conn,
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() {
		err := client.conn.Serve(serveCtx, local.handleRequest, local.handleNotification)
		client.done <- err
	}()
	go func() {
		<-ctx.Done()
		client.Close()
	}()
	return client, nil
}
