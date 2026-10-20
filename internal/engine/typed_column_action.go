package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type typedColumnAction struct {
	action             parser.AlterTableAction
	usage              *sqltype.Usage
	declaredDimensions int16
}
