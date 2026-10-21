package partition

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

type InsertGroup struct {
	Ref    ids.ObjectRef
	Rows   index.WriteBatch
	Unique *index.UniqueWriteBinding
}
