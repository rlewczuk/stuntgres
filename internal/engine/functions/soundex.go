package functions

import (
	"fmt"

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

func SoundexInput[E any](name string, args []Argument[E], text sqltype.Usage) Resolution {
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

func CoerceSoundexArguments[E any, B ArgumentCoercer[E]](b B, args []Argument[E], text sqltype.Usage) ([]E, error) {
	children := make([]E, len(args))
	for i, arg := range args {
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: text})
		if err != nil {
			return nil, err
		}
		children[i] = child
	}
	return children, nil
}

type Soundex[E Expression[C], C Context, P any] struct {
	Info[P]
	Function string
	Args []E
}

func NewSoundex[E Expression[C], C Context, P any](info Info[P], name string, args []E) (Soundex[E, C, P], error) {
	call := Soundex[E, C, P]{Info: info, Function: name, Args: args}
	return call, call.Validate()
}

func (e Soundex[E, C, P]) Reconstruct(info Info[P], args []E) (Soundex[E, C, P], error) {
	return NewSoundex[E, C](info, e.Function, args)
}

func (e Soundex[E, C, P]) Validate() error {
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

func (e Soundex[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
