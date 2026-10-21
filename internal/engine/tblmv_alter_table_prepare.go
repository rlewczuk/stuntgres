package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type alterTablePreparation struct {
	Actions     []parser.AlterTableAction
	Destination ids.ObjectRef
	Types       []alterTypePreparation
}
