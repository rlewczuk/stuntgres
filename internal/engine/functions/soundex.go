package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func SoundexSignature(name string, count int) string {
	switch {
	case name == "soundex" && count == 1:
		return "25"
	case name == "difference" && count == 2:
		return "25,25"
	}
	return ""
}

func SoundexInput(name string, args []Argument, text sqltype.Usage) Resolution {
	if SoundexSignature(name, len(args)) == "" {
		return Missing
	}
	for _, arg := range args {
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, text, sqltype.Implicit); err != nil {
				return Missing
			}
		}
	}
	return Resolved
}

func CoerceSoundexArguments(b ArgumentCoercer, args []Argument, text sqltype.Usage) ([]expr.Expr, error) {
	children := make([]expr.Expr, len(args))
	for i, arg := range args {
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: text})
		if err != nil {
			return nil, err
		}
		children[i] = child
	}
	return children, nil
}

type Soundex struct {
	expr.Info
	Function string
	Args     []expr.Expr
}

func NewSoundex(info expr.Info, name string, args []expr.Expr) (Soundex, error) {
	call := Soundex{Info: info, Function: name, Args: args}
	return call, call.Validate()
}

func (e Soundex) Reconstruct(info expr.Info, args []expr.Expr) (Soundex, error) {
	return NewSoundex(info, e.Function, args)
}

// Soundex implements expr.Composite.
var _ expr.Composite = Soundex{}

func (e Soundex) Children() []expr.Expr { return e.Args }
func (e Soundex) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Soundex", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}

func (e Soundex) Validate() error {
	result := sqltype.Text
	if e.Function == "difference" {
		result = sqltype.Int4
	}
	if SoundexSignature(e.Function, len(e.Args)) == "" || e.Usage.Type == nil || e.Usage.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid stored Soundex call")
	}
	for _, arg := range e.Args {
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid stored Soundex argument")
		}
	}
	return nil
}

func (e Soundex) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	source, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Function == "soundex" {
		code, err := SoundexCode(ctx, source)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(e.Usage.Type, code), nil
	}
	target, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	score, err := SoundexDifference(ctx, source, target)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(e.Usage.Type, int64(score))
}
