package delegation

import (
	"context"
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

const (
	MetaParentSessionID = "parent_session_id"
	MetaChildSessionID  = "child_session_id"
	MetaParentToolCall  = "parent_tool_call_id"
	MetaParentToolName  = "parent_tool_name"
	MetaDelegationID    = "delegation_id"
)

type Metadata struct {
	ParentSessionID string
	ChildSessionID  string
	ParentToolCall  string
	ParentToolName  string
	DelegationID    string
}

type Lineage struct {
	ParentSessionID string
	ChildSessionID  string
	ParentToolCall  string
	ParentToolName  string
	DelegationID    string
	TaskID          string
}

type lineageContextKey struct{}

func WithLineage(ctx context.Context, lineage Lineage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, lineageContextKey{}, lineage)
}

func LineageFromContext(ctx context.Context) (Lineage, bool) {
	if ctx == nil {
		return Lineage{}, false
	}
	lineage, ok := ctx.Value(lineageContextKey{}).(Lineage)
	return lineage, ok
}

func DetachContext(ctx context.Context, lineage Lineage) context.Context {
	base := WithLineage(context.Background(), lineage)
	if approver, ok := toolexec.ApproverFromContext(ctx); ok {
		base = toolexec.WithApprover(base, approver)
	}
	if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
		base = policy.WithToolAuthorizer(base, authorizer)
	}
	if streamer, ok := sessionstream.StreamerFromContext(ctx); ok {
		base = sessionstream.WithStreamer(base, streamer)
	}
	return base
}

func AttachContext(ctx context.Context, lineage Lineage) context.Context {
	base := WithLineage(context.Background(), lineage)
	if approver, ok := toolexec.ApproverFromContext(ctx); ok {
		base = toolexec.WithApprover(base, approver)
	}
	if authorizer, ok := policy.ToolAuthorizerFromContext(ctx); ok {
		base = policy.WithToolAuthorizer(base, authorizer)
	}
	if streamer, ok := sessionstream.StreamerFromContext(ctx); ok {
		base = sessionstream.WithStreamer(base, streamer)
	}
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		base, cancel = context.WithDeadline(base, deadline)
		context.AfterFunc(ctx, cancel)
		return base
	}
	base, cancel := context.WithCancel(base)
	context.AfterFunc(ctx, cancel)
	return base
}

func AnnotateEvent(ev *session.Event, sessionID string, lineage Lineage) {
	if ev == nil {
		return
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if value := strings.TrimSpace(lineage.ParentSessionID); value != "" {
		ev.Meta[MetaParentSessionID] = value
	}
	childSessionID := strings.TrimSpace(lineage.ChildSessionID)
	if childSessionID == "" {
		childSessionID = strings.TrimSpace(sessionID)
	}
	if childSessionID != "" {
		ev.Meta[MetaChildSessionID] = childSessionID
	}
	if value := strings.TrimSpace(lineage.ParentToolCall); value != "" {
		ev.Meta[MetaParentToolCall] = value
	}
	if value := strings.TrimSpace(lineage.ParentToolName); value != "" {
		ev.Meta[MetaParentToolName] = value
	}
	if value := strings.TrimSpace(lineage.DelegationID); value != "" {
		ev.Meta[MetaDelegationID] = value
	}
}

func MetadataFromEvent(ev *session.Event) (Metadata, bool) {
	if ev == nil || len(ev.Meta) == 0 {
		return Metadata{}, false
	}
	meta := Metadata{
		ParentSessionID: strings.TrimSpace(stringValue(ev.Meta[MetaParentSessionID])),
		ChildSessionID:  strings.TrimSpace(stringValue(ev.Meta[MetaChildSessionID])),
		ParentToolCall:  strings.TrimSpace(stringValue(ev.Meta[MetaParentToolCall])),
		ParentToolName:  strings.TrimSpace(stringValue(ev.Meta[MetaParentToolName])),
		DelegationID:    strings.TrimSpace(stringValue(ev.Meta[MetaDelegationID])),
	}
	if meta.ParentSessionID == "" && meta.ChildSessionID == "" && meta.ParentToolCall == "" && meta.DelegationID == "" {
		return Metadata{}, false
	}
	return meta, true
}

func ResolveChildSessionID(parentSessionID string, requested string, newID func() string) (string, error) {
	requested = strings.TrimSpace(requested)
	parentSessionID = strings.TrimSpace(parentSessionID)
	if requested == "" {
		if newID == nil {
			return "", fmt.Errorf("delegation: child session id generator is nil")
		}
		return newID(), nil
	}
	if requested == parentSessionID {
		return "", fmt.Errorf("runtime: delegated child session_id must differ from parent session")
	}
	return requested, nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
