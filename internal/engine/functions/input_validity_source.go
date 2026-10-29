package functions

import "stuntgres/internal/engine/expr"

// Scalar calls produce exactly one typed value, including a whole-NULL record.
// Generic FunctionFrom layout expands RECORD members; this is never an SRF.
func (InputValidity) SourceAdmission() (bool, bool) { return true, false }

func (n InputValidity) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
