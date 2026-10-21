package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type alterTablePreparation struct {
	actions     []parser.AlterTableAction
	destination ids.ObjectRef
	types       []alterTypePreparation
}
