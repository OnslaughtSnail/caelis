package main

import (
	"os"
	"strings"
)

func defaultNoAnimation() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("CAELIS_NO_ANIMATION")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
