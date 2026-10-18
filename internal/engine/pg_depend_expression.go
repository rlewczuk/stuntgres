package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// A collection is one PostgreSQL dependency-recording pass. Whole-relation
// references are redundant with attributes only within that pass.
type pgDependRefs struct {
	ctx       context.Context
	catalog   CatalogRead
	addresses map[ids.ObjectAddress]struct{}
	err       error
}

func (r *pgDependRefs) add(address ids.ObjectAddress) {
	if r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	if address.ObjectID == 0 || pgDependPinned(r.catalog, address) {
		return
	}
	whole := address
	whole.SubID = 0
	d, ok := r.catalog.Descriptor(whole)
	if !ok {
		r.err = fmt.Errorf("stuntgres: missing dependency provider")
		return
	}
	if address.SubID != 0 {
		relation, ok := d.(*relationDescriptor)
		if !ok || address.SubID != -6 && !relation.hasAttribute(address.SubID) {
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
	case selectedOperatorExpression:
		r.add(n.ref.Address)
	case unnestExpression:
		if n.procedure != nil {
			r.add(n.ref.Address)
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
	case literalExpression:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
		r.add(n.RegclassRef.Address)
		if r.err == nil {
			r.err = walkCatalogAliasDependencies(r.catalog, n.value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkOperatorReferences(r.catalog, n.value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkRegTSReferences(r.catalog, n.value, func(address ids.ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
	case rowExpression:
		if typ := n.ResultUsage().Type; typ != nil && typ.ID() != 0 {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case coercionExpression:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case collationExpression:
		r.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(n.ResultUsage().Collation.ID)})
	case columnExpression:
		r.add(n.origin.Address)
	case tableOIDExpression:
		address := n.owner.Address
		address.SubID = -6
		r.add(address)
	case sequenceCallExpression:
		r.add(n.ref.Address)
	case subqueryExpression:
		r.query(n.queryPlan.Root)
	}
	// Fixed builtin providers are pinned; installed providers above record their
	// genuine procedure identity. Arguments include every analyzed CASE arm,
	// independently of execution demand.
	boundExpressionChildren(expr, r.expression)
}

func (r *pgDependRefs) query(node LogicalNode) {
	if node == nil || r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	if source, ok := node.(*sourceNode); ok {
		r.add(source.ref.Address)
	}
	if aggregate, ok := node.(*aggregateNode); ok {
		for _, call := range aggregate.calls {
			if call.procedure != nil {
				r.add(call.ref.Address)
			}
		}
	}
	if _, set := node.(*setNode); set {
		queryExpressions(node, func(expr expr.Expr) {
			// The set binder always installs an outer adaptation wrapper.
			// An unchanged usage is not an analyzed cast dependency; genuine
			// casts remain in its child and still take the ordinary path.
			if wrapper, ok := expr.(coercionExpression); ok && wrapper.child.ResultUsage() == wrapper.ResultUsage() {
				r.expression(wrapper.child)
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

func pgDependExpressionRefs(ctx context.Context, catalog CatalogRead, expression expr.Expr) ([]ids.ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog}
	r.expression(expression)
	return r.finish()
}

func pgDependQueryRefs(ctx context.Context, catalog CatalogRead, query *BoundQuery) ([]ids.ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog}
	if query != nil {
		r.query(query.Root)
	}
	return r.finish()
}
