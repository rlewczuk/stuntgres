package engine

import (
	"context"
	"fmt"
)

// A collection is one PostgreSQL dependency-recording pass. Whole-relation
// references are redundant with attributes only within that pass.
type pgDependRefs struct {
	ctx       context.Context
	catalog   CatalogRead
	addresses map[ObjectAddress]struct{}
	err       error
}

func (r *pgDependRefs) add(address ObjectAddress) {
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
		r.addresses = make(map[ObjectAddress]struct{})
	}
	r.addresses[address] = struct{}{}
}

func (r *pgDependRefs) expression(expr BoundExpr) {
	if expr == nil || r.err != nil {
		return
	}
	if r.err = r.ctx.Err(); r.err != nil {
		return
	}
	switch n := expr.(type) {
	case installedFunctionExpression:
		r.add(n.ref.Address)
	case literalExpression:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())})
		}
		r.add(n.regclassRef.Address)
		if r.err == nil {
			r.err = walkCatalogAliasDependencies(r.catalog, n.value, func(address ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkOperatorReferences(r.catalog, n.value, func(address ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
		if r.err == nil {
			r.err = walkRegTSReferences(r.catalog, n.value, func(address ObjectAddress) error {
				r.add(address)
				return r.err
			})
		}
	case coercionExpression:
		if typ := n.ResultUsage().Type; typ != nil {
			r.add(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())})
		}
	case collationExpression:
		r.add(ObjectAddress{ClassID: CollationClassID, ObjectID: OID(n.ResultUsage().Collation.ID)})
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
	if _, set := node.(*setNode); set {
		queryExpressions(node, func(expr BoundExpr) {
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

func (r *pgDependRefs) finish() ([]ObjectAddress, error) {
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
	out := make([]ObjectAddress, 0, len(r.addresses))
	for address := range r.addresses {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, address)
	}
	return out, nil
}

func pgDependExpressionRefs(ctx context.Context, catalog CatalogRead, expression BoundExpr) ([]ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog}
	r.expression(expression)
	return r.finish()
}

func pgDependQueryRefs(ctx context.Context, catalog CatalogRead, query *BoundQuery) ([]ObjectAddress, error) {
	r := pgDependRefs{ctx: ctx, catalog: catalog}
	if query != nil {
		r.query(query.Root)
	}
	return r.finish()
}
