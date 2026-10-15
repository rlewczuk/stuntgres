package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// ArraySearchPlan retains concrete generations and equality authority. Missing
// equality and indeterminate collation are separate, demand-driven failures.
type ArraySearchPlan struct {
	name                   string
	array, result, integer *Descriptor
	comparison             Comparison
	missing                sqlstate.Error
}

func (p ArraySearchPlan) Equal(other ArraySearchPlan) bool {
	return p.name == other.name && p.array == other.array && p.result == other.result && p.integer == other.integer && p.missing == other.missing && p.comparison.Equal(other.comparison)
}

func BindArraySearch(name string, array, result, integer *Descriptor, collation CollationRef, conflict bool) (ArraySearchPlan, error) {
	if !array.Concrete() || !array.IsArray() || !result.Concrete() || !integer.Concrete() || integer.ID() != Int4 {
		return ArraySearchPlan{}, fmt.Errorf("invalid array search signature")
	}
	switch name {
	case "array_position":
		if result != integer {
			return ArraySearchPlan{}, fmt.Errorf("invalid position result")
		}
	case "array_positions":
		if !result.IsArray() || result.Element() != integer {
			return ArraySearchPlan{}, fmt.Errorf("invalid positions result")
		}
	case "array_remove", "array_replace":
		if result != array {
			return ArraySearchPlan{}, fmt.Errorf("invalid array result")
		}
	case "@>", "<@", "&&":
		if result.ID() != Bool {
			return ArraySearchPlan{}, fmt.Errorf("invalid membership result")
		}
	default:
		return ArraySearchPlan{}, fmt.Errorf("invalid array search operation")
	}
	// Array equality requests the leaf's base provider; unlike scalar enum
	// operator resolution, a domain over enum does not hide that provider.
	element := array.Element().BaseType()
	comparison, err := BindComparison(element, element, collation, conflict)
	if err != nil {
		// Unsupported equality leaves remain admitted array codecs. The release
		// algorithms request their equality provider only after specific fast paths.
		if d, ok := err.(interface{ SQLState() string }); !ok || d.SQLState() != "42883" {
			return ArraySearchPlan{}, err
		}
	}
	plan := ArraySearchPlan{name: name, array: array, result: result, integer: integer, comparison: comparison}
	if err != nil {
		plan.missing = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type " + array.Element().DiagnosticName()}
	}
	return plan, nil
}

func (p ArraySearchPlan) Name() string         { return p.name }
func (p ArraySearchPlan) Array() *Descriptor   { return p.array }
func (p ArraySearchPlan) Integer() *Descriptor { return p.integer }
func (p ArraySearchPlan) InputCollation() (CollationRef, bool) {
	return p.comparison.inputCollation, p.comparison.collationIndeterminate
}
func (p ArraySearchPlan) SessionDependent() bool { return p.comparison.SessionDependent() }
func (p ArraySearchPlan) Membership() bool       { return p.name == "@>" || p.name == "<@" || p.name == "&&" }
func (p ArraySearchPlan) demand() error {
	if p.missing.Code != "" {
		return &p.missing
	}
	return nil
}
func (p ArraySearchPlan) equal(ctx context.Context, a, b Value, env InputEnvironment) (bool, error) {
	if a.IsNull() || b.IsNull() {
		return a.IsNull() && b.IsNull(), nil
	}
	a.typ, b.typ = p.comparison.left, p.comparison.right
	return p.comparison.EqualValues(ctx, a, b, env)
}

func (p ArraySearchPlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	count := 2
	if p.name == "array_replace" {
		count = 3
	}
	if p.name == "array_position" && len(args) == 3 {
		count = 3
	}
	if len(args) != count || args[0].Type() != p.array {
		return Value{}, fmt.Errorf("invalid array search arguments")
	}
	if p.Membership() {
		if args[1].Type() != p.array {
			return Value{}, fmt.Errorf("foreign membership array generation")
		}
		if args[0].IsNull() || args[1].IsNull() {
			return Null(p.result), nil
		}
		if err := p.demand(); err != nil {
			return Value{}, err
		}
		return p.membership(ctx, args[0], args[1], env)
	}
	if args[1].Type() != p.array.Element() || p.name == "array_replace" && args[2].Type() != p.array.Element() || p.name == "array_position" && count == 3 && args[2].Type() != p.integer {
		return Value{}, fmt.Errorf("foreign array search argument generation")
	}
	a, search := args[0], args[1]
	if a.IsNull() {
		return Null(p.result), nil
	}
	if (p.name == "array_position" || p.name == "array_positions") && a.ArrayRank() > 1 {
		return Value{}, failure("0A000", "searching for elements in multidimensional arrays is not supported")
	}
	if a.ArrayLen() == 0 {
		switch p.name {
		case "array_position":
			return Null(p.result), nil
		case "array_positions":
			return OwnedArrayValue(p.result, nil, nil)
		default:
			return a, nil
		}
	}
	if p.name == "array_remove" && a.ArrayRank() > 1 {
		return Value{}, failure("0A000", "removing elements from multidimensional arrays is not supported")
	}
	if search.IsNull() && (p.name == "array_position" || p.name == "array_positions") {
		found := false
		for i := range a.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if a.ArrayElement(i).IsNull() {
				found = true
				break
			}
		}
		if !found {
			if p.name == "array_position" {
				return Null(p.result), nil
			}
			return OwnedArrayValue(p.result, nil, nil)
		}
	}
	start := int64(a.ArrayDimension(0).Lower)
	if p.name == "array_position" && count == 3 {
		if args[2].IsNull() {
			return Value{}, failure("22004", "initial position must not be null")
		}
		var err error
		start, err = args[2].Integer()
		if err != nil {
			return Value{}, err
		}
	}
	if err := p.demand(); err != nil {
		return Value{}, err
	}
	switch p.name {
	case "array_position", "array_positions":
		var positions []Value
		lower := int64(a.ArrayDimension(0).Lower)
		for i := range a.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			subscript := lower + int64(i)
			if p.name == "array_position" && subscript < start {
				continue
			}
			match, err := p.equal(ctx, a.ArrayElement(i), search, env)
			if err != nil {
				return Value{}, err
			}
			if !match {
				continue
			}
			position, err := Integer(p.integer, subscript)
			if err != nil {
				return Value{}, err
			}
			if p.name == "array_position" {
				return position, nil
			}
			positions = append(positions, position)
		}
		if p.name == "array_position" {
			return Null(p.result), nil
		}
		var dimensions []ArrayDimension
		if len(positions) > 0 {
			dimensions = []ArrayDimension{{Lower: 1, Length: int32(len(positions))}}
		}
		return OwnedArrayValue(p.result, dimensions, positions)
	case "array_replace":
		return a.MapArray(p.array, func(old Value, _ *Descriptor) (Value, error) {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			match, err := p.equal(ctx, old, search, env)
			if err != nil {
				return Value{}, err
			}
			if match {
				return args[2], nil
			}
			return old, nil
		})
	case "array_remove":
		var survivors []Value
		changed := false
		for i := range a.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			old := a.ArrayElement(i)
			match, err := p.equal(ctx, old, search, env)
			if err != nil {
				return Value{}, err
			}
			if match {
				if !changed {
					changed = true
					if i > 0 {
						survivors = make([]Value, i)
						copy(survivors, a.array.elements[:i])
					}
				}
			} else if changed {
				survivors = append(survivors, old)
			}
		}
		if !changed {
			return a, nil
		}
		var dimensions []ArrayDimension
		if len(survivors) > 0 {
			dimensions = []ArrayDimension{{Lower: a.ArrayDimension(0).Lower, Length: int32(len(survivors))}}
		}
		return OwnedArrayValue(p.array, dimensions, survivors)
	}
	return Value{}, fmt.Errorf("invalid array search plan")
}

func (p ArraySearchPlan) membership(ctx context.Context, left, right Value, env InputEnvironment) (Value, error) {
	if p.name == "<@" {
		left, right = right, left
	}
	for i := range right.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		required := right.ArrayElement(i)
		matched := false
		if !required.IsNull() {
			for j := range left.ArrayLen() {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				candidate := left.ArrayElement(j)
				if candidate.IsNull() {
					continue
				}
				var err error
				matched, err = p.equal(ctx, candidate, required, env)
				if err != nil {
					return Value{}, err
				}
				if matched {
					break
				}
			}
		}
		if p.name == "&&" && matched {
			return Boolean(p.result, true), nil
		}
		if p.name != "&&" && !matched {
			return Boolean(p.result, false), nil
		}
	}
	return Boolean(p.result, p.name != "&&"), nil
}
