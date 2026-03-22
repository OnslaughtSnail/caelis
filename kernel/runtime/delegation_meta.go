package runtime

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type DelegationMetadata struct {
	ParentSessionID string
	ChildSessionID  string
	ParentToolCall  string
	ParentToolName  string
	DelegationID    string
}

func DelegationMetadataFromEvent(ev *session.Event) (DelegationMetadata, bool) {
	if ev == nil || len(ev.Meta) == 0 {
		return DelegationMetadata{}, false
	}
	meta := DelegationMetadata{
		ParentSessionID: strings.TrimSpace(subagentStringValue(ev.Meta[metaParentSessionID])),
		ChildSessionID:  strings.TrimSpace(subagentStringValue(ev.Meta[metaChildSessionID])),
		ParentToolCall:  strings.TrimSpace(subagentStringValue(ev.Meta[metaParentToolCall])),
		ParentToolName:  strings.TrimSpace(subagentStringValue(ev.Meta[metaParentToolName])),
		DelegationID:    strings.TrimSpace(subagentStringValue(ev.Meta[metaDelegationID])),
	}
	if meta.ParentSessionID == "" && meta.ChildSessionID == "" && meta.ParentToolCall == "" && meta.DelegationID == "" {
		return DelegationMetadata{}, false
	}
	return meta, true
}
