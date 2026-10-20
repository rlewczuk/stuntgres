package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// A collection is one PostgreSQL dependency-recording pass. Whole-relation
// references are redundant with attributes only within that pass.
type pgDependRefs struct {
	ctx        context.Context
	catalog    catalog.Read
	definition definitionReader
	addresses  map[ids.ObjectAddress]struct{}
	err        error
}

func (r *pgDependRefs) add(address ids.ObjectAddress) {
	if r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	if address.ObjectID == 0 || pgDependPinned(r.catalog, r.definition, address) {
		return
	}
	whole := address
	whole.SubID = 0
	d, ok := definitionDescriptorAt(r.catalog, r.definition, whole)
	if !ok {
		r.err = fmt.Errorf("stuntgres: missing dependency provider")
		return
	}
	if address.SubID != 0 {
		relation, ok := d.(*catalog.Relation)
		if !ok || address.SubID != -6 && !relation.HasAttribute(address.SubID) {
			r.err = fmt.Errorf("stuntgres: missing dependency attribute")
			return
		}
	}
	if r.addresses == nil {
		r.addresses = make(map[ids.ObjectAddress]struct{})
	}
	r.addresses[address] = struct{}{}
}

func (r *pgDependRefs) expression(expr expr.Expr) {
	if expr == nil || r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	switch n := expr.(type) {
	case plan.SQLRoutineExpr:
		r.add(n.Ref.Address)
	case plan.SelectedOperatorExpr:
		r.add(n.Ref.Address)
	case plan.UnnestExpr:
		if n.Procedure != nil {
			r.add(n.Ref.Address)
		}
	case installedSoundexFunctionExpression:
		r.add(n.ref.Address)
	case installedMetaphoneFunctionExpression:
		r.add(n.ref.Address)
	case installedLevenshteinFunctionExpression:
		r.add(n.ref.Address)
	case installedUUIDOsspFunctionExpression:
		r.add(n.ref.Address)
	case installedCryptoFunctionExpression:
		r.add(n.ref.Address)
	case installedFunctionExpression:
		r.add(n.ref.Address)
	case plan.LiteralExpr:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
		r.add(n.RegclassRef.Address)
		if r.err == nil {
			r.err = walkCatalogAliasDependenciesAt(r.catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkOperatorReferencesAt(r.catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkRegTSReferencesAt(r.catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
	case plan.RowExpr:
		if typ := n.ResultUsage().Type; typ != nil && typ.ID() != 0 {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case plan.CoercionExpr:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case plan.CollationExpr:
		r.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(n.ResultUsage().Collation.ID)})
	case plan.ColumnExpr:
		r.add(n.Origin.Address)
	case plan.TableOIDExpr:
		address := n.Owner.Address
		address.SubID = -6
		r.add(address)
	case functions.SequenceCall:
		r.add(n.Ref.Address)
	case plan.SubqueryExpr:
		r.query(n.QueryPlan.Root)
	}
	// Fixed builtin providers are pinned; installed providers above record their
	// genuine procedure identity. Arguments include every analyzed CASE arm,
	// independently of execution demand.
	boundExpressionChildren(expr, r.expression)
}

func (r *pgDependRefs) query(node plan.Node) {
	if node == nil || r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	if source, ok := node.(*plan.SourceNode); ok {
		r.add(source.Ref.Address)
	}
	if aggregate, ok := node.(*plan.AggregateNode); ok {
		for _, call := range aggregate.Calls {
			if call.Procedure != nil {
				r.add(call.Ref.Address)
			}
		}
	}
	if _, set := node.(*plan.SetNode); set {
		queryExpressions(node, func(expr expr.Expr) {
			// The set binder always installs an outer adaptation wrapper.
			// An unchanged usage is not an analyzed cast dependency; genuine
			// casts remain in its child and still take the ordinary path.
			if wrapper, ok := expr.(plan.CoercionExpr); ok && wrapper.Child.ResultUsage() == wrapper.ResultUsage() {
				r.expression(wrapper.Child)
			} else {
				r.expression(expr)
			}
		})
	} else {
		queryExpressions(node, r.expression)
	}
	left, right := queryInputs(node)
	r.query(left)
	r.query(right)
}

func (r *pgDependRefs) finish() ([]ids.ObjectAddress, error) {
	if r.err != nil {
		return nil, r.err
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	// Remove whole-object references in linear time, not a pairwise scan.
	for address := range r.addresses {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if address.SubID != 0 {
			address.SubID = 0
			delete(r.addresses, address)
		}
	}
	out := make([]ids.ObjectAddress, 0, len(r.addresses))
	for address := range r.addresses {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, address)
	}
	return out, nil
}

func pgDependExpressionRefs(ctx context.Context, catalog catalog.Read, expression expr.Expr) ([]ids.ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog}
	r.expression(expression)
	return r.finish()
}

func pgDependQueryRefs(ctx context.Context, catalog catalog.Read, reader definitionReader, query *plan.Query) ([]ids.ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog, definition: reader}
	if query != nil {
		r.query(query.Root)
	}
	return r.finish()
}
