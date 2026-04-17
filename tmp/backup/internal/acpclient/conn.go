package acpclient

import (
	"encoding/json"
	"io"

	"github.com/OnslaughtSnail/caelis/internal/acpconn"
)

type Conn = acpconn.Conn

func NewConn(reader io.Reader, writer io.Writer) *Conn {
	return acpconn.New(reader, writer)
}

func mustMarshalRaw(v any) json.RawMessage {
	return acpconn.MustMarshalRaw(v)
}
