package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
)

// A collection is one PostgreSQL dependency-recording pass. Whole-relation
// references are redundant with attributes only within that pass.
type DependRefs struct {
	Context    context.Context
	Catalog    catalog.Read
	definition catalog.DefinitionReader
	Addresses  map[ids.ObjectAddress]struct{}
	Err        error
}

func (r *DependRefs) Add(address ids.ObjectAddress) {
	if r.Err != nil {
		return
	}
	if r.Err = r.Context.Err(); r.Err != nil {
		return
	}
	if address.ObjectID == 0 || DependPinned(r.Catalog, r.definition, address) {
		return
	}
	whole := address
	whole.SubID = 0
	d, ok := catalog.DescriptorAt(r.Catalog, r.definition, whole)
	if !ok {
		r.Err = fmt.Errorf("stuntgres: missing dependency provider")
		return
	}
	if address.SubID != 0 {
		relation, ok := d.(*catalog.Relation)
		if !ok || address.SubID != -6 && !relation.HasAttribute(address.SubID) {
			r.Err = fmt.Errorf("stuntgres: missing dependency attribute")
			return
		}
	}
	if r.Addresses == nil {
		r.Addresses = make(map[ids.ObjectAddress]struct{})
	}
	r.Addresses[address] = struct{}{}
}

func (r *DependRefs) Expression(expr expr.Expr) {
	if expr == nil || r.Err != nil {
		return
	}
	if r.Err = r.Context.Err(); r.Err != nil {
		return
	}
	switch n := expr.(type) {
	case plan.SQLRoutineExpr:
		r.Add(n.Ref.Address)
	case plan.SelectedOperatorExpr:
		r.Add(n.Ref.Address)
	case plan.UnnestExpr:
		if n.Procedure != nil {
			r.Add(n.Ref.Address)
		}
	case plan.InstalledSoundexExpr:
		r.Add(n.Ref.Address)
	case plan.InstalledMetaphoneExpr:
		r.Add(n.Ref.Address)
	case plan.InstalledLevenshteinExpr:
		r.Add(n.Ref.Address)
	case plan.InstalledUUIDOsspExpr:
		r.Add(n.Ref.Address)
	case plan.InstalledCryptoExpr:
		r.Add(n.Ref.Address)
	case plan.InstalledTextExpr:
		r.Add(n.Ref.Address)
	case plan.LiteralExpr:
		if typ := n.ResultUsage().Type; typ != nil {
			r.Add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
		r.Add(n.RegclassRef.Address)
		if r.Err == nil {
			r.Err = catalog.WalkAliasDependenciesAt(r.Catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.Add(address)
				return r.Err
			})
		}
		if r.Err == nil {
			r.Err = regobj.WalkOperatorReferencesAt(r.Catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.Add(address)
				return r.Err
			})
		}
		if r.Err == nil {
			r.Err = regobj.WalkTextSearchReferencesAt(r.Catalog, r.definition, n.Value, func(address ids.ObjectAddress) error {
				r.Add(address)
				return r.Err
			})
		}
	case plan.RowExpr:
		if typ := n.ResultUsage().Type; typ != nil && typ.ID() != 0 {
			r.Add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case plan.CoercionExpr:
		if typ := n.ResultUsage().Type; typ != nil {
			r.Add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		}
	case plan.CollationExpr:
		r.Add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(n.ResultUsage().Collation.ID)})
	case plan.ColumnExpr:
		r.Add(n.Origin.Address)
	case plan.TableOIDExpr:
		address := n.Owner.Address
		address.SubID = -6
		r.Add(address)
	case functions.SequenceCall:
		r.Add(n.Ref.Address)
	case plan.SubqueryExpr:
		r.Query(n.QueryPlan.Root)
	}
	// Fixed builtin providers are pinned; installed providers above record their
	// genuine procedure identity. Arguments include every analyzed CASE arm,
	// independently of execution demand.
	plan.ExpressionChildren(expr, r.Expression)
}

func (r *DependRefs) Query(node plan.Node) {
	if node == nil || r.Err != nil {
		return
	}
	if r.Err = r.Context.Err(); r.Err != nil {
		return
	}
	if source, ok := node.(*plan.SourceNode); ok {
		r.Add(source.Ref.Address)
	}
	if aggregate, ok := node.(*plan.AggregateNode); ok {
		for _, call := range aggregate.Calls {
			if call.Procedure != nil {
				r.Add(call.Ref.Address)
			}
		}
	}
	if _, set := node.(*plan.SetNode); set {
		plan.QueryExpressions(node, func(expr expr.Expr) {
			// The set binder always installs an outer adaptation wrapper.
			// An unchanged usage is not an analyzed cast dependency; genuine
			// casts remain in its child and still take the ordinary path.
			if wrapper, ok := expr.(plan.CoercionExpr); ok && wrapper.Child.ResultUsage() == wrapper.ResultUsage() {
				r.Expression(wrapper.Child)
			} else {
				r.Expression(expr)
			}
		})
	} else {
		plan.QueryExpressions(node, r.Expression)
	}
	left, right := plan.QueryInputs(node)
	r.Query(left)
	r.Query(right)
}

func (r *DependRefs) Finish() ([]ids.ObjectAddress, error) {
	if r.Err != nil {
		return nil, r.Err
	}
	if err := r.Context.Err(); err != nil {
		return nil, err
	}
	// Remove whole-object references in linear time, not a pairwise scan.
	for address := range r.Addresses {
		if err := r.Context.Err(); err != nil {
			return nil, err
		}
		if address.SubID != 0 {
			address.SubID = 0
			delete(r.Addresses, address)
		}
	}
	out := make([]ids.ObjectAddress, 0, len(r.Addresses))
	for address := range r.Addresses {
		if err := r.Context.Err(); err != nil {
			return nil, err
		}
		out = append(out, address)
	}
	return out, nil
}

func ExpressionDependRefs(ctx context.Context, catalog catalog.Read, expression expr.Expr) ([]ids.ObjectAddress, error) {
	r := DependRefs{Context: ctx, Catalog: catalog}
	r.Expression(expression)
	return r.Finish()
}

func QueryDependRefs(ctx context.Context, catalog catalog.Read, reader catalog.DefinitionReader, query *plan.Query) ([]ids.ObjectAddress, error) {
	r := DependRefs{Context: ctx, Catalog: catalog, definition: reader}
	if query != nil {
		r.Query(query.Root)
	}
	return r.Finish()
}

// IndexDependRefs records an index definition's pg_depend references: its
// key expressions and key collations.
func IndexDependRefs(ctx context.Context, view catalog.Read, definition *catalog.IndexDefinition) ([]ids.ObjectAddress, error) {
	refs := DependRefs{Context: ctx, Catalog: view}
	for _, key := range definition.Keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if key.Derived != nil {
			refs.Expression(key.Derived.Definition)
		}
		refs.Add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
	}
	return refs.Finish()
}

// PartitionDependRefs records a partitioned relation's pg_depend references:
// key columns, key expressions, key types and collations.
func PartitionDependRefs(ctx context.Context, view catalog.Read, relation *catalog.Relation) ([]ids.ObjectAddress, error) {
	refs := DependRefs{Context: ctx, Catalog: view}
	for _, key := range relation.Partition.Keys {
		if key.Attribute != 0 {
			address := relation.Address()
			address.SubID = key.Attribute
			refs.Add(address)
		}
		if key.Derived != nil {
			refs.Expression(key.Derived.Definition)
		}
		refs.Add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(key.Usage.Type.ID())})
		refs.Add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
		refs.Add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Usage.Collation.ID)})
	}
	return refs.Finish()
}
