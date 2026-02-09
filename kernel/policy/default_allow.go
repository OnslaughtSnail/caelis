package policy

// DefaultAllow returns the kernel default policy hook.
func DefaultAllow() Hook {
	return NoopHook{HookName: "default_allow"}
}
