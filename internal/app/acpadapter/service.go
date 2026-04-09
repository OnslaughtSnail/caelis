package acpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/internal/slashcmd"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/sessionsvc"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
)

type Config struct {
	Runtime               *runtime.Runtime
	Store                 session.Store
	Model                 model.LLM
	NewModel              internalacp.ModelFactory
	AppName               string
	UserID                string
	DefaultAgent          string
	WorkspaceRoot         string
	SessionModes          []internalacp.SessionMode
	DefaultModeID         string
	SessionConfig         []internalacp.SessionConfigOptionTemplate
	BuildSystemPrompt     internalacp.PromptFactory
	NewSessionResources   internalacp.SessionResourceFactory
	NewAgent              internalacp.AgentFactory
	ListSessions          internalacp.SessionListFactory
	AvailableCommands     internalacp.AvailableCommandsFactory
	SessionConfigState    internalacp.SessionConfigStateFactory
	NormalizeConfig       internalacp.SessionConfigNormalizer
	SupportsPromptImage   func(internalacp.AgentSessionConfig) bool
	PromptImageEnabled    func() bool
	TaskRegistry          *task.Registry
	EnablePlan            bool
	EnableSelfSpawn       bool
	SubagentRunnerFactory runtime.SubagentRunnerFactory
}

type Service struct {
	runtime                *runtime.Runtime
	store                  session.Store
	model                  model.LLM
	newModel               internalacp.ModelFactory
	appName                string
	userID                 string
	defaultAgent           string
	workspaceRoot          string
	sessionModes           []internalacp.SessionMode
	defaultModeID          string
	sessionConfig          []internalacp.SessionConfigOptionTemplate
	buildSystemPrompt      internalacp.PromptFactory
	sessionResourceFactory internalacp.SessionResourceFactory
	newAgent               internalacp.AgentFactory
	listSessions           internalacp.SessionListFactory
	availableCommands      internalacp.AvailableCommandsFactory
	sessionConfigState     internalacp.SessionConfigStateFactory
	normalizeConfig        internalacp.SessionConfigNormalizer
	supportsPromptImageFn  func(internalacp.AgentSessionConfig) bool
	promptImageEnabledFn   func() bool
	taskRegistry           *task.Registry
	enablePlan             bool
	enableSelfSpawn        bool
	subagentRunnerFactory  runtime.SubagentRunnerFactory

	mu       sync.Mutex
	sessions map[string]*managedSession
}

type managedSession struct {
	id        string
	cwd       string
	resources *internalacp.SessionResources

	stateMu           sync.Mutex
	modeID            string
	configValues      map[string]string
	meta              map[string]any
	configOptions     []internalacp.SessionConfigOption
	availableCommands []internalacp.AvailableCommand
	planEntries       []internalacp.PlanEntry
	promptText        string

	runMu     sync.Mutex
	runCancel context.CancelFunc
	activeRun sessionsvc.TurnHandle
}

type promptHandle struct {
	session *managedSession
	handle  sessionsvc.TurnHandle
	cancel  context.CancelFunc
	done    func()
	once    sync.Once
}

type staticPromptHandle struct {
	events []*session.Event
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("acpadapter: store is required")
	}
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("acpadapter: runtime is required")
	}
	if strings.TrimSpace(cfg.AppName) == "" || strings.TrimSpace(cfg.UserID) == "" {
		return nil, fmt.Errorf("acpadapter: app_name and user_id are required")
	}
	if cfg.Model == nil && cfg.NewModel == nil {
		return nil, fmt.Errorf("acpadapter: model is required")
	}
	if cfg.NewSessionResources == nil {
		return nil, fmt.Errorf("acpadapter: session resource factory is required")
	}
	if cfg.NewAgent == nil {
		return nil, fmt.Errorf("acpadapter: agent factory is required")
	}
	workspaceRoot := filepath.Clean(strings.TrimSpace(cfg.WorkspaceRoot))
	if workspaceRoot == "" {
		return nil, fmt.Errorf("acpadapter: workspace root is required")
	}
	return &Service{
		runtime:                cfg.Runtime,
		store:                  cfg.Store,
		model:                  cfg.Model,
		newModel:               cfg.NewModel,
		appName:                strings.TrimSpace(cfg.AppName),
		userID:                 strings.TrimSpace(cfg.UserID),
		defaultAgent:           strings.TrimSpace(cfg.DefaultAgent),
		workspaceRoot:          workspaceRoot,
		sessionModes:           append([]internalacp.SessionMode(nil), cfg.SessionModes...),
		defaultModeID:          strings.TrimSpace(cfg.DefaultModeID),
		sessionConfig:          append([]internalacp.SessionConfigOptionTemplate(nil), cfg.SessionConfig...),
		buildSystemPrompt:      cfg.BuildSystemPrompt,
		sessionResourceFactory: cfg.NewSessionResources,
		newAgent:               cfg.NewAgent,
		listSessions:           cfg.ListSessions,
		availableCommands:      cfg.AvailableCommands,
		sessionConfigState:     cfg.SessionConfigState,
		normalizeConfig:        cfg.NormalizeConfig,
		supportsPromptImageFn:  cfg.SupportsPromptImage,
		promptImageEnabledFn:   cfg.PromptImageEnabled,
		taskRegistry:           cfg.TaskRegistry,
		enablePlan:             cfg.EnablePlan,
		enableSelfSpawn:        cfg.EnableSelfSpawn,
		subagentRunnerFactory:  cfg.SubagentRunnerFactory,
		sessions:               map[string]*managedSession{},
	}, nil
}

func (s *Service) Capabilities() internalacp.AdapterCapabilities {
	return internalacp.AdapterCapabilities{
		PromptImage: s.promptImageEnabled(),
		SessionList: s.listSessions != nil,
	}
}

func (s *Service) NewSession(ctx context.Context, req internalacp.AdapterNewSessionRequest, caps internalacp.ClientCapabilities) (internalacp.AdapterSessionState, error) {
	if ctx == nil {
		return internalacp.AdapterSessionState{}, fmt.Errorf("acpadapter: context is required")
	}
	cwd, err := s.validateSessionCWD(req.CWD)
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = idutil.NewSessionID()
	}
	sess := &managedSession{
		id:           sessionID,
		cwd:          cwd,
		modeID:       s.initialModeID(),
		configValues: s.initialConfigValues(),
		meta:         internalacp.CloneMeta(req.Meta),
	}
	s.applyMetaModelAlias(sess)
	s.normalizeSessionConfig(sess)
	resources, err := s.newSessionResources(ctx, sessionID, sess.cwd, caps, sess.mode)
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	sess.resources = resources
	baseSvc, err := s.baseSessionService(nil)
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	info, err := baseSvc.StartSession(ctx, sessionsvc.StartSessionRequest{
		AppName:            s.appName,
		UserID:             s.userID,
		PreferredSessionID: sessionID,
		Workspace:          sessionsvc.WorkspaceRef{CWD: sess.cwd},
	})
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	sess.id = info.SessionID
	s.refreshDerivedState(sess)
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	s.storeSession(sess)
	return s.snapshot(sess), nil
}

func (s *Service) ListSessions(ctx context.Context, req internalacp.SessionListRequest) (internalacp.SessionListResponse, error) {
	if s.listSessions == nil {
		return internalacp.SessionListResponse{}, fmt.Errorf("session listing is not supported")
	}
	return s.listSessions(ctx, req)
}

func (s *Service) LoadSession(ctx context.Context, req internalacp.AdapterLoadSessionRequest, caps internalacp.ClientCapabilities) (internalacp.LoadedSessionState, error) {
	if ctx == nil {
		return internalacp.LoadedSessionState{}, fmt.Errorf("acpadapter: context is required")
	}
	cwd, err := s.validateSessionCWD(req.CWD)
	if err != nil {
		return internalacp.LoadedSessionState{}, err
	}
	sessRef := s.sessionRef(req.SessionID)
	if err := s.ensureSessionExists(ctx, sessRef); err != nil {
		return internalacp.LoadedSessionState{}, err
	}
	loaded, state, err := s.loadSessionState(ctx, strings.TrimSpace(req.SessionID), cwd, caps, req.Meta)
	if err != nil {
		return internalacp.LoadedSessionState{}, err
	}
	if len(req.Meta) > 0 {
		if err := s.persistSessionState(ctx, s.sessionRef(loaded.id), loaded); err != nil {
			return internalacp.LoadedSessionState{}, err
		}
	}
	return internalacp.LoadedSessionState{
		Session: s.snapshot(loaded),
		Events:  state,
	}, nil
}

func (s *Service) SetMode(ctx context.Context, req internalacp.AdapterSetModeRequest) (internalacp.AdapterSessionState, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	modeID := strings.TrimSpace(req.ModeID)
	if modeID == "" {
		return internalacp.AdapterSessionState{}, fmt.Errorf("modeId is required")
	}
	if !s.modeExists(modeID) {
		return internalacp.AdapterSessionState{}, fmt.Errorf("unsupported mode %q", modeID)
	}
	sess.stateMu.Lock()
	sess.modeID = modeID
	if s.hasConfigCategory("mode") {
		if sess.configValues == nil {
			sess.configValues = map[string]string{}
		}
		for _, item := range s.sessionConfig {
			if strings.TrimSpace(item.Category) == "mode" {
				sess.configValues[item.ID] = modeID
			}
		}
	}
	sess.stateMu.Unlock()
	s.normalizeSessionConfig(sess)
	s.refreshDerivedState(sess)
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	return s.snapshot(sess), nil
}

func (s *Service) SetConfigOption(ctx context.Context, req internalacp.AdapterSetConfigOptionRequest) (internalacp.AdapterSessionState, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	configID := strings.TrimSpace(req.ConfigID)
	if configID == "" {
		return internalacp.AdapterSessionState{}, fmt.Errorf("configId is required")
	}
	value := strings.TrimSpace(req.Value)
	if !s.configOptionSupports(sess, configID, value) {
		if _, ok := s.configTemplate(configID); !ok {
			return internalacp.AdapterSessionState{}, fmt.Errorf("unsupported config option %q", configID)
		}
		return internalacp.AdapterSessionState{}, fmt.Errorf("unsupported value %q for config option %q", value, configID)
	}
	template, _ := s.configTemplate(configID)
	sess.stateMu.Lock()
	if sess.configValues == nil {
		sess.configValues = map[string]string{}
	}
	sess.configValues[configID] = value
	if strings.TrimSpace(template.Category) == "mode" && s.modeExists(value) {
		sess.modeID = value
	}
	sess.stateMu.Unlock()
	s.normalizeSessionConfig(sess)
	s.refreshDerivedState(sess)
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return internalacp.AdapterSessionState{}, err
	}
	return s.snapshot(sess), nil
}

func (s *Service) StartPrompt(ctx context.Context, req internalacp.StartPromptRequest) (internalacp.StartPromptResult, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	runCtx := ctx
	if runCtx == nil {
		return internalacp.StartPromptResult{}, fmt.Errorf("acpadapter: context is required")
	}
	if mergeSessionMeta(sess, req.Meta) {
		if err := s.persistSessionState(runCtx, s.sessionRef(sess.id), sess); err != nil {
			return internalacp.StartPromptResult{}, err
		}
	}
	if !req.HasImages && len(req.ContentParts) == 0 {
		if inv, ok := slashcmd.Parse(req.InputText); ok && s.hasAvailableCommand(sess, inv.Name) {
			return s.handleSlashCommand(ctx, sess, inv)
		}
	}
	systemPrompt, err := s.ensurePromptSnapshot(runCtx, sess)
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	ag, err := s.newAgent(true, sess.cwd, systemPrompt, sess.agentConfig())
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	llm, err := s.resolveModel(sess.agentConfig())
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	if req.OnSessionStream != nil {
		runCtx = sessionstream.WithStreamer(runCtx, sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
			_ = req.OnSessionStream(update)
		}))
	}
	runInput := sessionmode.Inject(req.InputText, sess.mode())
	runParts := append([]model.ContentPart(nil), req.ContentParts...)
	if req.HasImages {
		if controlText := strings.TrimSpace(sessionmode.Inject("", sess.mode())); controlText != "" {
			runParts = append([]model.ContentPart{{
				Type: model.ContentPartText,
				Text: controlText,
			}}, runParts...)
		}
		runInput = ""
	}
	if !s.supportsPromptImage(sess.agentConfig()) {
		runParts = filterImageContentParts(runParts, false)
	}
	submission := runtime.Submission{
		Text:         runInput,
		ContentParts: runParts,
		Mode:         runtime.SubmissionConversation,
	}
	if active := sess.activeHandle(); active != nil {
		if submitErr := active.Submit(submission); submitErr == nil {
			return internalacp.StartPromptResult{StopReason: internalacp.StopReasonEndTurn}, nil
		} else if !errors.Is(submitErr, runtime.ErrRunnerClosed) {
			return internalacp.StartPromptResult{}, submitErr
		}
		sess.clearActiveRun()
	}
	runCtx, cancel := context.WithCancel(runCtx)
	runSvc, err := s.sessionService(sess)
	if err != nil {
		cancel()
		return internalacp.StartPromptResult{}, err
	}
	runResult, err := runSvc.RunTurn(runCtx, sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:   s.appName,
			UserID:    s.userID,
			SessionID: sess.id,
		},
		Input:        runInput,
		ContentParts: runParts,
		Agent:        ag,
		Model:        llm,
	})
	if err != nil {
		cancel()
		return internalacp.StartPromptResult{}, err
	}
	handle := &promptHandle{
		session: sess,
		handle:  runResult.Handle,
		cancel:  cancel,
		done: func() {
			cancel()
			sess.clearActiveRun()
		},
	}
	sess.setActiveRun(cancel, runResult.Handle)
	return internalacp.StartPromptResult{Handle: handle}, nil
}

func (s *Service) CancelPrompt(sessionID string) {
	sess, err := s.session(sessionID)
	if err != nil {
		return
	}
	sess.cancelActiveRun()
}

func (s *Service) SessionFS(sessionID string) toolexec.FileSystem {
	sess, err := s.session(sessionID)
	if err != nil || sess == nil || sess.resources == nil || sess.resources.Runtime == nil {
		return nil
	}
	return sess.resources.Runtime.FileSystem()
}

func (s *Service) handleSlashCommand(ctx context.Context, sess *managedSession, inv slashcmd.Invocation) (internalacp.StartPromptResult, error) {
	switch inv.Name {
	case "help":
		lines := make([]string, 0, 1+len(sess.availableCommandsSnapshot()))
		lines = append(lines, "Available commands:")
		for _, item := range sess.availableCommandsSnapshot() {
			line := "/" + item.Name
			if hint := strings.TrimSpace(item.Input.Hint); hint != "" {
				line = hint
			}
			if desc := strings.TrimSpace(item.Description); desc != "" {
				line += " - " + desc
			}
			lines = append(lines, line)
		}
		return s.staticAssistantResult(ctx, sess, strings.Join(lines, "\n"))
	case "status":
		return s.staticAssistantResult(ctx, sess, s.formatSlashStatus(sess))
	case "compact":
		return s.handleSlashCompact(ctx, sess, strings.TrimSpace(strings.Join(inv.Args, " ")))
	default:
		return s.staticAssistantResult(ctx, sess, fmt.Sprintf("Command /%s is not supported in this session.", inv.Name))
	}
}

func (s *Service) staticAssistantResult(ctx context.Context, sess *managedSession, text string) (internalacp.StartPromptResult, error) {
	ev, err := s.appendAssistantText(ctx, sess.id, text)
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	return internalacp.StartPromptResult{
		StopReason: internalacp.StopReasonEndTurn,
		Handle:     &staticPromptHandle{events: []*session.Event{ev}},
	}, nil
}

func (s *Service) handleSlashCompact(ctx context.Context, sess *managedSession, note string) (internalacp.StartPromptResult, error) {
	llm, err := s.resolveModel(sess.agentConfig())
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	ev, err := s.runtime.Compact(ctx, runtime.CompactRequest{
		AppName:   s.appName,
		UserID:    s.userID,
		SessionID: sess.id,
		Model:     llm,
		Note:      note,
	})
	if err != nil {
		return internalacp.StartPromptResult{}, err
	}
	if ev != nil {
		if entries := planEntriesFromStateEvent(ev); len(entries) > 0 {
			sess.setPlan(entries)
		}
	}
	if ev == nil {
		return s.staticAssistantResult(ctx, sess, "Compact skipped.")
	}
	return s.staticAssistantResult(ctx, sess, "Compact completed.")
}

func (s *Service) appendAssistantText(ctx context.Context, sessionID string, text string) (*session.Event, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("acpadapter: assistant text is empty")
	}
	ev := &session.Event{
		ID:        fmt.Sprintf("ev_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleAssistant, text),
	}
	if err := s.store.AppendEvent(ctx, s.sessionRef(sessionID), ev); err != nil {
		return nil, err
	}
	return ev, nil
}

func (s *Service) loadSessionState(ctx context.Context, sessionID string, resolvedCWD string, caps internalacp.ClientCapabilities, reqMeta map[string]any) (*managedSession, []*session.Event, error) {
	loadFrom, err := s.baseSessionService(nil)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := loadFrom.LoadSession(ctx, sessionsvc.LoadSessionRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:   s.appName,
			UserID:    s.userID,
			SessionID: sessionID,
		},
		CWD:              resolvedCWD,
		IncludeLifecycle: false,
	})
	if err != nil {
		return nil, nil, err
	}
	modeID, configValues, storedCWD, planEntries, meta := s.restoreSessionState(loaded.State)
	if storedCWD != "" && storedCWD != resolvedCWD {
		return nil, nil, fmt.Errorf("cwd %q does not match persisted session cwd %q", resolvedCWD, storedCWD)
	}
	if storedCWD != "" {
		resolvedCWD = storedCWD
	}
	meta = mergeACPRequestMeta(meta, reqMeta)
	if existing := s.loadedSession(sessionID); existing != nil {
		if existing.cwd != resolvedCWD {
			return nil, nil, fmt.Errorf("session %q is already loaded with cwd %q", sessionID, existing.cwd)
		}
		existing.setState(modeID, configValues, planEntries, meta)
		s.normalizeSessionConfig(existing)
		s.refreshDerivedState(existing)
		return existing, loaded.Events, nil
	}
	sess := &managedSession{
		id:           sessionID,
		cwd:          resolvedCWD,
		modeID:       modeID,
		configValues: configValues,
		meta:         internalacp.CloneMeta(meta),
		planEntries:  append([]internalacp.PlanEntry(nil), planEntries...),
	}
	s.applyMetaModelAlias(sess)
	s.normalizeSessionConfig(sess)
	resources, err := s.newSessionResources(ctx, sessionID, sess.cwd, caps, sess.mode)
	if err != nil {
		return nil, nil, err
	}
	sess.resources = resources
	s.refreshDerivedState(sess)
	s.storeSession(sess)
	return sess, loaded.Events, nil
}

func (s *Service) sessionService(sess *managedSession) (*sessionsvc.Service, error) {
	cfg := sessionsvc.ServiceConfig{
		Runtime:               s.runtime,
		Store:                 s.store,
		AppName:               s.appName,
		UserID:                s.userID,
		DefaultAgent:          s.defaultAgent,
		WorkspaceRoot:         s.workspaceRoot,
		TaskRegistry:          s.taskRegistry,
		EnablePlan:            s.enablePlan,
		EnableSelfSpawn:       s.enableSelfSpawnForSession(sess),
		SubagentRunnerFactory: s.subagentRunnerFactory,
	}
	if sess != nil && sess.resources != nil {
		cfg.Execution = sess.resources.Runtime
		cfg.Tools = append([]tool.Tool(nil), sess.resources.Tools...)
		cfg.Policies = append([]policy.Hook(nil), sess.resources.Policies...)
		cfg.WorkspaceCWD = sess.cwd
	}
	service, err := sessionsvc.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("acpadapter: session service: %w", err)
	}
	return service, nil
}

func (s *Service) enableSelfSpawnForSession(sess *managedSession) bool {
	if s == nil || !s.enableSelfSpawn {
		return false
	}
	if sess == nil {
		return true
	}
	return !internalacp.IsDelegatedChild(sess.metaSnapshot())
}

func (s *Service) baseSessionService(sess *managedSession) (*sessionsvc.Service, error) {
	return s.sessionService(sess)
}

func (s *Service) resolveModel(cfg internalacp.AgentSessionConfig) (model.LLM, error) {
	if s.newModel != nil {
		return s.newModel(cfg)
	}
	if s.model == nil {
		return nil, fmt.Errorf("acpadapter: model is not configured")
	}
	return s.model, nil
}

func (s *Service) supportsPromptImage(cfg internalacp.AgentSessionConfig) bool {
	if s.supportsPromptImageFn == nil {
		return false
	}
	return s.supportsPromptImageFn(cfg)
}

func (s *Service) promptImageEnabled() bool {
	if s.promptImageEnabledFn == nil {
		return false
	}
	return s.promptImageEnabledFn()
}

func (s *Service) snapshot(sess *managedSession) internalacp.AdapterSessionState {
	if sess == nil {
		return internalacp.AdapterSessionState{}
	}
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	return internalacp.AdapterSessionState{
		SessionID:     sess.id,
		CWD:           sess.cwd,
		ConfigOptions: append([]internalacp.SessionConfigOption(nil), sess.configOptions...),
		Modes: &internalacp.SessionModeState{
			AvailableModes: append([]internalacp.SessionMode(nil), s.sessionModes...),
			CurrentModeID:  strings.TrimSpace(sess.modeID),
		},
		AvailableCommands: append([]internalacp.AvailableCommand(nil), sess.availableCommands...),
		PlanEntries:       append([]internalacp.PlanEntry(nil), sess.planEntries...),
	}
}

func (s *Service) refreshDerivedState(sess *managedSession) {
	if sess == nil {
		return
	}
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	sess.configOptions = s.sessionConfigOptionsLocked(sess)
	sess.availableCommands = s.availableCommandsLocked(sess)
}

func (s *Service) sessionConfigOptionsLocked(sess *managedSession) []internalacp.SessionConfigOption {
	if sess == nil || len(s.sessionConfig) == 0 {
		return nil
	}
	cfg := internalacp.AgentSessionConfig{
		ModeID:       strings.TrimSpace(sess.modeID),
		ConfigValues: cloneStringMap(sess.configValues),
	}
	if s.sessionConfigState != nil {
		return s.sessionConfigState(cfg, append([]internalacp.SessionConfigOptionTemplate(nil), s.sessionConfig...))
	}
	values := cloneStringMap(sess.configValues)
	out := make([]internalacp.SessionConfigOption, 0, len(s.sessionConfig))
	for _, item := range s.sessionConfig {
		current := strings.TrimSpace(values[item.ID])
		if current == "" {
			current = strings.TrimSpace(item.DefaultValue)
		}
		out = append(out, internalacp.SessionConfigOption{
			Type:         "select",
			ID:           item.ID,
			Name:         item.Name,
			Description:  item.Description,
			Category:     item.Category,
			CurrentValue: current,
			Options:      append([]internalacp.SessionConfigSelectOption(nil), item.Options...),
		})
	}
	return out
}

func (s *Service) availableCommandsLocked(sess *managedSession) []internalacp.AvailableCommand {
	if sess == nil {
		return internalacp.DefaultAvailableCommands()
	}
	if s.availableCommands != nil {
		cmds := s.availableCommands(internalacp.AgentSessionConfig{
			ModeID:       strings.TrimSpace(sess.modeID),
			ConfigValues: cloneStringMap(sess.configValues),
		})
		if len(cmds) > 0 {
			return append([]internalacp.AvailableCommand(nil), cmds...)
		}
	}
	cmds := internalacp.DefaultAvailableCommands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	return cmds
}

func (s *Service) normalizeSessionConfig(sess *managedSession) {
	if sess == nil || s.normalizeConfig == nil {
		return
	}
	next := s.normalizeConfig(sess.agentConfig())
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	if strings.TrimSpace(next.ModeID) != "" && s.modeExists(next.ModeID) {
		sess.modeID = strings.TrimSpace(next.ModeID)
	}
	sess.configValues = cloneStringMap(next.ConfigValues)
}

func (s *Service) applyMetaModelAlias(sess *managedSession) {
	if sess == nil {
		return
	}
	alias := coreacpmeta.ModelAlias(sess.metaSnapshot())
	if alias == "" {
		return
	}
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	if sess.configValues == nil {
		sess.configValues = map[string]string{}
	}
	if strings.TrimSpace(sess.configValues["model"]) == "" {
		sess.configValues["model"] = alias
	}
}

func (s *Service) persistSessionState(ctx context.Context, sessRef *session.Session, sess *managedSession) error {
	if sessRef == nil || sess == nil {
		return nil
	}
	modeID := sess.mode()
	configValues := sess.configSnapshot()
	meta := sess.metaSnapshot()
	planEntries := sess.planSnapshot()
	if updater, ok := s.store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sessRef, func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			values = sessionmode.StoreSnapshot(values, modeID)
			values["acp"] = map[string]any{
				"cwd":          sess.cwd,
				"modeId":       modeID,
				"configValues": configValues,
				"meta":         meta,
			}
			values["plan"] = map[string]any{
				"version": 1,
				"entries": planEntries,
			}
			return values, nil
		})
	}
	values, err := s.store.SnapshotState(ctx, sessRef)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values = sessionmode.StoreSnapshot(values, modeID)
	values["acp"] = map[string]any{
		"cwd":          sess.cwd,
		"modeId":       modeID,
		"configValues": configValues,
		"meta":         meta,
	}
	values["plan"] = map[string]any{
		"version": 1,
		"entries": planEntries,
	}
	return s.store.ReplaceState(ctx, sessRef, values)
}

func (s *Service) ensurePromptSnapshot(_ context.Context, sess *managedSession) (string, error) {
	if sess == nil {
		return "", fmt.Errorf("acpadapter: session is required")
	}
	sess.stateMu.Lock()
	if frozen := strings.TrimSpace(sess.promptText); frozen != "" {
		sess.stateMu.Unlock()
		return frozen, nil
	}
	sess.stateMu.Unlock()
	if s.buildSystemPrompt == nil {
		return "", fmt.Errorf("acpadapter: system prompt factory is required")
	}
	promptText, err := s.buildSystemPrompt(sess.cwd)
	if err != nil {
		return "", err
	}
	promptText = s.adjustSystemPrompt(sess, promptText)
	promptText = strings.TrimSpace(promptText)
	if promptText == "" {
		return "", fmt.Errorf("acpadapter: system prompt is empty")
	}
	sess.stateMu.Lock()
	if frozen := strings.TrimSpace(sess.promptText); frozen != "" {
		sess.stateMu.Unlock()
		return frozen, nil
	}
	sess.promptText = promptText
	sess.stateMu.Unlock()
	return promptText, nil
}

func (s *Service) adjustSystemPrompt(sess *managedSession, promptText string) string {
	if sess == nil || !internalacp.IsDelegatedChild(sess.metaSnapshot()) {
		return strings.TrimSpace(promptText)
	}
	promptText = stripMarkdownSection(promptText, "## Agent Delegation")
	promptText = stripPromptLines(promptText, func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.Contains(trimmed, "SPAWN for delegated child sessions") ||
			strings.Contains(trimmed, "use child sessions for bounded side work or specialization")
	})
	promptText = strings.TrimSpace(promptText)
	const constraint = "## Session Constraints\n\nThis delegated ACP child session cannot call SPAWN. Complete the assigned task with the tools available in this session."
	if promptText == "" {
		return constraint
	}
	return strings.TrimSpace(promptText + "\n\n" + constraint)
}

func stripMarkdownSection(text string, heading string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	heading = strings.TrimSpace(heading)
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == heading {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(trimmed, "## ") {
			skip = false
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripPromptLines(text string, drop func(string) bool) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if drop != nil && drop(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (s *Service) restoreSessionState(state map[string]any) (string, map[string]string, string, []internalacp.PlanEntry, map[string]any) {
	modeID := sessionmode.LoadSnapshot(state)
	if !s.modeExists(modeID) {
		modeID = s.initialModeID()
	}
	values := s.initialConfigValues()
	raw := anyMap(state["acp"])
	if raw == nil {
		return modeID, values, "", loadPlanEntries(state["plan"]), nil
	}
	cwd, _ := raw["cwd"].(string)
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	if storedMode, _ := raw["modeId"].(string); s.modeExists(storedMode) {
		modeID = strings.TrimSpace(storedMode)
	}
	storedValues := anyMap(raw["configValues"])
	for _, template := range s.sessionConfig {
		if storedValues == nil {
			break
		}
		rawValue, _ := storedValues[template.ID].(string)
		if templateSupports(template, rawValue) {
			if values == nil {
				values = map[string]string{}
			}
			values[template.ID] = strings.TrimSpace(rawValue)
		}
	}
	return modeID, values, cwd, loadPlanEntries(state["plan"]), internalacp.CloneMeta(anyMap(raw["meta"]))
}

func mergeACPRequestMeta(base map[string]any, incoming map[string]any) map[string]any {
	if len(incoming) == 0 {
		return internalacp.CloneMeta(base)
	}
	merged := internalacp.CloneMeta(base)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range internalacp.CloneMeta(incoming) {
		if existing, ok := merged[key].(map[string]any); ok {
			if nested, ok := value.(map[string]any); ok {
				for nestedKey, nestedValue := range nested {
					existing[nestedKey] = nestedValue
				}
				merged[key] = existing
				continue
			}
		}
		merged[key] = value
	}
	return merged
}

func mergeSessionMeta(sess *managedSession, incoming map[string]any) bool {
	if sess == nil || len(incoming) == 0 {
		return false
	}
	next := mergeACPRequestMeta(sess.metaSnapshot(), incoming)
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	if equalACPMetadata(sess.meta, next) {
		return false
	}
	sess.meta = next
	return true
}

func equalACPMetadata(left map[string]any, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok {
			return false
		}
		leftNested, leftOK := value.(map[string]any)
		rightNested, rightOK := other.(map[string]any)
		if leftOK || rightOK {
			if !leftOK || !rightOK || !equalACPMetadata(leftNested, rightNested) {
				return false
			}
			continue
		}
		if fmt.Sprint(value) != fmt.Sprint(other) {
			return false
		}
	}
	return true
}

func (s *Service) validateSessionCWD(cwd string) (string, error) {
	value := filepath.Clean(strings.TrimSpace(cwd))
	if value == "" {
		return "", fmt.Errorf("cwd is required")
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("cwd %q must be an absolute path", value)
	}
	if !pathWithinRoot(s.workspaceRoot, value) {
		return "", fmt.Errorf("cwd %q is outside workspace root %q", value, s.workspaceRoot)
	}
	return value, nil
}

func (s *Service) ensureSessionExists(ctx context.Context, sessRef *session.Session) error {
	existsStore, ok := s.store.(session.ExistenceStore)
	if !ok {
		return nil
	}
	exists, err := existsStore.SessionExists(ctx, sessRef)
	if err != nil {
		return err
	}
	if !exists {
		return session.ErrSessionNotFound
	}
	return nil
}

func (s *Service) initialModeID() string {
	if s.modeExists(s.defaultModeID) {
		return s.defaultModeID
	}
	return ""
}

func (s *Service) initialConfigValues() map[string]string {
	if len(s.sessionConfig) == 0 {
		return nil
	}
	values := make(map[string]string, len(s.sessionConfig))
	for _, item := range s.sessionConfig {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		values[item.ID] = strings.TrimSpace(item.DefaultValue)
	}
	return values
}

func (s *Service) hasConfigCategory(category string) bool {
	category = strings.TrimSpace(category)
	if category == "" {
		return false
	}
	for _, item := range s.sessionConfig {
		if strings.TrimSpace(item.Category) == category {
			return true
		}
	}
	return false
}

func (s *Service) modeExists(modeID string) bool {
	modeID = strings.TrimSpace(modeID)
	if modeID == "" {
		return false
	}
	for _, mode := range s.sessionModes {
		if strings.TrimSpace(mode.ID) == modeID {
			return true
		}
	}
	return false
}

func (s *Service) configTemplate(id string) (internalacp.SessionConfigOptionTemplate, bool) {
	id = strings.TrimSpace(id)
	for _, item := range s.sessionConfig {
		if strings.TrimSpace(item.ID) == id {
			return item, true
		}
	}
	return internalacp.SessionConfigOptionTemplate{}, false
}

func (s *Service) configOptionSupports(sess *managedSession, id string, value string) bool {
	id = strings.TrimSpace(id)
	value = strings.TrimSpace(value)
	if id == "" || value == "" {
		return false
	}
	for _, item := range sess.configOptionsSnapshot() {
		if strings.TrimSpace(item.ID) != id {
			continue
		}
		for _, option := range item.Options {
			if strings.TrimSpace(option.Value) == value {
				return true
			}
		}
		return false
	}
	if template, ok := s.configTemplate(id); ok {
		return templateSupports(template, value)
	}
	return false
}

func (s *Service) newSessionResources(ctx context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
	return s.sessionResourceFactory(ctx, sessionID, sessionCWD, caps, modeResolver)
}

func (s *Service) hasAvailableCommand(sess *managedSession, name string) bool {
	registry := slashcmd.New(slashDefinitions(sess.availableCommandsSnapshot())...)
	return registry.Has(name)
}

func (s *Service) formatSlashStatus(sess *managedSession) string {
	mode := strings.TrimSpace(sess.mode())
	if mode == "" {
		mode = "default"
	}
	lines := []string{
		"Session status:",
		"mode: " + mode,
	}
	if strings.TrimSpace(sess.cwd) != "" {
		lines = append(lines, "cwd: "+sess.cwd)
	}
	if modelID := currentModelID(sess.configOptionsSnapshot()); modelID != "" {
		lines = append(lines, "model: "+modelID)
	}
	if entries := sess.planSnapshot(); len(entries) > 0 {
		lines = append(lines, fmt.Sprintf("plan items: %d", len(entries)))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) sessionRef(sessionID string) *session.Session {
	return &session.Session{
		AppName: s.appName,
		UserID:  s.userID,
		ID:      strings.TrimSpace(sessionID),
	}
}

func (s *Service) storeSession(sess *managedSession) {
	if sess == nil || strings.TrimSpace(sess.id) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[strings.TrimSpace(sess.id)] = sess
}

func (s *Service) loadedSession(id string) *managedSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[strings.TrimSpace(id)]
}

func (s *Service) session(id string) (*managedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[strings.TrimSpace(id)]
	if !ok || sess == nil {
		return nil, fmt.Errorf("unknown session %q", id)
	}
	return sess, nil
}

func (s *managedSession) mode() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.modeID)
}

func (s *managedSession) configSnapshot() map[string]string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return cloneStringMap(s.configValues)
}

func (s *managedSession) metaSnapshot() map[string]any {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return internalacp.CloneMeta(s.meta)
}

func (s *managedSession) agentConfig() internalacp.AgentSessionConfig {
	return internalacp.AgentSessionConfig{
		ModeID:       s.mode(),
		ConfigValues: s.configSnapshot(),
	}
}

func (s *managedSession) setState(modeID string, configValues map[string]string, planEntries []internalacp.PlanEntry, meta map[string]any) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.modeID = strings.TrimSpace(modeID)
	s.configValues = cloneStringMap(configValues)
	s.meta = internalacp.CloneMeta(meta)
	s.planEntries = append([]internalacp.PlanEntry(nil), planEntries...)
}

func (s *managedSession) setPlan(entries []internalacp.PlanEntry) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.planEntries = append([]internalacp.PlanEntry(nil), entries...)
}

func (s *managedSession) planSnapshot() []internalacp.PlanEntry {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]internalacp.PlanEntry(nil), s.planEntries...)
}

func (s *managedSession) configOptionsSnapshot() []internalacp.SessionConfigOption {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]internalacp.SessionConfigOption(nil), s.configOptions...)
}

func (s *managedSession) availableCommandsSnapshot() []internalacp.AvailableCommand {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]internalacp.AvailableCommand(nil), s.availableCommands...)
}

func (s *managedSession) setActiveRun(cancel context.CancelFunc, handle sessionsvc.TurnHandle) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.runCancel = cancel
	s.activeRun = handle
}

func (s *managedSession) activeHandle() sessionsvc.TurnHandle {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeRun
}

func (s *managedSession) clearActiveRun() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.runCancel = nil
	s.activeRun = nil
}

func (s *managedSession) cancelActiveRun() {
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

func (h *promptHandle) Events() iter.Seq2[*session.Event, error] {
	if h == nil || h.handle == nil {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, fmt.Errorf("acpadapter: prompt handle is nil"))
		}
	}
	seq := h.handle.Events()
	return func(yield func(*session.Event, error) bool) {
		defer h.finish()
		for ev, err := range seq {
			if ev != nil {
				if entries := planEntriesFromStateEvent(ev); len(entries) > 0 && h.session != nil {
					h.session.setPlan(entries)
				}
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

func (h *promptHandle) Close() error {
	if h == nil || h.handle == nil {
		return nil
	}
	err := h.handle.Close()
	h.finish()
	return err
}

func (h *promptHandle) finish() {
	if h == nil {
		return
	}
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

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func templateSupports(t internalacp.SessionConfigOptionTemplate, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range t.Options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

func anyMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, one := range typed {
			out[key] = one
		}
		return out
	default:
		return nil
	}
}

func loadPlanEntries(raw any) []internalacp.PlanEntry {
	payload := anyMap(raw)
	if payload == nil {
		return nil
	}
	return normalizePlanEntries(payload["entries"])
}

func normalizePlanEntries(raw any) []internalacp.PlanEntry {
	var decoded []internalacp.PlanEntry
	if err := decodeACPViaJSON(raw, &decoded); err != nil {
		return nil
	}
	out := make([]internalacp.PlanEntry, 0, len(decoded))
	for _, item := range decoded {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		out = append(out, internalacp.PlanEntry{Content: content, Status: status})
	}
	return out
}

func slashDefinitions(cmds []internalacp.AvailableCommand) []slashcmd.Definition {
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
		defs = append(defs, slashcmd.Definition{
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			InputHint:   hint,
		})
	}
	return defs
}

func currentModelID(options []internalacp.SessionConfigOption) string {
	for _, item := range options {
		if strings.TrimSpace(item.Category) != "model" {
			continue
		}
		return strings.TrimSpace(item.CurrentValue)
	}
	return ""
}

func filterImageContentParts(parts []model.ContentPart, keepImages bool) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	filtered := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == model.ContentPartImage && !keepImages {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func planEntriesFromStateEvent(ev *session.Event) []internalacp.PlanEntry {
	if ev == nil {
		return nil
	}
	resp := ev.Message.ToolResponse()
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.PlanToolName) {
		return nil
	}
	return normalizePlanEntries(resp.Result["entries"])
}

func pathWithinRoot(root string, path string) bool {
	root = resolvePathForContainment(root)
	path = resolvePathForContainment(path)
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func resolvePathForContainment(path string) string {
	current := filepath.Clean(strings.TrimSpace(path))
	if current == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		return filepath.Clean(resolved)
	}
	suffix := make([]string, 0, 4)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(strings.TrimSpace(path))
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
		if _, err := os.Lstat(current); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			continue
		}
		for i := len(suffix) - 1; i >= 0; i-- {
			resolved = filepath.Join(resolved, suffix[i])
		}
		return filepath.Clean(resolved)
	}
}

func decodeACPViaJSON(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
