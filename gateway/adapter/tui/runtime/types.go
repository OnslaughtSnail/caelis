package runtime

import (
	"context"
	"errors"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

var ErrMigrationPending = errors.New("tui/runtime: legacy tui migration wiring pending")

type SubmissionMode string

const (
	SubmissionModeDefault SubmissionMode = ""
	SubmissionModeOverlay SubmissionMode = "overlay"
)

type Attachment struct {
	Name   string
	Offset int
}

type Submission struct {
	Text        string
	DisplayText string
	Mode        SubmissionMode
	Attachments []Attachment
}

type StatusSnapshot struct {
	SessionID    string
	Workspace    string
	Model        string
	ModeLabel    string
	Surface      string
	PromptTokens int
	Running      bool
}

type ResumeCandidate struct {
	SessionID string
	Prompt    string
	Age       string
}

type SlashArgCandidate struct {
	Value   string
	Display string
	Detail  string
	NoAuth  bool
}

type ConnectConfig struct {
	Provider            string
	Model               string
	BaseURL             string
	TimeoutSeconds      int
	APIKey              string
	TokenEnv            string
	AuthType            string
	ContextWindowTokens int
	MaxOutputTokens     int
	ReasoningLevels     []string
}

type Turn interface {
	HandleID() string
	RunID() string
	TurnID() string
	SessionRef() sdksession.SessionRef
	Events() <-chan appgateway.EventEnvelope
	Submit(context.Context, appgateway.SubmitRequest) error
	Cancel() bool
	Close() error
}

// Driver is the only backend boundary that the transplanted legacy-style TUI
// shell should depend on.
type Driver interface {
	Status(context.Context) (StatusSnapshot, error)
	WorkspaceDir() string

	Submit(context.Context, Submission) (Turn, error)
	Interrupt(context.Context) error

	NewSession(context.Context) (sdksession.Session, error)
	ResumeSession(context.Context, string) (sdksession.Session, error)
	ListSessions(context.Context, int) ([]ResumeCandidate, error)
	ReplayEvents(context.Context) ([]appgateway.EventEnvelope, error)
	Compact(context.Context, string) error

	Connect(context.Context, ConnectConfig) (StatusSnapshot, error)
	UseModel(context.Context, string) (StatusSnapshot, error)
	DeleteModel(context.Context, string) error
	SetSandboxMode(context.Context, string) (StatusSnapshot, error)

	CompleteMention(context.Context, string, int) ([]string, error)
	CompleteFile(context.Context, string, int) ([]string, error)
	CompleteSkill(context.Context, string, int) ([]string, error)
	CompleteResume(context.Context, string, int) ([]ResumeCandidate, error)
	CompleteSlashArg(context.Context, string, string, int) ([]SlashArgCandidate, error)
}
