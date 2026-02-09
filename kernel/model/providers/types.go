package providers

import "time"

// APIType defines protocol dialect used by a model provider.
type APIType string

const (
	APIOpenAI           APIType = "openai"
	APIOpenAICompatible APIType = "openai_compatible"
	APIGemini           APIType = "gemini"
	APIAnthropic        APIType = "anthropic"
	APIDeepSeek         APIType = "deepseek"
)

// AuthType defines model provider authentication strategy.
type AuthType string

const (
	AuthAPIKey      AuthType = "api_key"
	AuthBearerToken AuthType = "bearer_token"
	AuthOAuthToken  AuthType = "oauth_token"
)

// AuthConfig is provider-agnostic auth configuration.
type AuthConfig struct {
	Type          AuthType
	TokenEnv      string
	Token         string
	CredentialRef string
	HeaderKey     string
	Prefix        string
}

// Config is a provider-agnostic model alias definition.
type Config struct {
	Alias               string
	Provider            string
	API                 APIType
	Model               string
	BaseURL             string
	Headers             map[string]string
	Timeout             time.Duration
	MaxOutputTok        int
	ContextWindowTokens int
	Auth                AuthConfig
}
