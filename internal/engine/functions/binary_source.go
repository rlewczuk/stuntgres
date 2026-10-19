package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func (e Binary) SourceAdmission() (bool, bool) { return parser.JSONFunctionName(e.Plan.Name()), false }

func (e Binary) OpenSource(c expr.Context) (expr.SourceCursor, error) {
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
