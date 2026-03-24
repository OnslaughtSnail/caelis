package acpclient

import (
	"context"
	"fmt"
	"io"
)

func StartLoopback(ctx context.Context, cfg Config, reader io.Reader, writer io.Writer) (*Client, error) {
	if reader == nil || writer == nil {
		return nil, fmt.Errorf("acpclient: loopback reader and writer are required")
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		cfg:       cfg,
		conn:      NewConn(reader, writer),
		cancel:    cancel,
		done:      make(chan error, 1),
		terminals: map[string]clientTerminal{},
	}
	go func() {
		err := client.conn.Serve(serveCtx, client.handleRequest, client.handleNotification)
		client.done <- err
	}()
	go func() {
		<-ctx.Done()
		client.Close()
	}()
	return client, nil
}
