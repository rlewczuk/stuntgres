package engine

import (
	"stuntgres/internal/engine/ids"
)

type partitionReplayTeardown struct{ child, parent ids.ObjectRef }
