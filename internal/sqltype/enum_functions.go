package sqltype

import (
	"context"
	"fmt"
)

// EnumSupportPlan holds the exact catalog generation selected by anyenum.
type EnumSupportPlan struct {
	name        string
	enum, array *Descriptor
	arity       int
}

func BindEnumSupport(name string, result Usage, arguments ...Usage) (EnumSupportPlan, error) {
	p := EnumSupportPlan{name: name, arity: len(arguments)}
	if len(arguments) < 1 || len(arguments) > 2 || name != "enum_range" && len(arguments) != 1 || name != "enum_first" && name != "enum_last" && name != "enum_range" {
		return p, fmt.Errorf("invalid enum support signature")
	}
	p.enum = arguments[0].Type
	if !p.enum.IsEnum() {
		return p, fmt.Errorf("invalid enum support argument")
	}
	for _, u := range arguments {
		if u != p.enum.Usage() {
			return p, fmt.Errorf("foreign enum support argument")
		}
	}
	expected := p.enum
	if name == "enum_range" {
		p.array = result.Type
		if !p.array.Concrete() || !p.array.IsArray() || p.array.Element() != p.enum {
			return p, fmt.Errorf("invalid enum range association")
		}
		expected = p.array
	}
	if result != expected.Usage() {
		return p, fmt.Errorf("invalid enum support result")
	}
	return p, nil
}
func (p EnumSupportPlan) Name() string      { return p.name }
func (p EnumSupportPlan) Enum() *Descriptor { return p.enum }
func (p EnumSupportPlan) Evaluate(ctx context.Context, first, last Value, env InputEnvironment) (Value, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
	}
	for i, v := range [2]Value{first, last} {
		if i >= p.arity {
			break
		}
		if v.typ != p.enum {
			return Value{}, fmt.Errorf("foreign enum support value")
		}
		if !v.null {
			if _, err := p.enum.enumOutput(v); err != nil {
				return Value{}, err
			}
		}
	}
	count := len(p.enum.enum.members)
	if p.name != "enum_range" {
		if count == 0 {
			return Value{}, failure("55000", fmt.Sprintf("enum %s contains no values", p.enum.DiagnosticName()))
		}
		ordinal := 0
		if p.name == "enum_last" {
			ordinal = count - 1
		}
		member := p.enum.enum.members[ordinal].ID
		if err := p.enum.checkEnumMember(member, env); err != nil {
			return Value{}, err
		}
		return Value{typ: p.enum, integer: member}, nil
	}
	lower, upper := 0, count-1
	if p.arity == 2 {
		if !first.null {
			lower = p.enum.enum.ranks[first.integer]
		}
		if !last.null {
			upper = p.enum.enum.ranks[last.integer]
		}
	}
	if upper < lower {
		return OwnedArrayValue(p.array, nil, nil)
	}
	cells := make([]Value, upper-lower+1)
	for i := range cells {
		member := p.enum.enum.members[lower+i].ID
		if err := p.enum.checkEnumMember(member, env); err != nil {
			return Value{}, err
		}
		cells[i] = Value{typ: p.enum, integer: member}
	}
	return OwnedArrayValue(p.array, []ArrayDimension{{Lower: 1, Length: int32(len(cells))}}, cells)
}
