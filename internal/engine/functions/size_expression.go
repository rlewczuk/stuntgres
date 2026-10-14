package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type SizeTypes struct{ Text, Int8, Numeric *sqltype.Descriptor }
type SizeTypeDemand uint8

const (
	SizeText SizeTypeDemand = 1 << iota
	SizeInt8
	SizeNumeric
)

func AnalyzeSizeArgument[E any, B ArgumentAnalyzer[E]](b B, name string, raw parser.Expr) (Argument[E], error) {
	intent := StringOtherArgument
	if name == "pg_size_bytes" {
		intent = StringTextArgument
	}
	return b.AnalyzeArgument(raw, AnalysisRequest{Intent: intent})
}
func NeededSizeTypes(name string, visible bool, count int, input sqltype.Usage) SizeTypeDemand {
	if !visible || count != 1 {
		return 0
	}
	if name != "pg_size_pretty" {
		return SizeText
	}
	if d := input.Type; d != nil && d.BaseType().ID() == sqltype.Int8 {
		return SizeInt8
	}
	return SizeInt8 | SizeNumeric
}
func SelectSize(name string, visible bool, count int, input sqltype.Usage, types SizeTypes) (target *sqltype.Descriptor, resolution Resolution) {
	matches := 0
	if visible && count == 1 {
		candidates := [2]*sqltype.Descriptor{types.Text}
		n := 1
		if name == "pg_size_pretty" {
			candidates, n = [2]*sqltype.Descriptor{types.Int8, types.Numeric}, 2
		}
		for _, candidate := range candidates[:n] {
			if d := input.Type; d != nil {
				if d.BaseType().ID() == candidate.ID() {
					target, matches = candidate, 1
					break
				}
				if _, err := sqltype.PlanCoercion(input, candidate.Usage(), sqltype.Implicit); err != nil {
					continue
				}
			}
			target = candidate
			matches++
		}
	}
	if matches == 1 {
		return target, Resolved
	}
	if matches > 1 {
		return target, Ambiguous
	}
	return target, Missing
}
func CoerceSizeArgument[E any, B ArgumentCoercer[E]](b B, operand Argument[E], target sqltype.Usage) (E, error) {
	return b.CoerceArgument(operand, CoercionRequest{Intent: SizeFormalArgument, Target: target})
}

// NewSize retains the selected unmodified formal usage independently of the
// argument's coercion metadata, which may preserve a domain's numeric typmod.
func NewSize[E Expression[C], C LiteralContext[E], P any](info Info[P], name string, arg E, formal sqltype.Usage) (Math[E, C, P], error) {
	plan, err := sqltype.BindMath(name, info.Usage, formal)
	return Math[E, C, P]{info, plan, []E{arg}}, err
}
func SizeResult(name string) sqltype.ID {
	if name == "pg_size_bytes" {
		return sqltype.Int8
	}
	return sqltype.Text
}
