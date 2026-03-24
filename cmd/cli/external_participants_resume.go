package main

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
)

func (c *cliConsole) restoreResumedExternalParticipants(ctx context.Context) {
	if c == nil || c.tuiSender == nil {
		return
	}
	items, err := c.loadSessionParticipants(ctx)
	if err != nil {
		return
	}
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "running", "waiting_approval":
		default:
			continue
		}
		go c.resumeExternalParticipantStream(ctx, item)
	}
}

func (c *cliConsole) resumeExternalParticipantStream(ctx context.Context, participant externalParticipant) {
	desc, ok := c.dynamicSlashAgentDescriptor(participant.AgentID)
	if !ok {
		return
	}
	var ready atomic.Bool
	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:   strings.TrimSpace(desc.Command),
		Args:      append([]string(nil), desc.Args...),
		Env:       copyStringMap(desc.Env),
		WorkDir:   c.resolveExternalAgentWorkDir(desc),
		Runtime:   c.execRuntime,
		Workspace: c.workspace.CWD,
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			if !ready.Load() {
				return
			}
			c.forwardExternalAgentUpdate(&externalAgentTurn{
				mode:        externalAgentTurnLoad,
				desc:        desc,
				participant: participant,
				toolCalls:   map[string]toolCallSnapshot{},
			}, env)
		},
	})
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Initialize(ctx); err != nil {
		return
	}
	if _, err := client.LoadSession(ctx, participant.ChildSessionID, c.resolveExternalAgentWorkDir(desc)); err != nil {
		return
	}
	ready.Store(true)
	<-ctx.Done()
}
