package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type stepBoundaryTracker struct {
	pendingToolCalls map[string]int
	pendingUnnamed   int
}

func (t *stepBoundaryTracker) observe(ev *session.Event) (boundary bool, terminal bool) {
	if ev == nil || isEventPartial(ev) {
		return false, false
	}
	msg := ev.Message
	if msg.Role == model.RoleAssistant {
		if len(msg.ToolCalls) == 0 {
			t.reset()
			return true, true
		}
		t.reset()
		t.pendingToolCalls = make(map[string]int, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if id := strings.TrimSpace(call.ID); id != "" {
				t.pendingToolCalls[id]++
				continue
			}
			t.pendingUnnamed++
		}
		return false, false
	}
	if msg.Role != model.RoleTool || msg.ToolResponse == nil {
		return false, false
	}
	if len(t.pendingToolCalls) == 0 && t.pendingUnnamed == 0 {
		return false, false
	}
	if id := strings.TrimSpace(msg.ToolResponse.ID); id != "" {
		if count := t.pendingToolCalls[id]; count > 0 {
			if count == 1 {
				delete(t.pendingToolCalls, id)
			} else {
				t.pendingToolCalls[id] = count - 1
			}
		}
	} else if t.pendingUnnamed > 0 {
		t.pendingUnnamed--
	}
	if len(t.pendingToolCalls) == 0 && t.pendingUnnamed == 0 {
		t.reset()
		return true, false
	}
	return false, false
}

func (t *stepBoundaryTracker) reset() {
	t.pendingToolCalls = nil
	t.pendingUnnamed = 0
}

// loopDetector detects infinite loops where the model produces identical
// outputs across consecutive agent turns. It computes a signature from the
// full turn output (text, reasoning, tool call names AND arguments) so that
// legitimate repeated tool calls with different parameters (e.g. reading
// different lines of the same file) are never flagged.
type loopDetector struct {
	lastSig        string
	consecutiveCnt int
}

const loopDetectorThreshold = 3

// observeTurn records one complete agent turn and returns true when the model
// appears stuck in an infinite loop (loopDetectorThreshold consecutive
// identical turns).
func (d *loopDetector) observeTurn(events []*session.Event) bool {
	if isStableTaskPollingTurn(events) {
		d.reset()
		return false
	}
	sig := turnSignature(events)
	if sig == "" {
		d.reset()
		return false
	}
	if sig == d.lastSig {
		d.consecutiveCnt++
	} else {
		d.lastSig = sig
		d.consecutiveCnt = 1
	}
	return d.consecutiveCnt >= loopDetectorThreshold
}

func (d *loopDetector) reset() {
	d.lastSig = ""
	d.consecutiveCnt = 0
}

func isStableTaskPollingTurn(events []*session.Event) bool {
	if len(events) != 2 {
		return false
	}
	assistant := events[0]
	toolResult := events[1]
	if assistant == nil || toolResult == nil {
		return false
	}
	if assistant.Message.Role != model.RoleAssistant || len(assistant.Message.ToolCalls) != 1 {
		return false
	}
	call := assistant.Message.ToolCalls[0]
	if !strings.EqualFold(strings.TrimSpace(call.Name), "TASK") {
		return false
	}
	args, err := model.ParseToolCallArgs(call.Args)
	if err != nil {
		return false
	}
	action, _ := args["action"].(string)
	if !strings.EqualFold(strings.TrimSpace(action), "wait") {
		return false
	}
	if toolResult.Message.Role != model.RoleTool || toolResult.Message.ToolResponse == nil {
		return false
	}
	resp := toolResult.Message.ToolResponse
	if !strings.EqualFold(strings.TrimSpace(resp.Name), "TASK") {
		return false
	}
	running, _ := resp.Result["running"].(bool)
	return running
}

// turnSignature produces a content-addressable hash of a complete agent turn.
// It includes assistant text, reasoning, and every tool call name + normalized
// arguments, plus tool responses. This ensures that two turns that call the
// same tool with different arguments produce different signatures.
func turnSignature(events []*session.Event) string {
	if len(events) == 0 {
		return ""
	}
	h := sha256.New()
	for _, ev := range events {
		if ev == nil {
			continue
		}
		msg := ev.Message
		if msg.Role == model.RoleAssistant {
			h.Write([]byte("A:"))
			h.Write([]byte(strings.TrimSpace(msg.Text)))
			h.Write([]byte("\x00"))
			h.Write([]byte(strings.TrimSpace(msg.Reasoning)))
			h.Write([]byte("\x00"))
			for _, tc := range msg.ToolCalls {
				h.Write([]byte("TC:"))
				h.Write([]byte(strings.TrimSpace(tc.Name)))
				h.Write([]byte(":"))
				h.Write([]byte(normalizeArgs(tc.Args)))
				h.Write([]byte("\x00"))
			}
		} else if msg.Role == model.RoleTool && msg.ToolResponse != nil {
			h.Write([]byte("TR:"))
			h.Write([]byte(strings.TrimSpace(msg.ToolResponse.Name)))
			h.Write([]byte(":"))
			h.Write([]byte(normalizeResultMap(msg.ToolResponse.Result)))
			h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeArgs canonicalizes JSON tool-call argument text so that key
// ordering does not affect the signature.
func normalizeArgs(raw string) string {
	args, err := model.ParseToolCallArgs(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return normalizeMap(args)
}

func normalizeMap(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte(':')
		sb.WriteString(fmt.Sprintf("%v", m[k]))
	}
	sb.WriteByte('}')
	return sb.String()
}

func normalizeResultMap(m map[string]any) string {
	return normalizeMap(m)
}

var errLoopDetected = errors.New("runtime: agent appears stuck in an infinite loop — the same output was produced multiple consecutive times")
