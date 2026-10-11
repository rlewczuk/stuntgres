package sqltype

import (
	"context"
	"fmt"
)

// IndexForeignKeyComparison belongs to the selected supporting class. Operator
// family membership, not general predicate comparability, admits cross inputs.
// Coercion targets are unsized released class inputs, never the parent typmod.
// Original domain values retain their storage authority; comparison strips only
// their already-validated wrappers.
type IndexForeignKeyComparison struct {
	parent, child                 *Descriptor
	operatorParent, operatorChild *Descriptor
	parentCast, childCast         CoercionPlan
	parentConvert, childConvert   bool
	cross                         bool
	comparison                    Comparison
	childComparison               Comparison
	index                         IndexComparison
}

// InputTypes identifies the selected operator inputs for released RI query
// diagnostics. It is comparison authority, not destination assignment typing.
func (p IndexForeignKeyComparison) InputTypes() (*Descriptor, *Descriptor) {
	return p.operatorParent, p.operatorChild
}

func BindIndexForeignKey(parent, child Usage, class IndexClass, lookup func(ID) (*Descriptor, bool)) (IndexForeignKeyComparison, error) {
	if _, err := BindIndexOrdering(parent, class); err != nil {
		return IndexForeignKeyComparison{}, err
	}
	parent, child = parent.BaseUsage(), child.BaseUsage()
	for _, typ := range [2]*Descriptor{parent.Type, child.Type} {
		current, ok := lookup(typ.id)
		if !ok || current != typ {
			return IndexForeignKeyComparison{}, failure("42804", "incompatible foreign-key descriptor generation")
		}
	}
	plan := IndexForeignKeyComparison{parent: parent.Type, child: child.Type, operatorParent: parent.Type, operatorChild: child.Type}
	if !class.enum {
		input, ok := lookup(class.input)
		if !ok || !input.Concrete() {
			return plan, fmt.Errorf("missing foreign-key class input descriptor")
		}
		plan.operatorParent, plan.operatorChild = input, input
		if indexFamilyEquality(class.Family, class.input, child.Type.id) {
			plan.operatorChild = child.Type
		}
	}
	// Binary-compatible same-base inputs already have the selected index's
	// equality semantics, including enums and CIDR through inet_ops.
	if parent.Type == child.Type {
		var err error
		plan.index, err = BindIndexOrdering(parent, class)
		return plan, err
	}
	if class.enum {
		return plan, failure("42804", "incompatible foreign-key index inputs")
	}
	input, ok := lookup(class.input)
	if !ok || !input.Concrete() {
		return plan, fmt.Errorf("missing foreign-key class input descriptor")
	}
	parentInput, childInput := input.Usage(), input.Usage()
	// Foreign-key probes use the supporting index's comparison authority.
	parentInput.Collation = parent.Collation
	if indexFamilyEquality(class.Family, class.input, child.Type.id) {
		// Preserve genuine mixed-width members: no narrowing int8 into int4.
		childInput = child.Type.Usage()
		plan.cross = true
	}
	var err error
	plan.parentCast, err = PlanCoercion(parent, parentInput, Implicit)
	if err != nil {
		return plan, err
	}
	plan.childCast, err = PlanCoercion(child, childInput, Implicit)
	if err != nil {
		return plan, err
	}
	plan.parentConvert, plan.childConvert = parent.Type != parentInput.Type, child.Type != childInput.Type
	if plan.cross {
		plan.comparison, err = BindComparison(parentInput.Type, childInput.Type, parent.Collation, false)
		if err == nil {
			plan.childComparison, err = BindComparison(childInput.Type, childInput.Type, parent.Collation, false)
		}
	} else {
		plan.index, err = BindIndexOrdering(parentInput, class)
	}
	return plan, err
}

// Released B-tree families with implemented cross-input members. Same-input
// members use the index provider above. Both pfeq and ffeq must exist; these
// families contain the corresponding same-child equality as well.
func indexFamilyEquality(family string, parent, child ID) bool {
	if parent == child {
		return true
	}
	switch family {
	case "integer_ops":
		integer := func(id ID) bool { return id == Int2 || id == Int4 || id == Int8 }
		return integer(parent) && integer(child)
	case "float_ops":
		return isFloat(parent) && isFloat(child)
	case "datetime_ops":
		return dateTimestampType(parent) && dateTimestampType(child)
	case "text_ops":
		text := func(id ID) bool { return id == Text || id == Name }
		return text(parent) && text(child)
	}
	return false
}

// PrepareChild applies the admitted input coercion once for an ordered probe.
func (p IndexForeignKeyComparison) PrepareChild(ctx context.Context, child Value, input InputEnvironment) (Value, error) {
	child, err := child.BaseValue()
	if err != nil {
		return Value{}, err
	}
	if child.Type() != p.child {
		return Value{}, fmt.Errorf("invalid foreign-key child generation")
	}
	if p.childConvert {
		return p.childCast.Apply(ctx, child, input, OutputEnvironment{})
	}
	return child, nil
}

// Compare orders an index-side parent against an ordinary referencing value.
func (p IndexForeignKeyComparison) Compare(ctx context.Context, parent, child Value, input InputEnvironment) (int, error) {
	child, err := p.PrepareChild(ctx, child, input)
	if err != nil {
		return 0, err
	}
	return p.ComparePrepared(ctx, parent, child, input)
}

// ComparePrepared preserves provider/generation authority while avoiding a
// repeated child cast for every entry visited by a lower-bound/equal-run probe.
func (p IndexForeignKeyComparison) ComparePrepared(ctx context.Context, parent, child Value, input InputEnvironment) (int, error) {
	parent, err := parent.BaseValue()
	if err != nil {
		return 0, err
	}
	childType := p.child
	if p.childConvert {
		childType = p.childCast.target.Type
	}
	if parent.Type() != p.parent || child.Type() != childType {
		return 0, fmt.Errorf("invalid foreign-key comparison generation")
	}
	if p.parentConvert {
		parent, err = p.parentCast.Apply(ctx, parent, input, OutputEnvironment{})
		if err != nil {
			return 0, err
		}
	}
	if p.cross {
		return p.comparison.Compare(ctx, parent, child, input)
	}
	return p.index.Compare(ctx, parent, child, input)
}

// EqualChildren uses the admitted family's same-child equality, including
// cross-width members. It never narrows a child into the parent's storage type.
func (p IndexForeignKeyComparison) EqualChildren(ctx context.Context, before, after Value, input InputEnvironment) (bool, error) {
	if before.IsNull() || after.IsNull() {
		return false, nil
	}
	left, err := p.PrepareChild(ctx, before, input)
	if err != nil {
		return false, err
	}
	right, err := p.PrepareChild(ctx, after, input)
	if err != nil {
		return false, err
	}
	var comparison int
	if p.cross {
		comparison, err = p.childComparison.Compare(ctx, left, right, input)
	} else {
		comparison, err = p.index.Compare(ctx, left, right, input)
	}
	return comparison == 0, err
}
