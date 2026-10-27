package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Replacement is a scalar composite source: the shared FROM machinery expands
// exactly one value, including a whole-NULL tuple. It is never an SRF.
func (e HstoreRecord) SourceAdmission() (bool, bool) { return e.Arity() == 2, false }

func (e HstoreRecord) SameSourceOccurrence(other expr.Expr) bool {
	next, ok := other.(HstoreRecord)
	return ok && e.Member == next.Member && e.Ref == next.Ref && sqltype.SameRecordUsage(e.ResultUsage(), next.ResultUsage())
}

func (e HstoreRecord) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
