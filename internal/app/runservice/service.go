package runservice

import (
	kernelrunservice "github.com/OnslaughtSnail/caelis/kernel/runservice"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type ServiceConfig = kernelrunservice.ServiceConfig
type Service = kernelrunservice.Service
type RunTurnRequest = kernelrunservice.RunTurnRequest
type RunTurnResult = kernelrunservice.RunTurnResult

func New(cfg ServiceConfig) (*Service, error) {
	return kernelrunservice.New(cfg)
}

func NewSelfSpawnTool(defaultAgent string) (tool.Tool, error) {
	return kernelrunservice.NewSelfSpawnTool(defaultAgent)
}
