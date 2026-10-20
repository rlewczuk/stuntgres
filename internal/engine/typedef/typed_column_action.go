package typedef

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type TypedColumnAction struct {
	Action             parser.AlterTableAction
	Usage              *sqltype.Usage
	DeclaredDimensions int16
}
