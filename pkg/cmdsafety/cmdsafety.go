package cmdsafety

import internalcmdsafety "github.com/OnslaughtSnail/caelis/internal/cmdsafety"

func DetectBlockedCommand(command string) (string, string) {
	return internalcmdsafety.DetectBlockedCommand(command)
}

func IsDangerousCommand(command string) bool {
	return internalcmdsafety.IsDangerousCommand(command)
}
