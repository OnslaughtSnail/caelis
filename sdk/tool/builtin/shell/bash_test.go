package shell

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/OnslaughtSnail/caelis/sdk/sandbox/host"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

func TestBashDefinitionExposesMinimalArguments(t *testing.T) {
	t.Parallel()

	rt, err := host.New(host.Config{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatalf("NewBash() error = %v", err)
	}
	properties, _ := tool.Definition().InputSchema["properties"].(map[string]any)
	if _, ok := properties["command"]; !ok {
		t.Fatal("command property missing")
	}
	if _, ok := properties["workdir"]; !ok {
		t.Fatal("workdir property missing")
	}
	if _, ok := properties["yield_time_ms"]; !ok {
		t.Fatal("yield_time_ms property missing")
	}
	if _, ok := properties["timeout_ms"]; ok {
		t.Fatal("timeout_ms property unexpectedly exposed")
	}
	if _, ok := properties["tty"]; ok {
		t.Fatal("tty property unexpectedly exposed")
	}
	if _, ok := properties["env"]; ok {
		t.Fatal("env property unexpectedly exposed")
	}
	if _, ok := properties["dir"]; ok {
		t.Fatal("dir alias unexpectedly exposed")
	}
}

func TestBashCallAcceptsYieldTimeWithoutChangingSyncResult(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rt, err := host.New(host.Config{CWD: dir})
	if err != nil {
		t.Fatalf("host.New() error = %v", err)
	}
	tool, err := NewBash(BashConfig{Runtime: rt})
	if err != nil {
		t.Fatalf("NewBash() error = %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"command":       "printf 'ok'",
		"workdir":       dir,
		"yield_time_ms": 25,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := tool.Call(context.Background(), sdktool.Call{Name: BashToolName, Input: raw})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("result.Content = empty, want json payload")
	}
}
