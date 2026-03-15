package idutil

import (
	"strings"

	"github.com/google/uuid"
)

const (
	sessionPrefix         = "s-"
	runPrefix             = "r-"
	taskPrefix            = "t-"
	delegationPrefix      = "dlg_"
	sessionTokenLength    = 12
	runTokenLength        = 12
	taskTokenLength       = 12
	delegationTokenLength = 12
	DisplayPrefixLength   = 10
)

func NewSessionID() string {
	return sessionPrefix + compactUUID(sessionTokenLength)
}

func NewDelegationID() string {
	return delegationPrefix + compactUUID(delegationTokenLength)
}

func NewRunID() string {
	return runPrefix + compactUUID(runTokenLength)
}

func NewTaskID() string {
	return taskPrefix + compactUUID(taskTokenLength)
}

func ShortDisplay(id string) string {
	value := strings.TrimSpace(id)
	if len(value) <= DisplayPrefixLength {
		return value
	}
	return value[:DisplayPrefixLength]
}

func compactUUID(n int) string {
	value := strings.ReplaceAll(uuid.NewString(), "-", "")
	if n <= 0 || n >= len(value) {
		return value
	}
	return value[:n]
}
