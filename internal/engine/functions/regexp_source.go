package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e Regexp) SourceAdmission() (bool, bool) { return !e.Plan.ReturnsSet(), false }

func (e Regexp) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	admitted, _ := e.SourceAdmission()
	if !admitted {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
