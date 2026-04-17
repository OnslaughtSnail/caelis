package runtime

import (
	"github.com/OnslaughtSnail/caelis/kernel/delegation"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type DelegationMetadata = delegation.Metadata

func DelegationMetadataFromEvent(ev *session.Event) (DelegationMetadata, bool) {
	return delegation.MetadataFromEvent(ev)
}
