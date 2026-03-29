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
	observationCtx := context.WithoutCancel(ctx)
	updateCtx := observationCtx
	runState := &activeExternalAgentRun{}
	runState.setSessionID(participant.ChildSessionID)
	client, err := acpclient.Start(observationCtx, acpclient.Config{
		Command:   strings.TrimSpace(desc.Command),
		Args:      append([]string(nil), desc.Args...),
		Env:       copyStringMap(desc.Env),
		WorkDir:   c.resolveExternalAgentWorkDir(desc),
		Runtime:   c.executionRuntimeForSession(),
		Workspace: c.workspace.CWD,
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			if !ready.Load() {
				return
			}
			c.forwardExternalAgentUpdate(updateCtx, &externalAgentTurn{
				mode:        externalAgentTurnLoad,
				desc:        desc,
				participant: participant,
				toolCalls:   map[string]toolCallSnapshot{},
			}, env)
		},
		OnPermissionRequest: func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
			return c.handleExternalPermissionRequest(reqCtx, req, strings.TrimSpace(desc.ID), runState)
		},
	})
	if err != nil {
		return
	}
	runState.setClient(client)
	defer func() { _ = client.Close() }()
	initCtx, initCancel := context.WithTimeout(observationCtx, resumedSubagentLoadTimeoutForAgent(participant.AgentID))
	defer initCancel()
	if _, err := client.Initialize(initCtx); err != nil {
		return
	}
	loadCtx, loadCancel := context.WithTimeout(observationCtx, resumedSubagentLoadTimeoutForAgent(participant.AgentID))
	defer loadCancel()
	if _, err := client.LoadSession(loadCtx, participant.ChildSessionID, c.resolveExternalAgentWorkDir(desc), nil); err != nil {
		return
	}
	ready.Store(true)
	<-ctx.Done()
}
