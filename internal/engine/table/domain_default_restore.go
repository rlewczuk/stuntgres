package table

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

// CaptureDomainReplayNode captures the shared typed control-value construction
// facts used by row replay and retained expression statistics.
func CaptureDomainReplayNode(ctx context.Context, view catalog.Read, u sqltype.Usage, v sqltype.Value) (typedef.DomainReplayNode, error) {
	return captureDomainReplayNode(ctx, view, u, v, false)
}

// CaptureStatisticsReplayNode alone admits retained structural tree output.
// This does not grant ordinary row replay or SQL input authority.
func CaptureStatisticsReplayNode(ctx context.Context, view catalog.Read, u sqltype.Usage, v sqltype.Value) (typedef.DomainReplayNode, error) {
	return captureDomainReplayNode(ctx, view, u, v, true)
}

func captureDomainReplayNode(ctx context.Context, view catalog.Read, u sqltype.Usage, v sqltype.Value, statistics bool) (typedef.DomainReplayNode, error) {
	if err := ctx.Err(); err != nil {
		return typedef.DomainReplayNode{}, err
	}
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
		child, err := captureDomainReplayNode(ctx, view, u.BaseUsage(), base, statistics)
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
			elements[i], err = captureDomainReplayNode(ctx, view, element, v.ArrayElement(i), statistics)
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
			child, err := captureDomainReplayNode(ctx, view, field.Usage, v.CompositeField(i), statistics)
			if err != nil {
				return n, err
			}
			members[i] = typedef.DomainReplayMember{Name: field.Name, Value: &child}
		}
		return typedef.DomainReplayNode{Kind: "composite", Members: &members}, nil
	}
	if u.Type.IsSeg() {
		payload, err := v.SegDatum()
		if err != nil {
			return n, err
		}
		return typedef.DomainReplayNode{Kind: "seg", Seg: &payload}, nil
	}
	if statistics && u.Type.ID() == sqltype.PgNodeTree {
		current, ok := view.LookupType(sqltype.PgNodeTree)
		if !ok || current != u.Type {
			return n, fmt.Errorf("stuntgres: invalid retained tree authority")
		}
		text, err := u.Type.Output(ctx, v)
		if err != nil {
			return n, err
		}
		return typedef.DomainReplayNode{Kind: "statistics_tree", StatisticsTree: &text}, nil
	}
	literal, err := regobj.Literal(ctx, view, plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: v})
	if err != nil {
		return n, err
	}
	return typedef.DomainReplayNode{Kind: "scalar", Scalar: &literal}, nil
}
