package stored

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	return routineDefaultsWithChecks(ctx, view, reader, entries, nil)
}

func routineDefaultsWithChecks(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, entries []expr.Expr, checks []catalog.ExpressionDependency) (*RoutineDefaults, error) {
	if view.Owner() == nil {
		return nil, fmt.Errorf("missing routine default catalog owner")
	}
	p := &RoutineDefaults{entries: slices.Clone(entries), domain: view.Owner().Domain, epoch: view.Epoch()}
	for _, node := range entries {
		if node == nil {
			continue
		}
		var refs []ids.ObjectRef
		purposes := make(map[ids.ObjectRef]catalog.DependencyPurpose)
		if _, err := (ScalarRenderer{Definition: reader, RoutineDefault: true, Dependencies: &refs, DependencyPurposes: purposes, CapturedChecks: checks}).SQL(ctx, view, node); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			owner := ref.Address
			owner.SubID = 0
			d, ok := catalog.DescriptorAt(view, reader, owner)
			if !ok {
				return nil, fmt.Errorf("missing routine default dependency")
			}
			if ref.Address.SubID != 0 {
				relation, ok := d.(*catalog.Relation)
				if !ok || !relation.HasAttribute(ref.Address.SubID) {
					return nil, fmt.Errorf("missing routine default dependency attribute")
				}
			}
			retained := false
			for _, check := range checks {
				if check.Ref == ref {
					p.dependencies = append(p.dependencies, check)
					retained = true
				}
			}
			if !retained {
				p.dependencies = append(p.dependencies, catalog.ExpressionDependency{Ref: ref, Revision: d.Revision(), Purpose: purposes[ref]})
			}
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
	if err := p.Validate(view); err != nil {
		return "", err
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
	for _, dependency := range p.dependencies {
		if dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if dependency.Purpose != catalog.DefinitionDependency && dependency.Purpose != catalog.OperandDependency || dependency.Revision == 0 {
			return fmt.Errorf("invalid routine default capture purpose")
		}
	}
	next, err := routineDefaultsWithChecks(context.Background(), view, nil, p.entries, p.dependencies)
	if err != nil {
		return err
	}
	if !slices.Equal(next.dependencies, p.dependencies) {
		return fmt.Errorf("foreign routine default capture")
	}
	return nil
}
func (p *RoutineDefaults) Rebind(view catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, remap *catalog.RoutineRemap) (catalog.RoutineDefaults, error) {
	if p.domain == nil {
		return nil, ids.ErrCatalogReplaced
	}
	for _, dependency := range p.dependencies {
		if dependency.Ref.Domain() != p.domain || dependency.Ref.Epoch != p.epoch {
			return nil, ids.ErrCatalogReplaced
		}
		if dependency.Purpose != catalog.DefinitionDependency && dependency.Purpose != catalog.OperandDependency || dependency.Revision == 0 {
			return nil, fmt.Errorf("invalid routine default capture purpose")
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
	typ, collation, procedure := remap.Type, remap.Collation, remap.Procedure
	mapper := &ViewQueryRemapper{Catalog: view, Ref: ref, Type: typ, Collation: collation, Procedure: procedure, addressMapper: addressMapper}
	entries := make([]expr.Expr, len(p.entries))
	for i, old := range p.entries {
		if old == nil {
			continue
		}
		// Regclass datum OIDs are address projections, not lifetime-bearing
		// references. Only real captured ObjectRefs enter the checked mapper.
		node, err := remapScalar(old, ref, ref, nil, mapper.Nested, 0, addressMapper)
		if err != nil {
			return nil, err
		}
		entries[i], _, err = mapper.retag(node, typ)
		if err != nil {
			return nil, err
		}
	}
	if invalid {
		return nil, ids.ErrCatalogReplaced
	}
	dependencies := slices.Clone(p.dependencies)
	for i := range dependencies {
		dependencies[i].Ref = ref(dependencies[i].Ref)
	}
	next, err := routineDefaultsWithChecks(context.Background(), view, nil, entries, dependencies)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(next.dependencies, routineDependencySet(dependencies)) {
		return nil, fmt.Errorf("foreign routine default capture")
	}
	return next, nil
}
