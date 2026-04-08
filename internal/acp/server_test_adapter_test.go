package acp

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/internal/slashcmd"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

type harnessAdapterConfig struct {
	store         session.Store
	runtime       *runtime.Runtime
	baseRuntime   toolexec.Runtime
	serverConn    *Conn
	workspaceRoot string
	agentFactory  AgentFactory
	modelImpl     model.LLM
}

type harnessAdapter struct {
	t             *testing.T
	cfg           harnessConfig
	store         session.Store
	runtime       *runtime.Runtime
	baseRuntime   toolexec.Runtime
	serverConn    *Conn
	workspaceRoot string
	agentFactory  AgentFactory
	modelImpl     model.LLM
	taskRegistry  *task.Registry
	mu            sync.Mutex
	sessions      map[string]*harnessAdapterSession
}

type harnessAdapterSession struct {
	id        string
	cwd       string
	resources *SessionResources

	stateMu           sync.Mutex
	modeID            string
	configValues      map[string]string
	configOptions     []SessionConfigOption
	availableCommands []AvailableCommand
	planEntries       []PlanEntry

	runMu     sync.Mutex
	runCancel context.CancelFunc
	activeRun sessionsvc.TurnHandle
}

type harnessPromptHandle struct {
	session *harnessAdapterSession
	handle  sessionsvc.TurnHandle
	cancel  context.CancelFunc
	done    func()
	once    sync.Once
}

type staticPromptHandle struct {
	events []*session.Event
}

func newHarnessAdapter(t *testing.T, cfg harnessConfig, adapterCfg harnessAdapterConfig) Adapter {
	t.Helper()
	return &harnessAdapter{
		t:             t,
		cfg:           cfg,
		store:         adapterCfg.store,
		runtime:       adapterCfg.runtime,
		baseRuntime:   adapterCfg.baseRuntime,
		serverConn:    adapterCfg.serverConn,
		workspaceRoot: adapterCfg.workspaceRoot,
		agentFactory:  adapterCfg.agentFactory,
		modelImpl:     adapterCfg.modelImpl,
		taskRegistry:  task.NewRegistry(task.RegistryConfig{}),
		sessions:      map[string]*harnessAdapterSession{},
	}
}

func (a *harnessAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		PromptImage: a.cfg.promptImageEnabled == nil || a.cfg.promptImageEnabled(),
		SessionList: a.cfg.listSessions != nil,
	}
}

func (a *harnessAdapter) NewSession(ctx context.Context, req AdapterNewSessionRequest, caps ClientCapabilities) (AdapterSessionState, error) {
	cwd, err := a.validateCWD(req.CWD)
	if err != nil {
		return AdapterSessionState{}, err
	}
	sessionID := idutil.NewSessionID()
	sess := &harnessAdapterSession{
		id:           sessionID,
		cwd:          cwd,
		modeID:       a.initialModeID(),
		configValues: a.initialConfigValues(),
	}
	a.normalizeConfig(sess)
	res, err := a.newResources(ctx, sessionID, cwd, caps, sess.mode)
	if err != nil {
		return AdapterSessionState{}, err
	}
	sess.resources = res
	if _, err := a.store.GetOrCreate(ctx, &session.Session{AppName: "caelis", UserID: "tester", ID: sessionID}); err != nil {
		return AdapterSessionState{}, err
	}
	a.refreshDerived(sess)
	if err := a.persistState(ctx, sess); err != nil {
		return AdapterSessionState{}, err
	}
	a.storeSession(sess)
	return a.snapshot(sess), nil
}

func (a *harnessAdapter) ListSessions(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
	if a.cfg.listSessions == nil {
		return SessionListResponse{}, fmt.Errorf("session listing is not supported")
	}
	return a.cfg.listSessions(ctx, req)
}

func (a *harnessAdapter) LoadSession(ctx context.Context, req AdapterLoadSessionRequest, caps ClientCapabilities) (LoadedSessionState, error) {
	cwd, err := a.validateCWD(req.CWD)
	if err != nil {
		return LoadedSessionState{}, err
	}
	sessRef := &session.Session{AppName: "caelis", UserID: "tester", ID: strings.TrimSpace(req.SessionID)}
	if existing, ok := a.store.(session.ExistenceStore); ok {
		found, err := existing.SessionExists(ctx, sessRef)
		if err != nil {
			return LoadedSessionState{}, err
		}
		if !found {
			return LoadedSessionState{}, session.ErrSessionNotFound
		}
	}
	runSvc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime: a.runtime,
		Store:   a.store,
		AppName: "caelis",
		UserID:  "tester",
	})
	if err != nil {
		return LoadedSessionState{}, err
	}
	loaded, err := runSvc.LoadSession(ctx, sessionsvc.LoadSessionRequest{
		SessionRef: sessionsvc.SessionRef{AppName: "caelis", UserID: "tester", SessionID: req.SessionID},
		CWD:        cwd,
	})
	if err != nil {
		return LoadedSessionState{}, err
	}
	modeID, values, storedCWD, plans := a.restoreState(loaded.State)
	if storedCWD != "" && storedCWD != cwd {
		return LoadedSessionState{}, fmt.Errorf("cwd %q does not match persisted session cwd %q", cwd, storedCWD)
	}
	if storedCWD != "" {
		cwd = storedCWD
	}
	sess := a.loadedSession(req.SessionID)
	if sess == nil {
		sess = &harnessAdapterSession{id: req.SessionID, cwd: cwd, modeID: modeID, configValues: values, planEntries: plans}
		a.normalizeConfig(sess)
		res, err := a.newResources(ctx, sess.id, sess.cwd, caps, sess.mode)
		if err != nil {
			return LoadedSessionState{}, err
		}
		sess.resources = res
		a.storeSession(sess)
	} else {
		sess.stateMu.Lock()
		sess.modeID = modeID
		sess.configValues = cloneStringMap(values)
		sess.planEntries = append([]PlanEntry(nil), plans...)
		sess.stateMu.Unlock()
	}
	a.refreshDerived(sess)
	return LoadedSessionState{Session: a.snapshot(sess), Events: loaded.Events}, nil
}

func (a *harnessAdapter) SetMode(ctx context.Context, req AdapterSetModeRequest) (AdapterSessionState, error) {
	sess, err := a.session(req.SessionID)
	if err != nil {
		return AdapterSessionState{}, err
	}
	modeID := strings.TrimSpace(req.ModeID)
	if !a.modeExists(modeID) {
		return AdapterSessionState{}, fmt.Errorf("unsupported mode %q", modeID)
	}
	sess.stateMu.Lock()
	sess.modeID = modeID
	if a.hasConfigCategory("mode") {
		if sess.configValues == nil {
			sess.configValues = map[string]string{}
		}
		for _, item := range a.cfg.sessionConfig {
			if strings.TrimSpace(item.Category) == "mode" {
				sess.configValues[item.ID] = modeID
			}
		}
	}
	sess.stateMu.Unlock()
	a.normalizeConfig(sess)
	a.refreshDerived(sess)
	if err := a.persistState(ctx, sess); err != nil {
		return AdapterSessionState{}, err
	}
	return a.snapshot(sess), nil
}

func (a *harnessAdapter) SetConfigOption(ctx context.Context, req AdapterSetConfigOptionRequest) (AdapterSessionState, error) {
	sess, err := a.session(req.SessionID)
	if err != nil {
		return AdapterSessionState{}, err
	}
	id := strings.TrimSpace(req.ConfigID)
	value := strings.TrimSpace(req.Value)
	if !a.configSupports(sess, id, value) {
		return AdapterSessionState{}, fmt.Errorf("unsupported value %q for config option %q", value, id)
	}
	sess.stateMu.Lock()
	if sess.configValues == nil {
		sess.configValues = map[string]string{}
	}
	sess.configValues[id] = value
	if tpl, ok := a.configTemplate(id); ok && strings.TrimSpace(tpl.Category) == "mode" && a.modeExists(value) {
		sess.modeID = value
	}
	sess.stateMu.Unlock()
	a.normalizeConfig(sess)
	a.refreshDerived(sess)
	if err := a.persistState(ctx, sess); err != nil {
		return AdapterSessionState{}, err
	}
	return a.snapshot(sess), nil
}

func (a *harnessAdapter) StartPrompt(ctx context.Context, req StartPromptRequest) (StartPromptResult, error) {
	sess, err := a.session(req.SessionID)
	if err != nil {
		return StartPromptResult{}, err
	}
	if !req.HasImages && len(req.ContentParts) == 0 {
		if inv, ok := slashcmd.Parse(req.InputText); ok && a.hasCommand(sess, inv.Name) {
			return a.handleSlash(ctx, sess, inv)
		}
	}
	ag, err := a.agentFactory(true, sess.cwd, "", sess.agentConfig())
	if err != nil {
		return StartPromptResult{}, err
	}
	llm, err := a.resolveModel(sess.agentConfig())
	if err != nil {
		return StartPromptResult{}, err
	}
	if ctx == nil {
		return StartPromptResult{}, fmt.Errorf("acp harness: context is required")
	}
	runCtx := ctx
	if req.OnSessionStream != nil {
		runCtx = sessionstream.WithStreamer(runCtx, sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
			_ = req.OnSessionStream(update)
		}))
	}
	runInput := sessionmode.Inject(req.InputText, sess.mode())
	runParts := append([]model.ContentPart(nil), req.ContentParts...)
	if req.HasImages {
		if controlText := strings.TrimSpace(sessionmode.Inject("", sess.mode())); controlText != "" {
			runParts = append([]model.ContentPart{{Type: model.ContentPartText, Text: controlText}}, runParts...)
		}
		runInput = ""
	}
	if a.cfg.supportsPromptImage != nil && !a.cfg.supportsPromptImage(sess.agentConfig()) {
		runParts = filterImageContentParts(runParts, false)
	}
	submission := runtime.Submission{Text: runInput, ContentParts: runParts, Mode: runtime.SubmissionConversation}
	if active := sess.activeHandle(); active != nil {
		if submitErr := active.Submit(submission); submitErr == nil {
			return StartPromptResult{StopReason: StopReasonEndTurn}, nil
		} else if !errors.Is(submitErr, runtime.ErrRunnerClosed) {
			return StartPromptResult{}, submitErr
		}
		sess.clearActive()
	}
	runCtx, cancel := context.WithCancel(runCtx)
	runSvc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:               a.runtime,
		Store:                 a.store,
		AppName:               "caelis",
		UserID:                "tester",
		WorkspaceRoot:         a.workspaceRoot,
		WorkspaceCWD:          sess.cwd,
		Execution:             sess.resources.Runtime,
		Tools:                 append([]tool.Tool(nil), sess.resources.Tools...),
		Policies:              append([]policy.Hook(nil), sess.resources.Policies...),
		TaskRegistry:          a.taskRegistry,
		EnablePlan:            true,
		EnableSelfSpawn:       true,
		SubagentRunnerFactory: nil,
	})
	if err != nil {
		cancel()
		return StartPromptResult{}, err
	}
	runResult, err := runSvc.RunTurn(runCtx, sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{AppName: "caelis", UserID: "tester", SessionID: sess.id},
		Input:      runInput, ContentParts: runParts, Agent: ag, Model: llm,
	})
	if err != nil {
		cancel()
		return StartPromptResult{}, err
	}
	sess.setActive(cancel, runResult.Handle)
	return StartPromptResult{Handle: &harnessPromptHandle{
		session: sess,
		handle:  runResult.Handle,
		cancel:  cancel,
		done: func() {
			cancel()
			sess.clearActive()
		},
	}}, nil
}

func (a *harnessAdapter) CancelPrompt(sessionID string) {
	if sess, err := a.session(sessionID); err == nil {
		sess.cancel()
	}
}

func (a *harnessAdapter) SessionFS(sessionID string) toolexec.FileSystem {
	if sess, err := a.session(sessionID); err == nil && sess.resources != nil && sess.resources.Runtime != nil {
		return sess.resources.Runtime.FileSystem()
	}
	return nil
}

func (a *harnessAdapter) handleSlash(ctx context.Context, sess *harnessAdapterSession, inv slashcmd.Invocation) (StartPromptResult, error) {
	switch inv.Name {
	case "help":
		lines := []string{"Available commands:"}
		for _, item := range sess.commands() {
			line := "/" + item.Name
			if hint := strings.TrimSpace(item.Input.Hint); hint != "" {
				line = hint
			}
			if desc := strings.TrimSpace(item.Description); desc != "" {
				line += " - " + desc
			}
			lines = append(lines, line)
		}
		return a.staticResult(ctx, sess.id, strings.Join(lines, "\n"))
	case "status":
		mode := sess.mode()
		if mode == "" {
			mode = "default"
		}
		lines := []string{"Session status:", "mode: " + mode}
		if sess.cwd != "" {
			lines = append(lines, "cwd: "+sess.cwd)
		}
		if modelID := currentModelID(sess.options()); modelID != "" {
			lines = append(lines, "model: "+modelID)
		}
		if entries := sess.plans(); len(entries) > 0 {
			lines = append(lines, fmt.Sprintf("plan items: %d", len(entries)))
		}
		return a.staticResult(ctx, sess.id, strings.Join(lines, "\n"))
	case "compact":
		llm, err := a.resolveModel(sess.agentConfig())
		if err != nil {
			return StartPromptResult{}, err
		}
		ev, err := a.runtime.Compact(ctx, runtime.CompactRequest{
			AppName: "caelis", UserID: "tester", SessionID: sess.id, Model: llm, Note: strings.TrimSpace(strings.Join(inv.Args, " ")),
		})
		if err != nil {
			return StartPromptResult{}, err
		}
		if ev != nil {
			if entries := planEntriesFromResult(ev.Message.ToolResponse().Result); len(entries) > 0 {
				sess.setPlans(entries)
			}
		}
		if ev == nil {
			return a.staticResult(ctx, sess.id, "Compact skipped.")
		}
		return a.staticResult(ctx, sess.id, "Compact completed.")
	default:
		return a.staticResult(ctx, sess.id, fmt.Sprintf("Command /%s is not supported in this session.", inv.Name))
	}
}

func (a *harnessAdapter) staticResult(ctx context.Context, sessionID string, text string) (StartPromptResult, error) {
	ev := &session.Event{
		ID:        fmt.Sprintf("ev_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleAssistant, strings.TrimSpace(text)),
	}
	if err := a.store.AppendEvent(ctx, &session.Session{AppName: "caelis", UserID: "tester", ID: sessionID}, ev); err != nil {
		return StartPromptResult{}, err
	}
	return StartPromptResult{StopReason: StopReasonEndTurn, Handle: &staticPromptHandle{events: []*session.Event{ev}}}, nil
}

func (a *harnessAdapter) snapshot(sess *harnessAdapterSession) AdapterSessionState {
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	return AdapterSessionState{
		SessionID:         sess.id,
		CWD:               sess.cwd,
		ConfigOptions:     append([]SessionConfigOption(nil), sess.configOptions...),
		AvailableCommands: append([]AvailableCommand(nil), sess.availableCommands...),
		PlanEntries:       append([]PlanEntry(nil), sess.planEntries...),
		Modes: &SessionModeState{
			AvailableModes: append([]SessionMode(nil), a.cfg.sessionModes...),
			CurrentModeID:  sess.modeID,
		},
	}
}

func (a *harnessAdapter) refreshDerived(sess *harnessAdapterSession) {
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	cfg := AgentSessionConfig{ModeID: sess.modeID, ConfigValues: cloneStringMap(sess.configValues)}
	if a.cfg.sessionConfigState != nil {
		sess.configOptions = a.cfg.sessionConfigState(cfg, append([]SessionConfigOptionTemplate(nil), a.cfg.sessionConfig...))
	} else {
		out := make([]SessionConfigOption, 0, len(a.cfg.sessionConfig))
		values := cloneStringMap(sess.configValues)
		for _, item := range a.cfg.sessionConfig {
			current := strings.TrimSpace(values[item.ID])
			if current == "" {
				current = strings.TrimSpace(item.DefaultValue)
			}
			out = append(out, SessionConfigOption{
				Type:         "select",
				ID:           item.ID,
				Name:         item.Name,
				Description:  item.Description,
				Category:     item.Category,
				CurrentValue: current,
				Options:      append([]SessionConfigSelectOption(nil), item.Options...),
			})
		}
		sess.configOptions = out
	}
	if a.cfg.availableCommands != nil {
		if cmds := a.cfg.availableCommands(cfg); len(cmds) > 0 {
			sess.availableCommands = append([]AvailableCommand(nil), cmds...)
			return
		}
	}
	cmds := DefaultAvailableCommands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	sess.availableCommands = cmds
}

func (a *harnessAdapter) resolveModel(cfg AgentSessionConfig) (model.LLM, error) {
	if a.cfg.newModel != nil {
		return a.cfg.newModel(cfg)
	}
	return a.modelImpl, nil
}

func (a *harnessAdapter) normalizeConfig(sess *harnessAdapterSession) {
	if a.cfg.normalizeConfig == nil {
		return
	}
	next := a.cfg.normalizeConfig(sess.agentConfig())
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	if strings.TrimSpace(next.ModeID) != "" {
		sess.modeID = next.ModeID
	}
	sess.configValues = cloneStringMap(next.ConfigValues)
}

func (a *harnessAdapter) persistState(ctx context.Context, sess *harnessAdapterSession) error {
	modeID := sess.mode()
	configValues := sess.configSnapshot()
	plans := sess.plans()
	ref := &session.Session{AppName: "caelis", UserID: "tester", ID: sess.id}
	if updater, ok := a.store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, ref, func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			values = sessionmode.StoreSnapshot(values, modeID)
			values["acp"] = map[string]any{"cwd": sess.cwd, "modeId": modeID, "configValues": configValues}
			values["plan"] = map[string]any{"version": 1, "entries": plans}
			return values, nil
		})
	}
	return nil
}

func (a *harnessAdapter) restoreState(state map[string]any) (string, map[string]string, string, []PlanEntry) {
	modeID := sessionmode.LoadSnapshot(state)
	if !a.modeExists(modeID) {
		modeID = a.initialModeID()
	}
	values := a.initialConfigValues()
	raw := anyMap(state["acp"])
	if raw == nil {
		return modeID, values, "", loadPlanEntries(state["plan"])
	}
	cwd, _ := raw["cwd"].(string)
	stored := anyMap(raw["configValues"])
	for _, tpl := range a.cfg.sessionConfig {
		if stored == nil {
			break
		}
		if rawValue, _ := stored[tpl.ID].(string); tpl.supports(rawValue) {
			if values == nil {
				values = map[string]string{}
			}
			values[tpl.ID] = rawValue
		}
	}
	return modeID, values, filepath.Clean(strings.TrimSpace(cwd)), loadPlanEntries(state["plan"])
}

func (a *harnessAdapter) newResources(_ context.Context, sessionID string, sessionCWD string, caps ClientCapabilities, modeResolver func() string) (*SessionResources, error) {
	execRuntime := NewRuntime(a.baseRuntime, a.serverConn, sessionID, a.workspaceRoot, sessionCWD, caps, modeResolver)
	tools := make([]tool.Tool, 0, 1)
	bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: execRuntime})
	if err != nil {
		return nil, err
	}
	tools = append(tools, bashTool)
	res := &SessionResources{
		Runtime: execRuntime,
		Tools:   tools,
		Policies: []policy.Hook{
			policy.DefaultSecurityBaseline(),
		},
	}
	if a.cfg.customizeResources != nil {
		if err := a.cfg.customizeResources(res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (a *harnessAdapter) validateCWD(cwd string) (string, error) {
	value := filepath.Clean(strings.TrimSpace(cwd))
	if value == "" {
		return "", fmt.Errorf("cwd is required")
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("cwd %q must be an absolute path", value)
	}
	if !strings.HasPrefix(value, a.workspaceRoot) {
		return "", fmt.Errorf("cwd %q is outside workspace root %q", value, a.workspaceRoot)
	}
	return value, nil
}

func (a *harnessAdapter) initialModeID() string {
	if a.modeExists(a.cfg.defaultModeID) {
		return a.cfg.defaultModeID
	}
	return ""
}

func (a *harnessAdapter) initialConfigValues() map[string]string {
	if len(a.cfg.sessionConfig) == 0 {
		return nil
	}
	values := map[string]string{}
	for _, item := range a.cfg.sessionConfig {
		values[item.ID] = strings.TrimSpace(item.DefaultValue)
	}
	return values
}

func (a *harnessAdapter) modeExists(modeID string) bool {
	for _, one := range a.cfg.sessionModes {
		if strings.TrimSpace(one.ID) == strings.TrimSpace(modeID) {
			return true
		}
	}
	return false
}

func (a *harnessAdapter) hasConfigCategory(category string) bool {
	for _, item := range a.cfg.sessionConfig {
		if strings.TrimSpace(item.Category) == strings.TrimSpace(category) {
			return true
		}
	}
	return false
}

func (a *harnessAdapter) configTemplate(id string) (SessionConfigOptionTemplate, bool) {
	for _, item := range a.cfg.sessionConfig {
		if strings.TrimSpace(item.ID) == strings.TrimSpace(id) {
			return item, true
		}
	}
	return SessionConfigOptionTemplate{}, false
}

func (a *harnessAdapter) configSupports(sess *harnessAdapterSession, id string, value string) bool {
	for _, item := range sess.options() {
		if strings.TrimSpace(item.ID) != strings.TrimSpace(id) {
			continue
		}
		for _, option := range item.Options {
			if strings.TrimSpace(option.Value) == strings.TrimSpace(value) {
				return true
			}
		}
	}
	return false
}

func (a *harnessAdapter) hasCommand(sess *harnessAdapterSession, name string) bool {
	reg := slashcmd.New(slashDefinitions(sess.commands())...)
	return reg.Has(name)
}

func (a *harnessAdapter) storeSession(sess *harnessAdapterSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[strings.TrimSpace(sess.id)] = sess
}

func (a *harnessAdapter) loadedSession(id string) *harnessAdapterSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[strings.TrimSpace(id)]
}

func (a *harnessAdapter) session(id string) (*harnessAdapterSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess, ok := a.sessions[strings.TrimSpace(id)]
	if !ok || sess == nil {
		return nil, fmt.Errorf("unknown session %q", id)
	}
	return sess, nil
}

func (s *harnessAdapterSession) mode() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.modeID)
}

func (s *harnessAdapterSession) agentConfig() AgentSessionConfig {
	return AgentSessionConfig{ModeID: s.mode(), ConfigValues: s.configSnapshot()}
}

func (s *harnessAdapterSession) configSnapshot() map[string]string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return cloneStringMap(s.configValues)
}

func (s *harnessAdapterSession) options() []SessionConfigOption {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]SessionConfigOption(nil), s.configOptions...)
}

func (s *harnessAdapterSession) commands() []AvailableCommand {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]AvailableCommand(nil), s.availableCommands...)
}

func (s *harnessAdapterSession) plans() []PlanEntry {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]PlanEntry(nil), s.planEntries...)
}

func (s *harnessAdapterSession) setPlans(entries []PlanEntry) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.planEntries = append([]PlanEntry(nil), entries...)
}

func (s *harnessAdapterSession) activeHandle() sessionsvc.TurnHandle {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeRun
}

func (s *harnessAdapterSession) setActive(cancel context.CancelFunc, handle sessionsvc.TurnHandle) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.runCancel = cancel
	s.activeRun = handle
}

func (s *harnessAdapterSession) clearActive() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.runCancel = nil
	s.activeRun = nil
}

func (s *harnessAdapterSession) cancel() {
	s.runMu.Lock()
	handle := s.activeRun
	cancel := s.runCancel
	s.runMu.Unlock()
	if handle != nil && handle.Cancel() {
		return
	}
	if cancel != nil {
		cancel()
	}
}

func (h *harnessPromptHandle) Events() iter.Seq2[*session.Event, error] {
	seq := h.handle.Events()
	return func(yield func(*session.Event, error) bool) {
		defer h.finish()
		for ev, err := range seq {
			if ev != nil && ev.Message.ToolResponse() != nil && strings.EqualFold(strings.TrimSpace(ev.Message.ToolResponse().Name), tool.PlanToolName) {
				if entries := planEntriesFromResult(ev.Message.ToolResponse().Result); len(entries) > 0 {
					h.session.setPlans(entries)
				}
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (h *harnessPromptHandle) Close() error {
	err := h.handle.Close()
	h.finish()
	return err
}

func (h *harnessPromptHandle) finish() {
	h.once.Do(func() {
		if h.done != nil {
			h.done()
		}
	})
}

func (h *staticPromptHandle) Events() iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for _, ev := range h.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (h *staticPromptHandle) Close() error { return nil }

func slashDefinitions(cmds []AvailableCommand) []slashcmd.Definition {
	defs := make([]slashcmd.Definition, 0, len(cmds))
	for _, item := range cmds {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		hint := strings.TrimSpace(item.Input.Hint)
		if hint == "" {
			hint = "/" + name
		}
		defs = append(defs, slashcmd.Definition{Name: name, Description: item.Description, InputHint: hint})
	}
	return defs
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}
