package sessionmode

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cmdsafety"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

const (
	DefaultMode = "default"
	PlanMode    = "plan"
	FullMode    = "full_access"

	snapshotKey = "session_mode"
	openTag     = "<caelis-session-mode"
	closeTag    = "</caelis-session-mode>"
)

func Normalize(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case PlanMode:
		return PlanMode
	case FullMode:
		return FullMode
	default:
		return DefaultMode
	}
}

func Next(mode string) string {
	switch Normalize(mode) {
	case DefaultMode:
		return PlanMode
	case PlanMode:
		return FullMode
	default:
		return DefaultMode
	}
}

func DisplayLabel(mode string) string {
	switch Normalize(mode) {
	case PlanMode:
		return "plan"
	case FullMode:
		return "full_access"
	default:
		return ""
	}
}

func IsFullAccess(mode string) bool {
	return Normalize(mode) == FullMode
}

func PermissionMode(mode string) toolexec.PermissionMode {
	if IsFullAccess(mode) {
		return toolexec.PermissionModeFullControl
	}
	return toolexec.PermissionModeDefault
}

func ModeForPermission(permissionMode toolexec.PermissionMode, currentMode string) string {
	switch permissionMode {
	case toolexec.PermissionModeFullControl:
		return FullMode
	default:
		if Normalize(currentMode) == PlanMode {
			return PlanMode
		}
		return DefaultMode
	}
}

func IsDangerousCommand(command string) bool {
	return cmdsafety.IsDangerousCommand(command)
}

func Inject(input string, mode string) string {
	visible := Strip(input)
	if Normalize(mode) != PlanMode {
		return visible
	}
	control := controlBlock(PlanMode)
	visible = strings.TrimSpace(visible)
	if visible == "" {
		return control
	}
	return control + "\n\n" + visible
}

func Strip(input string) string {
	trimmed := strings.TrimLeft(input, " \t\r\n")
	if !strings.HasPrefix(trimmed, openTag) {
		return input
	}
	end := strings.Index(trimmed, closeTag)
	if end < 0 {
		return input
	}
	rest := trimmed[end+len(closeTag):]
	return strings.TrimLeft(rest, "\r\n")
}

func VisibleText(input string) string {
	return strings.TrimSpace(Strip(input))
}

func LoadSnapshot(values map[string]any) string {
	if values == nil {
		return DefaultMode
	}
	mode, _ := values[snapshotKey].(string)
	return Normalize(mode)
}

func StoreSnapshot(values map[string]any, mode string) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	values[snapshotKey] = Normalize(mode)
	return values
}

func controlBlock(mode string) string {
	mode = Normalize(mode)
	switch mode {
	case PlanMode:
		return `<caelis-session-mode mode="plan" hidden="true">
This turn is running in PLAN mode. Focus on analysis, planning, tradeoffs, and implementation strategy. Do not make changes unless the user explicitly asks you to execute them. Do not call the PLAN tool in this mode.
</caelis-session-mode>`
	default:
		return ""
	}
}
