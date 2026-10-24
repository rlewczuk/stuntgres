package engine

import (
	"context"
	"fmt"
	"maps"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
)

// Caller holds Engine.mu and the session's statement admission.
func (e *Engine) privateCatalogLocked(c *session.Journal) catalog.Read {
	return c.PrivateRead(&e.catalog.Owner, e.catalog.State)
}
func (e *Engine) catalogChangeLocked(c *session.Journal, a ids.ObjectAddress, d catalog.Descriptor) {
	c.RecordDescriptor(e.catalog.State, a, d)
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
	if e.catalog.State.Epoch() != epoch {
		e.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	if err := catalogDependencyPreflight(e.catalog.State, view.State(), c.Dependencies, c.Originals); err != nil {
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
	if c.Roles != nil && c.RolesBase != nil && c.Roles.SameAuthority(c.RolesBase) {
		if _, err := e.journalRolePublicationLocked(c, false); err != nil {
			return err
		}
	}
	if err := e.catalogFencesCurrentLocked(c, fenceCatalogRoleRead); err != nil {
		return err
	}
	roles := e.roles.published
	if c.Roles != nil {
		if c.Roles.SameAuthority(c.RolesBase) {
			// A completed ACL-only journal contributes no actual role delta.
			// Validate its catalog/default principal refs in current authority,
			// not in the completed statement's stale private snapshot.
			roles = e.roles.published
		} else if c.RolesBase != e.roles.published {
			var err error
			roles, err = e.mergedJournalRolesLocked(context.Background(), c)
			if err != nil {
				return err
			}
		} else {
			roles = c.Roles
		}
	}
	if !catalog.DefaultRecordsUnchanged(e.catalog.State, c.DefaultOriginals) || !catalog.DefaultRecordsUnchanged(e.catalog.State, c.DefaultReads) {
		return catalog.Conflict{}
	}
	view := e.privateCatalogLocked(c)
	read := e.roles.read(&e.catalog.Owner, view, roles)
	if err := catalog.ValidateDefaultACLs(read, view); err != nil {
		return err
	}
	if err := e.catalogReadPreflightLocked(c); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceExtensionAuthority); err != nil {
		return err
	}
	if err := catalogDependencyPreflight(e.catalog.State, view.State(), c.Dependencies, c.Originals); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceTruncateGraph); err != nil {
		return err
	}
	for a, shape := range c.Storage {
		store := e.storage.Relations[a]
		if store != shape.Original || store != nil && (store.Generation != shape.Generation || len(store.Versions) != len(shape.Root) || store.Ends != shape.Ends || !storage.SameVersionRoot(store.Versions, shape.Root)) {
			return catalog.Conflict{}
		}
	}
	if err := e.catalogFencesCurrentLocked(c, fenceMaterializedSource); err != nil {
		return err
	}
	for a, original := range c.Originals {
		if e.catalog.State.Get(a) != original {
			return catalog.Conflict{}
		}
	}
	for k, a := range c.Names {
		if e.catalog.State.NameAddress(k) != a {
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
// admission before removals are overlaid; new type/definition edges require absence.
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
				case *catalog.Procedure:
					if (target.ClassID != ids.TypeClassID || candidate.Get(target) == nil) && catalog.ProcedureDefinitionDependsOn(owner, target) {
						return catalog.Conflict{}
					}
				case *catalog.Operator:
					if (target.ClassID != ids.TypeClassID || candidate.Get(target) == nil) && routine.OperatorDependsOn(catalog.NewRead(nil, current), owner, target) {
						return catalog.Conflict{}
					}
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
				case *catalog.Cast:
					if candidate.Get(target) == nil {
						for _, dependency := range catalog.CastDependencies(owner) {
							if dependency == target {
								return catalog.Conflict{}
							}
						}
					}
				case *catalog.Conversion:
					if candidate.Get(target) == nil && (owner.Handler == target || owner.Namespace() == target) {
						return catalog.Conflict{}
					}
				case *catalog.Collation:
					if candidate.Get(target) == nil && owner.Namespace() == target {
						return catalog.Conflict{}
					}
				case *catalog.ForeignObject:
					for _, dependency := range pgcatalog.ForeignDependencies(owner) {
						if dependency != (ids.ObjectAddress{}) && dependency == target {
							return catalog.Conflict{}
						}
					}
				case *catalog.Relation:
					if view.ViewDepends(owner, target) || view.MaterializedExtensionDepends(owner, target) || table.CheckDepends(owner, target) || table.DefaultDepends(owner, target) || table.GenerationDepends(owner, target) || index.ExpressionDepends(owner, target) {
						return catalog.Conflict{}
					}
					for _, rule := range stored.RulesOf(owner) {
						if view.RuleDepends(catalog.NewRead(nil, candidate), rule, target) {
							return catalog.Conflict{}
						}
					}
					if err := owner.VisitTriggers(func(trigger catalog.TriggerFact) error {
						for _, address := range trigger.AutoExtensions {
							if address == target {
								return catalog.Conflict{}
							}
						}
						return nil
					}); err != nil {
						return err
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
