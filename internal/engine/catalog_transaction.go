package engine

import (
	"context"
	"fmt"
	"maps"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) privateCatalogLocked(c *session.Journal) catalog.Read {
	if c == nil {
		return e.catalog.read(e.catalog.state)
	}
	if c.CachedBase == e.catalog.state && c.Cached.State() != nil {
		return c.Cached
	}
	if len(c.Descriptors) == 0 && len(c.DefaultACLs) == 0 {
		c.CachedBase = e.catalog.state
		c.Cached = e.catalog.read(e.catalog.state)
		return c.Cached
	}
	n := catalog.CloneState(e.catalog.state)
	n.OverlayDefaultACLs(e.catalog.state, c.DefaultACLs)
	operators := catalog.OperatorIndexEdit{Index: e.catalog.state.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: e.catalog.state.ProcedureNames()}
	for a, d := range c.Descriptors {
		operators.Replace(e.catalog.state.Get(a), d)
		procedures.Replace(e.catalog.state.Get(a), d)
		if old := n.Get(a); old != nil {
			n.UnbindName(catalog.DescriptorNameKey(old))
		}
		if d == nil {
			n.RemoveDescriptor(a)
			n.RemoveDumpPriority(a)
		} else {
			n.SetDescriptor(a, d)
		}
	}
	for _, d := range c.Descriptors {
		if d != nil {
			n.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	n.SetOperatorNames(operators.Index)
	n.SetProcedureNames(procedures.Index)
	c.CachedBase, c.Cached = e.catalog.state, e.catalog.read(n)
	return c.Cached
}
func (e *Engine) catalogChangeLocked(c *session.Journal, a ids.ObjectAddress, d catalog.Descriptor) {
	if _, ok := c.Originals[a]; !ok {
		c.Originals[a] = e.catalog.state.Get(a)
	}
	if original := c.Originals[a]; original != nil {
		k := catalog.DescriptorNameKey(original)
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = e.catalog.state.NameAddress(k)
		}
	}
	if d != nil {
		k := catalog.DescriptorNameKey(d)
		if _, ok := c.Names[k]; !ok {
			c.Names[k] = e.catalog.state.NameAddress(k)
		}
	}
	c.Descriptors[a] = d
	if d == nil {
		JournalOf(c.Indexes).Forget(a)
	}
	c.CachedBase = nil
	c.Cached = catalog.Read{}
}
func (e *Engine) sequenceGeneratorLocked(s *StatementScope, a ids.ObjectAddress) *storage.SequenceGenerator {
	if s.catalog != nil {
		if descriptor, exists := s.catalog.Descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := s.catalog.Generators[a]; g != nil {
			return g
		}
	}
	if c := s.owner.Catalog; c != nil {
		if descriptor, exists := c.Descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := c.Generators[a]; g != nil {
			return g
		}
	}
	return e.storage.Sequences[a]
}
func (e *Engine) preparePrivateCatalog(ctx context.Context, c *session.Journal, epoch ids.CatalogEpoch) (*catalog.State, error) {
	e.mu.Lock()
	// Bind the overlay only to its owning incarnation. Replacement types must
	// never validate old descriptors before the publication epoch fence.
	if e.catalog.state.Epoch() != epoch {
		e.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	if err := catalogDependencyPreflight(e.catalog.state, view.State(), c.Dependencies, c.Originals); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()
	descriptors := maps.All(c.Descriptors)
	if c.DomainChanged {
		descriptors = view.Descriptors()
	}
	for a, d := range descriptors {
		if d == nil {
			continue
		}
		p := e.catalog.Provider(a.ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class")
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
	}
	return view.State(), ctx.Err()
}
func (e *Engine) catalogPreflightLocked(c *session.Journal) error {
	if c.RolesBase != nil && e.roles.published != c.RolesBase {
		return catalog.Conflict{}
	}
	if !catalog.DefaultRecordsUnchanged(e.catalog.state, c.DefaultOriginals) || !catalog.DefaultRecordsUnchanged(e.catalog.state, c.DefaultReads) {
		return catalog.Conflict{}
	}
	roles := e.roles.published
	if c.Roles != nil {
		roles = c.Roles
	}
	view := e.privateCatalogLocked(c)
	read := e.roles.read(e.catalog.owner(), view, roles)
	if err := catalog.ValidateDefaultACLs(read, view); err != nil {
		return err
	}
	if err := e.catalogReadPreflightLocked(c); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceExtensionAuthority); err != nil {
		return err
	}
	if err := catalogDependencyPreflight(e.catalog.state, view.State(), c.Dependencies, c.Originals); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceTruncateGraph); err != nil {
		return err
	}
	for a, shape := range c.Storage {
		store := e.storage.Relations[a]
		if store != shape.Original || store != nil && (store.Generation != shape.Generation || len(store.Versions) != len(shape.Root) || store.Ends != shape.Ends || !sameVersionRoot(store.Versions, shape.Root)) {
			return catalog.Conflict{}
		}
	}
	if err := e.catalogFencesCurrentLocked(c, fenceMaterializedSource); err != nil {
		return err
	}
	for a, original := range c.Originals {
		if e.catalog.state.Get(a) != original {
			return catalog.Conflict{}
		}
	}
	for k, a := range c.Names {
		if e.catalog.state.NameAddress(k) != a {
			return catalog.Conflict{}
		}
	}
	for _, d := range c.Descriptors {
		if d != nil {
			if err := access.ValidateACL(read, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// Inspect only committed descriptors changed since a captured retirement.
// Candidate overrides matter: an owner removed or rewired by this transaction
// must not impersonate a surviving edge into an absent provider.
// Existing expression/ownership observations also fence retained-message
// admission before removals are overlaid; only new type edges require absence.
func catalogDependencyPreflight(current, candidate *catalog.State, observations []session.DependencyObservation, originals map[ids.ObjectAddress]catalog.Descriptor) error {
	for _, observation := range observations {
		if current == observation.Base {
			continue
		}
		for address, descriptor := range current.Descriptors() {
			if descriptor == observation.Base.Get(address) {
				continue
			}
			owner := candidate.Get(address)
			for _, target := range observation.Targets {
				switch owner := owner.(type) {
				case *catalog.Type:
					if candidate.Get(target) != nil {
						continue
					}
					for _, check := range owner.DomainChecks {
						for _, dependency := range check.Dependencies {
							if dependency.Ref.Address == target {
								return catalog.Conflict{}
							}
						}
					}
					if owner.SQLType.IsDomain() && typedef.UsageDependsOn(owner.SQLType.DomainBase(), target) ||
						owner.SQLType.IsArray() && target == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(owner.SQLType.Element().ID())}) {
						return catalog.Conflict{}
					}
				case *catalog.ForeignObject:
					for _, dependency := range pgcatalog.ForeignDependencies(owner) {
						if dependency != (ids.ObjectAddress{}) && dependency == target {
							return catalog.Conflict{}
						}
					}
				case *catalog.Relation:
					if viewDepends(owner, target) || materializedExtensionDepends(owner, target) || checkDepends(owner, target) || defaultDepends(owner, target) || generationDepends(owner, target) || ExpressionDepends(owner, target) {
						return catalog.Conflict{}
					}
					if candidate.Get(target) == nil {
						for _, column := range owner.Columns {
							if typedef.UsageDependsOn(column.Usage, target) {
								return catalog.Conflict{}
							}
						}
					}
					if owner.Sequence != nil && owner.Sequence.Owned == target {
						// Identity TYPE refreshes this exact internal link under
						// its SRX grant; preserve the existing original fence.
						if !owner.Sequence.Identity || originals[address] != descriptor {
							return catalog.Conflict{}
						}
					}
					if owner.Foreign != nil && owner.Foreign.Server == target {
						return catalog.Conflict{}
					}
				}
			}
		}
	}
	return nil
}
