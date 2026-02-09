package providers

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func statusError(resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("model: empty http response")
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return fmt.Errorf("model: http status %d", resp.StatusCode)
	}
	return fmt.Errorf("model: http status %d body=%s", resp.StatusCode, body)
}
