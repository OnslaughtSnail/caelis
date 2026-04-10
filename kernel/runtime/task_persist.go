package runtime

import (
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/taskruntime"
)

func recoverBashBackendName(spec map[string]any, result map[string]any, execRuntime toolexec.Runtime) string {
	return taskruntime.RecoverBashBackendName(spec, result, execRuntime)
}

func stringValue(values map[string]any, key string) string {
	return taskruntime.StringValue(values, key)
}

func boolValue(values map[string]any, key string) bool {
	return taskruntime.BoolValue(values, key)
}

func intValue(values map[string]any, key string) int {
	return taskruntime.IntValue(values, key)
}
