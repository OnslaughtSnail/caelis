package runtime

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

func sameTaskSession(a task.SessionRef, b task.SessionRef) bool {
	return strings.TrimSpace(a.AppName) == strings.TrimSpace(b.AppName) &&
		strings.TrimSpace(a.UserID) == strings.TrimSpace(b.UserID) &&
		strings.TrimSpace(a.SessionID) == strings.TrimSpace(b.SessionID)
}
