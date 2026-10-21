package table

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type AlterTablePreparation struct {
	Actions     []parser.AlterTableAction
	Destination ids.ObjectRef
	Types       []AlterTypePreparation
}
