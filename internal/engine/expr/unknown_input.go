package expr

import (
	"fmt"
	"reflect"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// UnknownInput is immutable source provenance for a concrete TEXT literal whose
// logical input remains UNKNOWN. Only argument analysis creates this witness;
// it grants no executable UNKNOWN descriptor or datum.
type UnknownInput struct {
	Literal  parser.LiteralExpr
	Source   parser.Expr
	CastType *sqltype.Descriptor
	// Chain is populated only for composition, including the original cast.
	Chain []TransparentInputCast
}

// TransparentInputCast records one resolved no-op in source order (inner first).
// Syntax is retained verbatim: names alone never confer logical authority.
type TransparentInputCast struct {
	Syntax parser.CastExpr
	Target *sqltype.Descriptor
}

func TransparentUnknownTarget(target *sqltype.Descriptor) bool {
	if target == nil {
		return false
	}
	switch target.ID() {
	case sqltype.Unknown, 2276, 2283, 2776, 5077, 5079:
		return true
	}
	return false
}

func ValidateTransparentInputCasts(source parser.Expr, steps []TransparentInputCast) error {
	for _, step := range steps {
		if !TransparentUnknownTarget(step.Target) || !reflect.DeepEqual(step.Syntax.Child, source) {
			return fmt.Errorf("stuntgres: invalid transparent unknown cast chain")
		}
		source = step.Syntax
	}
	return nil
}

func TransparentInputCastTarget(steps []TransparentInputCast, raw parser.CastExpr) *sqltype.Descriptor {
	for _, step := range steps {
		if reflect.DeepEqual(step.Syntax, raw) {
			return step.Target
		}
	}
	return nil
}

func (u *UnknownInput) Validate(usage sqltype.Usage, value sqltype.Value) error {
	if u == nil {
		return nil
	}
	if usage.Type == nil || usage.Type.ID() != sqltype.Text || usage != usage.Type.Usage() || value.Type() != usage.Type {
		return fmt.Errorf("stuntgres: invalid unknown input transport")
	}
	switch u.Literal.Value.Kind {
	case parser.NullValue:
		if !value.IsNull() {
			return fmt.Errorf("stuntgres: invalid unknown NULL input")
		}
	case parser.StringValue:
		text, err := value.Text()
		if err != nil || text != u.Literal.Value.Text {
			return fmt.Errorf("stuntgres: invalid unknown string input")
		}
	default:
		return fmt.Errorf("stuntgres: invalid unknown input source")
	}
	return u.ValidateProvenance()
}

func (u *UnknownInput) ValidateProvenance() error {
	if u.CastType == nil {
		if len(u.Chain) != 0 || !reflect.DeepEqual(u.Source, u.Literal) {
			return fmt.Errorf("stuntgres: invalid unknown literal provenance")
		}
	} else {
		if len(u.Chain) != 0 {
			if len(u.Chain) < 2 || ValidateTransparentInputCasts(u.Literal, u.Chain) != nil || u.Chain[len(u.Chain)-1].Target != u.CastType || !reflect.DeepEqual(u.Source, u.Chain[len(u.Chain)-1].Syntax) {
				return fmt.Errorf("stuntgres: invalid unknown cast provenance")
			}
		} else {
			cast, ok := u.Source.(parser.CastExpr)
			if !TransparentUnknownTarget(u.CastType) || !ok || !reflect.DeepEqual(cast.Child, u.Literal) {
				return fmt.Errorf("stuntgres: invalid unknown cast provenance")
			}
		}
	}
	return nil
}

// MatchesLiteral and MatchesCast authenticate the original frozen source path,
// not a function spelling or a coincident output value.
func (u *UnknownInput) MatchesLiteral(raw parser.Expr) bool {
	return u != nil && reflect.DeepEqual(raw, u.Literal)
}
func (u *UnknownInput) MatchesCast(raw parser.CastExpr) bool {
	return u.CastTarget(raw) != nil
}

func (u *UnknownInput) CastTarget(raw parser.CastExpr) *sqltype.Descriptor {
	if u == nil {
		return nil
	}
	if len(u.Chain) != 0 {
		return TransparentInputCastTarget(u.Chain, raw)
	}
	if u.CastType != nil && reflect.DeepEqual(raw, u.Source) {
		return u.CastType
	}
	return nil
}

// RetagTransparentInputCasts preserves accepted backing on identity mappings.
func RetagTransparentInputCasts(steps []TransparentInputCast, typ func(*sqltype.Descriptor) *sqltype.Descriptor) ([]TransparentInputCast, bool, error) {
	var mapped []TransparentInputCast
	for i, step := range steps {
		target := typ(step.Target)
		if !TransparentUnknownTarget(target) || target.ID() != step.Target.ID() {
			return nil, false, fmt.Errorf("stuntgres: invalid remapped transparent unknown target")
		}
		if target != step.Target {
			if mapped == nil {
				mapped = make([]TransparentInputCast, len(steps))
				copy(mapped, steps)
			}
			mapped[i].Target = target
		}
	}
	if mapped == nil {
		return steps, false, nil
	}
	return mapped, true, nil
}

// SameTransparentInputCasts compares descriptor authority by identity, not by
// reflect's structural equality of same-valued foreign descriptor pointers.
func SameTransparentInputCasts(left, right []TransparentInputCast) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Target != right[i].Target || !reflect.DeepEqual(left[i].Syntax, right[i].Syntax) {
			return false
		}
	}
	return true
}
