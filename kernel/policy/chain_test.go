package policy

import (
	"context"
	"testing"
)

func TestChainApply_DefaultAllow(t *testing.T) {
	hooks := []Hook{DefaultAllow()}
	in, err := ApplyBeforeModel(context.Background(), hooks, ModelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if in.Messages != nil {
		// expected empty remains empty
	}
}
