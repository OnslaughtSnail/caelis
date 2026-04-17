package main

import (
	"context"

	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
)

func (c *cliConsole) initializeExternalParticipantProjectionTurn(ctx context.Context, turn *externalAgentTurn) error {
	if c == nil {
		return nil
	}
	return c.acpProjectionStore().AppendParticipantTurnStart(ctx, turn)
}

func (c *cliConsole) appendExternalParticipantProjection(ctx context.Context, turn *externalAgentTurn, item acpprojector.Projection) error {
	if c == nil {
		return nil
	}
	return c.acpProjectionStore().AppendParticipantProjection(ctx, turn, item)
}

func (c *cliConsole) updateExternalParticipantProjectionStatus(ctx context.Context, turn *externalAgentTurn, status string) error {
	if c == nil {
		return nil
	}
	return c.acpProjectionStore().AppendParticipantStatus(ctx, turn, status)
}
