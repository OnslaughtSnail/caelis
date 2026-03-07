//go:build !linux

package execenv

func MaybeRunInternalHelper(args []string) bool {
	_ = args
	return false
}
