package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// MetaphoneSignature admits precisely the three installed release signatures.
func MetaphoneSignature(name string, count int) string {
	switch {
	case name == "metaphone" && count == 2:
		return "25,23"
	case (name == "dmetaphone" || name == "dmetaphone_alt") && count == 1:
		return "25"
	}
	return ""
}

func MetaphoneInput(name string, args []Argument, text, integer sqltype.Usage) Resolution {
	if MetaphoneSignature(name, len(args)) == "" {
		return Missing
	}
	for i, arg := range args {
		target := text
		if i == 1 {
			target = integer
		}
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
				return Missing
			}
		}
	}
	return Resolved
}

func CoerceMetaphoneArguments(b ArgumentCoercer, args []Argument, text, integer sqltype.Usage) ([]expr.Expr, error) {
	children := make([]expr.Expr, len(args))
	for i, arg := range args {
		target := text
		if i == 1 {
			target = integer
		}
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		children[i] = child
	}
	return children, nil
}

type Metaphone struct {
	expr.Info
	Function string
	Args     []expr.Expr
}

func NewMetaphone(info expr.Info, name string, args []expr.Expr) (Metaphone, error) {
	call := Metaphone{Info: info, Function: name, Args: args}
	return call, call.Validate()
}
func (e Metaphone) Reconstruct(info expr.Info, args []expr.Expr) (Metaphone, error) {
	return NewMetaphone(info, e.Function, args)
}
func (e Metaphone) Validate() error {
	if MetaphoneSignature(e.Function, len(e.Args)) == "" || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid stored Metaphone call")
	}
	for i, arg := range e.Args {
		want := sqltype.Text
		if i == 1 {
			want = sqltype.Int4
		}
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != want {
			return fmt.Errorf("stuntgres: invalid stored Metaphone argument")
		}
	}
	return nil
}
func (e Metaphone) Eval(c expr.Context) (sqltype.Value, error) {
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
	var result string
	if e.Function == "metaphone" {
		cap, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		result, err = MetaphoneCode(ctx, source, int32(cap))
		if err != nil {
			return sqltype.Value{}, err
		}
	} else {
		result, err = doubleMetaphoneCode(ctx, source, e.Function == "dmetaphone_alt")
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(e.Usage.Type, result), nil
}
