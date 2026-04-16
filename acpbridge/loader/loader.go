package loader

import (
	"context"
	"strings"

	"github.com/OnslaughtSnail/caelis/acp/schema"
	bridgeprojector "github.com/OnslaughtSnail/caelis/acpbridge/projector"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type PromptCallbacks interface {
	SessionUpdate(context.Context, schema.SessionNotification) error
}

type SessionModesProvider interface {
	SessionModes(context.Context, sdksession.Session) (*schema.SessionModeState, error)
}

type SessionConfigProvider interface {
	SessionConfigOptions(context.Context, sdksession.Session) ([]schema.SessionConfigOption, error)
}

// SessionServiceLoaderConfig configures one default ACP session/load adapter
// backed by the SDK session service.
type SessionServiceLoaderConfig struct {
	Sessions  sdksession.Service
	Projector bridgeprojector.Projector
	AppName   string
	UserID    string
	Modes     SessionModesProvider
	Config    SessionConfigProvider
}

// SessionServiceLoader replays one durable SDK session through ACP
// session/update notifications.
type SessionServiceLoader struct {
	sessions  sdksession.Service
	projector bridgeprojector.Projector
	appName   string
	userID    string
	modes     SessionModesProvider
	config    SessionConfigProvider
}

// NewSessionServiceLoader constructs one default session/load adapter.
func NewSessionServiceLoader(cfg SessionServiceLoaderConfig) *SessionServiceLoader {
	projector := cfg.Projector
	if projector == nil {
		projector = bridgeprojector.EventProjector{}
	}
	return &SessionServiceLoader{
		sessions:  cfg.Sessions,
		projector: projector,
		appName:   strings.TrimSpace(cfg.AppName),
		userID:    strings.TrimSpace(cfg.UserID),
		modes:     cfg.Modes,
		config:    cfg.Config,
	}
}

// LoadSession replays durable canonical history through session/update and
// returns optional mode/config metadata for the loaded session.
func (l *SessionServiceLoader) LoadSession(
	ctx context.Context,
	req schema.LoadSessionRequest,
	cb PromptCallbacks,
) (schema.LoadSessionResponse, error) {
	ref := sdksession.SessionRef{
		AppName:   l.appName,
		UserID:    l.userID,
		SessionID: strings.TrimSpace(req.SessionID),
	}
	loaded, err := l.sessions.LoadSession(ctx, sdksession.LoadSessionRequest{
		SessionRef: ref,
	})
	if err != nil {
		return schema.LoadSessionResponse{}, err
	}

	if cb != nil {
		for _, event := range loaded.Events {
			if event == nil {
				continue
			}
			notifications, err := l.projector.ProjectNotifications(event)
			if err != nil {
				return schema.LoadSessionResponse{}, err
			}
			for _, notification := range notifications {
				if err := cb.SessionUpdate(ctx, notification); err != nil {
					return schema.LoadSessionResponse{}, err
				}
			}
		}
	}

	resp := schema.LoadSessionResponse{}
	if l.modes != nil {
		modes, err := l.modes.SessionModes(ctx, loaded.Session)
		if err != nil {
			return schema.LoadSessionResponse{}, err
		}
		resp.Modes = modes
	}
	if l.config != nil {
		options, err := l.config.SessionConfigOptions(ctx, loaded.Session)
		if err != nil {
			return schema.LoadSessionResponse{}, err
		}
		resp.ConfigOptions = options
	}
	return resp, nil
}
