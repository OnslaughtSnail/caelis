package acp

import (
	"encoding/json"
	"io"

	"github.com/OnslaughtSnail/caelis/internal/acpconn"
)

type Conn = acpconn.Conn
type postWriteResult = acpconn.PostWriteResult

func NewConn(reader io.Reader, writer io.Writer) *Conn {
	return acpconn.New(reader, writer)
}

func mustMarshalRaw(v any) json.RawMessage {
	return acpconn.MustMarshalRaw(v)
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}
