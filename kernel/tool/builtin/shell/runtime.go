package shell

import (
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func runtimeOrDefault(runtime toolexec.Runtime) (toolexec.Runtime, error) {
	if runtime != nil {
		return runtime, nil
	}
	return nil, fmt.Errorf("tool: runtime is required")
}
