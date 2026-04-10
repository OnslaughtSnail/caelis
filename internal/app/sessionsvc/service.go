package sessionsvc

import kernelsessionsvc "github.com/OnslaughtSnail/caelis/kernel/sessionsvc"

type WorkspaceRef = kernelsessionsvc.WorkspaceRef
type SessionRef = kernelsessionsvc.SessionRef
type SessionInfo = kernelsessionsvc.SessionInfo
type LoadedSession = kernelsessionsvc.LoadedSession
type StartSessionRequest = kernelsessionsvc.StartSessionRequest
type LoadSessionRequest = kernelsessionsvc.LoadSessionRequest
type RunTurnRequest = kernelsessionsvc.RunTurnRequest
type RunTurnResult = kernelsessionsvc.RunTurnResult
type SessionListRequest = kernelsessionsvc.SessionListRequest
type SessionSummary = kernelsessionsvc.SessionSummary
type SessionList = kernelsessionsvc.SessionList
type DelegationRef = kernelsessionsvc.DelegationRef
type InterruptSessionRequest = kernelsessionsvc.InterruptSessionRequest
type TurnHandle = kernelsessionsvc.TurnHandle
type WorkspaceSessionIndex = kernelsessionsvc.WorkspaceSessionIndex
type ServiceConfig = kernelsessionsvc.ServiceConfig
type Service = kernelsessionsvc.Service

func New(cfg ServiceConfig) (*Service, error) {
	return kernelsessionsvc.New(cfg)
}
