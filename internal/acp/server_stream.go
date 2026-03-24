package acp

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type partialContentState struct {
	pending      string
	sent         string
	firstBuf     time.Time
	pendingParts int
	policy       adaptivePartialChunkingPolicy
}

type pendingContentUpdate struct {
	updateType string
	text       string
}

func (s *Server) notifyEvent(sessionID string, ev *session.Event, sess *serverSession) error {
	if ev == nil {
		return nil
	}
	msg := ev.Message
	if sess == nil && eventIsPartial(ev) {
		// Session replay should be authoritative history, not raw transient chunks.
		return nil
	}
	if sess != nil && !eventIsPartial(ev) && msg.Role != model.RoleAssistant {
		if err := s.flushPendingContent(sessionID, sess); err != nil {
			return err
		}
	}
	if eventIsPartial(ev) {
		if err := s.flushPendingContentForChannelSwitch(sessionID, sess, eventChannel(ev)); err != nil {
			return err
		}
		switch eventChannel(ev) {
		case "answer":
			return s.emitBufferedPartial(sessionID, sess, "answer", msg.TextContent())
		case "reasoning":
			return s.emitBufferedPartial(sessionID, sess, "reasoning", msg.ReasoningText())
		}
	}
	if msg.Role == model.RoleUser {
		text := sessionmode.VisibleText(strings.TrimSpace(msg.TextContent()))
		if text == "" {
			return nil
		}
		if sess != nil {
			// ACP clients already know the live prompt they just submitted.
			// Re-emitting it as a session/update duplicates user history on clients
			// like acpx; keep user-message replay only for session/load.
			return nil
		}
		return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
			SessionID: sessionID,
			Update: ContentChunk{
				SessionUpdate: UpdateUserMessage,
				Content:       TextContent{Type: "text", Text: text},
			},
		})
	}
	if msg.Role == model.RoleAssistant {
		if sess == nil {
			if reasoning := msg.ReasoningText(); reasoning != "" {
				if err := s.emitContentUpdate(sessionID, UpdateAgentThought, reasoning); err != nil {
					return err
				}
			}
			if text := msg.TextContent(); text != "" {
				if err := s.emitContentUpdate(sessionID, UpdateAgentMessage, text); err != nil {
					return err
				}
			}
		} else {
			for _, update := range sess.finalizeAssistantContent(msg) {
				if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
					return err
				}
			}
		}
	}
	for _, call := range msg.ToolCalls() {
		args := map[string]any{}
		if raw := strings.TrimSpace(call.Args); raw != "" {
			_ = json.Unmarshal([]byte(raw), &args)
		}
		args = normalizeToolArgsForACP(call.Name, args, s.sessionFS(sessionID))
		if sess != nil {
			sess.rememberToolCall(call.ID, call.Name, args)
		}
		update := ToolCall{
			SessionUpdate: UpdateToolCall,
			ToolCallID:    call.ID,
			Title:         summarizeToolCallTitle(call.Name, args),
			Kind:          toolKindForName(call.Name),
			Status:        ToolStatusPending,
			RawInput:      args,
			Locations:     toolLocations(args, nil),
		}
		if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: update}); err != nil {
			return err
		}
	}
	if resp := msg.ToolResponse(); resp != nil {
		if sess != nil {
			sess.rememberAsyncToolResult(resp.Name, resp.ID, resp.Result)
		}
		status := toolStatusForResult(resp.Name, resp.Result)
		update := ToolCallUpdate{
			SessionUpdate: UpdateToolCallState,
			ToolCallID:    resp.ID,
			Status:        ptr(status),
			RawOutput:     sanitizeToolResultForACP(resp.Result),
			Locations:     toolLocations(nil, resp.Result),
		}
		if content := toolCallContentForResult(resp.Name, resp.Result); len(content) > 0 {
			update.Content = content
		}
		if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: update}); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(resp.Name), tool.PlanToolName) && !hasToolError(resp.Result) {
			entries := planEntriesFromResult(resp.Result)
			if sess != nil {
				sess.setPlan(entries)
			}
			if err := s.notifyPlan(sessionID, entries); err != nil {
				return err
			}
		}
		for _, extra := range supplementalToolCallUpdates(sess, resp) {
			if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: extra}); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (s *Server) notifySessionStreamUpdate(rootSessionID string, update sessionstream.Update) error {
	if update.Event == nil {
		return nil
	}
	sessionID := strings.TrimSpace(update.SessionID)
	if sessionID == "" || sessionID == strings.TrimSpace(rootSessionID) {
		return nil
	}
	var streamSess *serverSession
	if eventIsPartial(update.Event) || update.Event.Message.Role == model.RoleAssistant {
		streamSess = s.liveStreamSession(sessionID)
	}
	if err := s.notifyEvent(sessionID, update.Event, streamSess); err != nil {
		return err
	}
	if info, ok := runtime.LifecycleFromEvent(update.Event); ok {
		switch info.Status {
		case runtime.RunLifecycleStatusCompleted, runtime.RunLifecycleStatusFailed, runtime.RunLifecycleStatusInterrupted:
			s.dropLiveStreamSession(sessionID)
		}
	}
	return nil
}

func (s *Server) liveStreamSession(sessionID string) *serverSession {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveStream == nil {
		s.liveStream = map[string]*serverSession{}
	}
	if sess, ok := s.liveStream[sessionID]; ok && sess != nil {
		return sess
	}
	sess := &serverSession{id: sessionID}
	s.liveStream[sessionID] = sess
	return sess
}

func (s *Server) dropLiveStreamSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.liveStream, sessionID)
}

func (s *Server) emitBufferedPartial(sessionID string, sess *serverSession, channel string, text string) error {
	if sess == nil || text == "" {
		return nil
	}
	for _, update := range sess.enqueuePartialContent(channel, text, time.Now()) {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) flushPendingContent(sessionID string, sess *serverSession) error {
	if sess == nil {
		return nil
	}
	for _, update := range sess.flushPendingContent() {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) flushPendingContentForChannelSwitch(sessionID string, sess *serverSession, nextChannel string) error {
	if sess == nil {
		return nil
	}
	for _, update := range sess.flushPendingContentForChannelSwitch(nextChannel) {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) emitContentUpdate(sessionID string, updateType string, text string) error {
	if text == "" {
		return nil
	}
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: ContentChunk{
			SessionUpdate: updateType,
			Content:       TextContent{Type: "text", Text: text},
		},
	})
}

func eventIsPartial(ev *session.Event) bool {
	return session.IsPartial(ev)
}

func eventChannel(ev *session.Event) string {
	return string(session.PartialChannelOf(ev))
}

func (s *serverSession) resetPartialStreams() {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.answerStream = partialContentState{}
	s.thoughtStream = partialContentState{}
}

func (s *serverSession) enqueuePartialContent(channel string, text string, now time.Time) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	state, updateType := s.partialState(channel)
	if state == nil || text == "" {
		return nil
	}
	state.pending += text
	state.pendingParts++
	if state.firstBuf.IsZero() {
		state.firstBuf = now
	}
	if !shouldFlushPartialState(state, now) {
		return nil
	}
	update := flushPartialState(state, updateType)
	if update == nil {
		return nil
	}
	return []pendingContentUpdate{*update}
}

func (s *serverSession) flushPendingContent() []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	out := make([]pendingContentUpdate, 0, 2)
	if update := flushPartialState(&s.thoughtStream, UpdateAgentThought); update != nil {
		out = append(out, *update)
	}
	if update := flushPartialState(&s.answerStream, UpdateAgentMessage); update != nil {
		out = append(out, *update)
	}
	return out
}

func (s *serverSession) flushPendingContentForChannelSwitch(nextChannel string) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	var out []pendingContentUpdate
	switch strings.TrimSpace(nextChannel) {
	case "answer":
		if update := flushPartialState(&s.thoughtStream, UpdateAgentThought); update != nil {
			out = append(out, *update)
		}
	case "reasoning":
		if update := flushPartialState(&s.answerStream, UpdateAgentMessage); update != nil {
			out = append(out, *update)
		}
	}
	return out
}

func (s *serverSession) finalizeAssistantContent(msg model.Message) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	out := make([]pendingContentUpdate, 0, 2)
	if update := finalizePartialState(&s.thoughtStream, UpdateAgentThought, msg.ReasoningText()); update != nil {
		out = append(out, *update)
	}
	if update := finalizePartialState(&s.answerStream, UpdateAgentMessage, msg.TextContent()); update != nil {
		out = append(out, *update)
	}
	return out
}

func (s *serverSession) partialState(channel string) (*partialContentState, string) {
	switch strings.TrimSpace(channel) {
	case "reasoning":
		return &s.thoughtStream, UpdateAgentThought
	case "answer":
		return &s.answerStream, UpdateAgentMessage
	default:
		return nil, ""
	}
}

func shouldFlushPartialState(state *partialContentState, now time.Time) bool {
	if state == nil || state.pending == "" {
		return false
	}
	snapshot := partialQueueSnapshot{
		queuedParts: state.pendingParts,
	}
	if !state.firstBuf.IsZero() {
		snapshot.oldestAge = now.Sub(state.firstBuf)
	}
	thresholds := state.policy.thresholds(snapshot, now)
	if len(state.pending) >= thresholds.hardLimit {
		return true
	}
	if state.pendingParts >= thresholds.minTimedFlushPart && !state.firstBuf.IsZero() && now.Sub(state.firstBuf) >= thresholds.interval {
		return true
	}
	return len(state.pending) >= thresholds.softLimit && endsPartialFlushBoundary(state.pending)
}

func endsPartialFlushBoundary(text string) bool {
	if text == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	if last == utf8.RuneError {
		return false
	}
	return unicode.IsSpace(last) || unicode.IsPunct(last)
}

func flushPartialState(state *partialContentState, updateType string) *pendingContentUpdate {
	if state == nil || state.pending == "" {
		return nil
	}
	text := state.pending
	state.sent += text
	state.pending = ""
	state.firstBuf = time.Time{}
	state.pendingParts = 0
	return &pendingContentUpdate{updateType: updateType, text: text}
}

func finalizePartialState(state *partialContentState, updateType string, finalText string) *pendingContentUpdate {
	if state == nil {
		return nil
	}
	var text string
	switch {
	case finalText != "" && state.sent == "":
		text = finalText
	case finalText != "" && strings.HasPrefix(finalText, state.sent):
		text = finalText[len(state.sent):]
	case finalText != "" && state.pending != "":
		text = state.pending
	case finalText != "":
		text = ""
	case state.pending != "":
		text = state.pending
	}
	*state = partialContentState{}
	if text == "" {
		return nil
	}
	return &pendingContentUpdate{updateType: updateType, text: text}
}
