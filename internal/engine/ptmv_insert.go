package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

type partitionInsertGroup struct {
	Ref    ids.ObjectRef
	Rows   index.WriteBatch
	Unique *index.UniqueWriteBinding
}
