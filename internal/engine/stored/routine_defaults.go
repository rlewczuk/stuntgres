package stored

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RoutineDefaults owns typed immutable declaration expressions and their hard
// captures. Entries use input slots, never all-formal or written-call indexes.
type RoutineDefaults struct {
	entries      []expr.Expr
	dependencies []catalog.ExpressionDependency
	domain       *ids.Domain
	epoch        ids.CatalogEpoch
}

func NewRoutineDefaults(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, entries []expr.Expr) (*RoutineDefaults, error) {
	if view.Owner() == nil {
		return nil, fmt.Errorf("missing routine default catalog owner")
	}
	p := &RoutineDefaults{entries: slices.Clone(entries), domain: view.Owner().Domain, epoch: view.Epoch()}
	for _, node := range entries {
		if node == nil {
			continue
		}
		var refs []ids.ObjectRef
		if _, err := (ScalarRenderer{Definition: reader, RoutineDefault: true, Dependencies: &refs}).SQL(ctx, view, node); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			d, ok := catalog.DescriptorAt(view, reader, ref.Address)
			if !ok {
				return nil, fmt.Errorf("missing routine default dependency")
			}
			p.dependencies = append(p.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: d.Revision()})
		}
	}
	p.dependencies = routineDependencySet(p.dependencies)
	return p, nil
}
func (p *RoutineDefaults) Default(slot int) (expr.Expr, bool) {
	if slot < 0 || slot >= len(p.entries) || p.entries[slot] == nil {
		return nil, false
	}
	return p.entries[slot], true
}
func (p *RoutineDefaults) Dependencies() []catalog.ExpressionDependency { return p.dependencies }
func (p *RoutineDefaults) DefaultSQL(ctx context.Context, view catalog.Read, slot int) (string, error) {
	node, ok := p.Default(slot)
	if !ok {
		return "", fmt.Errorf("missing routine declaration default")
	}
	return (ScalarRenderer{RoutineDefault: true, CapturedChecks: p.dependencies}).SQL(ctx, view, node)
}
func (p *RoutineDefaults) ValidateDefinition(d *catalog.Procedure) error {
	if d.SQL == nil || len(d.SQL.Inputs) != len(p.entries) || len(d.SQL.InputFormals) != len(p.entries) {
		return fmt.Errorf("invalid routine default layout")
	}
	for slot, formal := range d.SQL.InputFormals {
		if formal < 0 || formal >= len(d.SQL.Formals) || !d.SQL.Formals[formal].Input() {
			return fmt.Errorf("invalid routine default formal projection")
		}
		node, ok := p.Default(slot)
		if ok != d.SQL.Formals[formal].HasDefault || ok && node.ResultUsage() != d.SQL.Inputs[slot] {
			return fmt.Errorf("inconsistent routine declaration default")
		}
	}
	return nil
}
func (p *RoutineDefaults) Validate(view catalog.Read) error {
	if view.Owner() == nil || p.domain != view.Owner().Domain || p.epoch != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	next, err := NewRoutineDefaults(context.Background(), view, nil, p.entries)
	if err != nil {
		return err
	}
	if !slices.Equal(next.dependencies, p.dependencies) {
		return fmt.Errorf("foreign routine default capture")
	}
	for _, dependency := range p.dependencies {
		if dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
func (p *RoutineDefaults) Rebind(view catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress) (catalog.RoutineDefaults, error) {
	if p.domain == nil {
		return nil, ids.ErrCatalogReplaced
	}
	for _, dependency := range p.dependencies {
		if dependency.Ref.Domain() != p.domain || dependency.Ref.Epoch != p.epoch {
			return nil, ids.ErrCatalogReplaced
		}
	}
	invalid := false
	addressMapper := func(address ids.ObjectAddress) ids.ObjectAddress {
		base := address
		base.SubID = 0
		if next, ok := mapping[base]; ok {
			address.ClassID, address.ObjectID = next.ClassID, next.ObjectID
		}
		return address
	}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		if old.Domain() != p.domain || old.Epoch != p.epoch {
			invalid = true
		}
		return view.Owner().Domain.Ref(view.Epoch(), addressMapper(old.Address))
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}
		if next, ok := mapping[address]; ok {
			address = next
		}
		current, _ := view.LookupType(sqltype.ID(address.ObjectID))
		return current
	}
	entries := make([]expr.Expr, len(p.entries))
	for i, old := range p.entries {
		if old == nil {
			continue
		}
		// Regclass datum OIDs are address projections, not lifetime-bearing
		// references. Only real captured ObjectRefs enter the checked mapper.
		node, err := remapScalar(old, ref, ref, nil, nil, 0, addressMapper)
		if err != nil {
			return nil, err
		}
		entries[i], _, err = RetagTypes(view, node, typ)
		if err != nil {
			return nil, err
		}
	}
	if invalid {
		return nil, ids.ErrCatalogReplaced
	}
	return NewRoutineDefaults(context.Background(), view, nil, entries)
}
