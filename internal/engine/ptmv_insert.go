package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

type partitionInsertGroup struct {
	ref    ids.ObjectRef
	rows   index.WriteBatch
	unique *index.UniqueWriteBinding
}
