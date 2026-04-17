package idutil

import coreid "github.com/OnslaughtSnail/caelis/pkg/idutil"

const DisplayPrefixLength = coreid.DisplayPrefixLength

func NewSessionID() string {
	return coreid.NewSessionID()
}

func NewDelegationID() string {
	return coreid.NewDelegationID()
}

func NewRunID() string {
	return coreid.NewRunID()
}

func NewTaskID() string {
	return coreid.NewTaskID()
}

func ShortDisplay(id string) string {
	return coreid.ShortDisplay(id)
}
