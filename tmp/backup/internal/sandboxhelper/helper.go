package sandboxhelper

import toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"

const BinaryName = "caelis-sandbox-helper"

func MaybeRun(args []string) bool {
	return toolexec.MaybeRunInternalHelper(args)
}
