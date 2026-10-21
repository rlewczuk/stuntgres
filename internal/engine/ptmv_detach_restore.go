package engine

import (
	"stuntgres/internal/engine/ids"
)

type partitionReplayTeardown struct{ Child, Parent ids.ObjectRef }
