package shell

import (
	"sync"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

var (
	defaultRuntimeOnce sync.Once
	defaultRuntimeInst toolexec.Runtime
	defaultRuntimeErr  error
)

func runtimeOrDefault(runtime toolexec.Runtime) (toolexec.Runtime, error) {
	if runtime != nil {
		return runtime, nil
	}
	defaultRuntimeOnce.Do(func() {
		defaultRuntimeInst, defaultRuntimeErr = toolexec.New(toolexec.Config{
			Mode: toolexec.ModeNoSandbox,
		})
	})
	if defaultRuntimeErr != nil {
		return nil, defaultRuntimeErr
	}
	return defaultRuntimeInst, nil
}
