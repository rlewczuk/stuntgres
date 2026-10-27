package core

import (
	"context"
	"fmt"
	"maps"
	"slices"

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
func (e *Engine) PrivateCatalogLocked(c *session.Journal) catalog.Read {
	return c.PrivateRead(&e.Catalog.Owner, e.Catalog.State)
}
func (e *Engine) CatalogChangeLocked(c *session.Journal, a ids.ObjectAddress, d catalog.Descriptor) {
	c.RecordDescriptor(e.Catalog.State, a, d)
}
func (e *Engine) SequenceGeneratorLocked(s *StatementScope, a ids.ObjectAddress) *storage.SequenceGenerator {
	if s.Catalog != nil {
		if descriptor, exists := s.Catalog.Descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := s.Catalog.Generators[a]; g != nil {
			return g
		}
	}
	if c := s.PendingCatalog(); c != nil {
		if descriptor, exists := c.Descriptors[a]; exists && descriptor == nil {
			return nil
		}
		if g := c.Generators[a]; g != nil {
			return g
		}
	}
	return e.Storage.Sequences[a]
}
func (e *Engine) preparePrivateCatalog(ctx context.Context, c *session.Journal, epoch ids.CatalogEpoch) (*catalog.State, error) {
	e.Mu.Lock()
	// Bind the overlay only to its owning incarnation. Replacement types must
	// never validate old descriptors before the publication epoch fence.
	if e.Catalog.State.Epoch() != epoch {
		e.Mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	view := e.PrivateCatalogLocked(c)
	if err := catalogDependencyPreflight(e.Catalog.State, view.State(), c.Dependencies, c.Originals); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	e.Mu.Unlock()
	descriptors := maps.All(c.Descriptors)
	if c.DomainChanged {
		descriptors = view.Descriptors()
	}
	for a, d := range descriptors {
		if d == nil {
			continue
		}
		p := e.Catalog.Provider(a.ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class")
		}
		if err := catalog.Validate(p, view, d); err != nil {
			return nil, err
		}
	}
	return view.State(), ctx.Err()
}
func (e *Engine) CatalogPreflightLocked(c *session.Journal) error {
	if c.Roles != nil && c.RolesBase != nil && c.Roles.SameAuthority(c.RolesBase) {
		if _, err := e.journalRolePublicationLocked(c, false); err != nil {
			return err
		}
	}
	if err := e.catalogFencesCurrentLocked(c, fenceCatalogRoleRead); err != nil {
		return err
	}
	roles := e.RoleStore.Published
	if c.Roles != nil {
		if c.Roles.SameAuthority(c.RolesBase) {
			// A completed ACL-only journal contributes no actual role delta.
			// Validate its catalog/default principal refs in current authority,
			// not in the completed statement's stale private snapshot.
			roles = e.RoleStore.Published
		} else if c.RolesBase != e.RoleStore.Published {
			var err error
			roles, err = e.mergedJournalRolesLocked(context.Background(), c)
			if err != nil {
				return err
			}
		} else {
			roles = c.Roles
		}
	}
	if !catalog.DefaultRecordsUnchanged(e.Catalog.State, c.DefaultOriginals) || !catalog.DefaultRecordsUnchanged(e.Catalog.State, c.DefaultReads) {
		return catalog.Conflict{}
	}
	view := e.PrivateCatalogLocked(c)
	read := e.RoleStore.Read(&e.Catalog.Owner, view, roles)
	if err := catalog.ValidateDefaultACLs(read, view); err != nil {
		return err
	}
	if err := e.catalogReadPreflightLocked(c); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, FenceExtensionAuthority); err != nil {
		return err
	}
	if err := catalogDependencyPreflight(e.Catalog.State, view.State(), c.Dependencies, c.Originals); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceAlterTypeUse); err != nil {
		return err
	}
	if err := e.catalogFencesCurrentLocked(c, fenceTruncateGraph); err != nil {
		return err
	}
	for address, change := range c.Statistics {
		original, _ := e.Catalog.State.Relation(address)
		if original != change.OriginalRelation {
			return catalog.Conflict{}
		}
		if e.Storage.Relations[address] != change.Original {
			return catalog.Conflict{}
		}
		if relation, exists := e.Catalog.State.Relation(address); exists && c.Descriptors[address] == nil {
			for key, fact := range change.Columns {
				slot := relation.IndexForAttribute(key.Attribute)
				if slot < 0 || fact != nil && !catalog.SamePartitionColumnUsage(relation.Columns[slot].Usage, fact.Usage) {
					return catalog.Conflict{}
				}
			}
		}
	}
	for a, shape := range c.Storage {
		store := e.Storage.Relations[a]
		if store != shape.Original || store != nil && (store.Generation != shape.Generation || len(store.Versions) != len(shape.Root) || store.Ends != shape.Ends || !storage.SameVersionRoot(store.Versions, shape.Root)) {
			return catalog.Conflict{}
		}
	}
	if err := e.catalogFencesCurrentLocked(c, fenceMaterializedSource); err != nil {
		return err
	}
	for a, original := range c.Originals {
		if e.Catalog.State.Get(a) != original {
			return catalog.Conflict{}
		}
	}
	for k, a := range c.Names {
		if e.Catalog.State.NameAddress(k) != a {
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
				if err := visitDefinitionProviders(catalog.NewRead(nil, candidate), owner, func(a ids.ObjectAddress) error {
					if a == target {
						return catalog.Conflict{}
					}
					return nil
				}); err != nil {
					return err
				}
				switch owner := owner.(type) {
				case *catalog.EventTrigger:
					if candidate.Get(target) == nil && owner.Function.Address == target {
						return catalog.Conflict{}
					}
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
				case *catalog.Statistics:
					missing := false
					owner.VisitDependencies(func(dependency ids.ObjectAddress, _ byte) {
						if dependency == target || target.SubID == 0 && dependency.ClassID == target.ClassID && dependency.ObjectID == target.ObjectID {
							missing = candidate.Get(target) == nil
						}
					})
					if missing {
						return catalog.Conflict{}
					}
				case *catalog.Conversion:
					if candidate.Get(target) == nil && (owner.Handler == target || owner.Namespace() == target) {
						return catalog.Conflict{}
					}
				case *catalog.TSConfiguration:
					if candidate.Get(target) == nil && slices.Contains(owner.Dependencies(), target) {
						return catalog.Conflict{}
					}
				case *catalog.TSDictionary:
					if candidate.Get(target) == nil && (owner.Template == target || owner.Namespace() == target) {
						return catalog.Conflict{}
					}
				case *catalog.TSTemplate:
					if candidate.Get(target) == nil && (owner.Init == target || owner.Lexize == target || owner.Namespace() == target) {
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
