package engine

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Added defaults are execution-private. Only captured nonvolatile values enter
// transaction shape projections; persistent definitions retain their bound tree.
type addedColumn struct {
	attribute    int32
	column       *parser.Column
	declaration  analyze.ColumnDeclaration
	expression   expr.Expr
	dependencies []ids.ObjectRef // Borrowed cooked DEFAULT manifest, distinct from the ALTER statement.
	consumer     *catalog.Relation
	value        sqltype.Value
}

func alterCookingError(err error) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position = ""
		return &copy
	}
	return err
}
