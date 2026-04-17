package acpmeta

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	metaKeyRoot           = "caelis"
	metaKeyDelegatedChild = "delegatedChild"
	metaKeyModelAlias     = "modelAlias"
	stateKeyACP           = "acp"
	stateKeyController    = "controller"
	stateKeyMeta          = "meta"
	controllerKeyAgentID  = "agentId"
	controllerKeySession  = "sessionId"
)

type ControllerSession struct {
	AgentID   string
	SessionID string
}

func CloneMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		if nested, ok := value.(map[string]any); ok {
			child := make(map[string]any, len(nested))
			for childKey, childValue := range nested {
				child[childKey] = childValue
			}
			out[key] = child
			continue
		}
		out[key] = value
	}
	return out
}

func IsDelegatedChild(meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	root, ok := meta[strings.TrimSpace(metaKeyRoot)].(map[string]any)
	if !ok || len(root) == 0 {
		return false
	}
	return metaBoolValue(root[metaKeyDelegatedChild])
}

func WithDelegatedChild(meta map[string]any, delegated bool) map[string]any {
	out := CloneMeta(meta)
	if out == nil {
		out = map[string]any{}
	}
	root, _ := out[metaKeyRoot].(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	root[metaKeyDelegatedChild] = delegated
	out[metaKeyRoot] = root
	return out
}

func ModelAlias(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	root, ok := meta[strings.TrimSpace(metaKeyRoot)].(map[string]any)
	if !ok || len(root) == 0 {
		return ""
	}
	value, _ := root[metaKeyModelAlias].(string)
	return strings.TrimSpace(value)
}

func WithModelAlias(meta map[string]any, alias string) map[string]any {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return CloneMeta(meta)
	}
	out := CloneMeta(meta)
	if out == nil {
		out = map[string]any{}
	}
	root, _ := out[metaKeyRoot].(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	root[metaKeyModelAlias] = alias
	out[metaKeyRoot] = root
	return out
}

func SessionMetaFromState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		return nil
	}
	meta, _ := acpState[stateKeyMeta].(map[string]any)
	return CloneMeta(meta)
}

func SessionMetaFromStore(ctx context.Context, store session.StateStore, sess *session.Session) (map[string]any, error) {
	if store == nil || sess == nil {
		//nolint:nilnil // Missing store or session simply means ACP metadata is unavailable.
		return nil, nil
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return nil, err
	}
	return SessionMetaFromState(values), nil
}

func ControllerSessionFromState(state map[string]any) ControllerSession {
	if len(state) == 0 {
		return ControllerSession{}
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		return ControllerSession{}
	}
	raw, _ := acpState[stateKeyController].(map[string]any)
	if len(raw) == 0 {
		return ControllerSession{}
	}
	return ControllerSession{
		AgentID:   strings.TrimSpace(stringValue(raw[controllerKeyAgentID])),
		SessionID: strings.TrimSpace(stringValue(raw[controllerKeySession])),
	}
}

func ControllerSessionFromStore(ctx context.Context, store session.StateStore, sess *session.Session) (ControllerSession, error) {
	if store == nil || sess == nil {
		return ControllerSession{}, nil
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return ControllerSession{}, err
	}
	return ControllerSessionFromState(values), nil
}

func StoreSessionMeta(state map[string]any, meta map[string]any) map[string]any {
	if len(state) == 0 {
		state = map[string]any{}
	} else {
		state = maps.Clone(state)
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		acpState = map[string]any{}
	} else {
		acpState = maps.Clone(acpState)
	}
	acpState[stateKeyMeta] = CloneMeta(meta)
	state[stateKeyACP] = acpState
	return state
}

func StoreControllerSession(state map[string]any, ref ControllerSession) map[string]any {
	ref.AgentID = strings.TrimSpace(ref.AgentID)
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	if len(state) == 0 {
		state = map[string]any{}
	} else {
		state = maps.Clone(state)
	}
	acpState, _ := state[stateKeyACP].(map[string]any)
	if len(acpState) == 0 {
		acpState = map[string]any{}
	} else {
		acpState = maps.Clone(acpState)
	}
	if ref.AgentID == "" && ref.SessionID == "" {
		delete(acpState, stateKeyController)
	} else {
		acpState[stateKeyController] = map[string]any{
			controllerKeyAgentID: ref.AgentID,
			controllerKeySession: ref.SessionID,
		}
	}
	if len(acpState) == 0 {
		delete(state, stateKeyACP)
		return state
	}
	state[stateKeyACP] = acpState
	return state
}

func UpdateSessionMeta(ctx context.Context, store session.StateStore, sess *session.Session, update func(map[string]any) map[string]any) error {
	if store == nil || sess == nil || update == nil {
		return nil
	}
	apply := func(values map[string]any) map[string]any {
		return StoreSessionMeta(values, update(SessionMetaFromState(values)))
	}
	if updater, ok := store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sess, func(values map[string]any) (map[string]any, error) {
			return apply(values), nil
		})
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	return store.ReplaceState(ctx, sess, apply(values))
}

func UpdateControllerSession(ctx context.Context, store session.StateStore, sess *session.Session, update func(ControllerSession) ControllerSession) error {
	if store == nil || sess == nil || update == nil {
		return nil
	}
	apply := func(values map[string]any) map[string]any {
		return StoreControllerSession(values, update(ControllerSessionFromState(values)))
	}
	if updater, ok := store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sess, func(values map[string]any) (map[string]any, error) {
			return apply(values), nil
		})
	}
	values, err := store.SnapshotState(ctx, sess)
	if err != nil {
		return err
	}
	return store.ReplaceState(ctx, sess, apply(values))
}

func metaBoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
