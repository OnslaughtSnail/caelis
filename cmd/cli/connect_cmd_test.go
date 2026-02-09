package main

import "testing"

func TestDefaultAPIKeyEnvForProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{provider: "deepseek", want: "DEEPSEEK_API_KEY"},
		{provider: "openai-compatible", want: "OPENAI_COMPATIBLE_API_KEY"},
		{provider: "  xiaomi ", want: "XIAOMI_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := defaultAPIKeyEnvForProvider(tt.provider)
			if got != tt.want {
				t.Fatalf("defaultAPIKeyEnvForProvider(%q)=%q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestSanitizeEnvName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "deepseek", want: "DEEPSEEK"},
		{input: "openai-compatible", want: "OPENAI_COMPATIBLE"},
		{input: "a b*c", want: "A_B_C"},
		{input: "__x__", want: "X"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEnvName(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeEnvName(%q)=%q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
