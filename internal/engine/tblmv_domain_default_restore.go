package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func captureDomainReplayNode(ctx context.Context, view catalog.Read, u sqltype.Usage, v sqltype.Value) (typedef.DomainReplayNode, error) {
	if !u.Valid() || v.Type() != u.Type {
		return typedef.DomainReplayNode{}, fmt.Errorf("stuntgres: invalid native captured cell type")
	}
	n := typedef.DomainReplayNode{Kind: "null"}
	if v.IsNull() {
		if u.Type.IsDomain() && u.Type.HasDomainConstraints() {
			identity, err := typedef.DomainReplayIdentity(view, u.Type)
			if err != nil {
				return n, err
			}
			n.Kind = "domain_null"
			n.Type = &identity
		}
		return n, nil
	}
	if u.Type.IsDomain() {
		identity, err := typedef.DomainReplayIdentity(view, u.Type)
		if err != nil {
			return n, err
		}
		base, err := v.BaseValue()
		if err != nil {
			return n, err
		}
		child, err := captureDomainReplayNode(ctx, view, u.BaseUsage(), base)
		if err != nil {
			return n, err
		}
		return typedef.DomainReplayNode{Kind: "domain", Type: &identity, Base: &child}, nil
	}
	if u.Type.IsArray() {
		dimensions := make([]typedef.DomainReplayDimension, v.ArrayRank())
		for i := range dimensions {
			dimension := v.ArrayDimension(i)
			dimensions[i] = typedef.DomainReplayDimension{Lower: dimension.Lower, Length: dimension.Length}
		}
		elements := make([]typedef.DomainReplayNode, v.ArrayLen())
		element := u
		element.Type = u.Type.Element()
		for i := range elements {
			var err error
			elements[i], err = captureDomainReplayNode(ctx, view, element, v.ArrayElement(i))
			if err != nil {
				return n, err
			}
		}
		return typedef.DomainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements}, nil
	}
	if u.Type.IsComposite() {
		members := make([]typedef.DomainReplayMember, v.CompositeLen())
		for i := range members {
			field := u.Type.CompositeMember(i)
			child, err := captureDomainReplayNode(ctx, view, field.Usage, v.CompositeField(i))
			if err != nil {
				return n, err
			}
			members[i] = typedef.DomainReplayMember{Name: field.Name, Value: &child}
		}
		return typedef.DomainReplayNode{Kind: "composite", Members: &members}, nil
	}
	literal, err := regobj.Literal(ctx, view, plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: v})
	if err != nil {
		return n, err
	}
	return typedef.DomainReplayNode{Kind: "scalar", Scalar: &literal}, nil
}
