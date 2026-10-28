package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Preserve session-owned temporary namespaces and generations during persistent
// replacement. Imported identity collisions are remapped, never temp identities.
func (e *Engine) preserveTemporaryLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, restoreOp *session.Operation, indexes *index.Snapshot, principal catalog.ProcedureRemapper, oids *catalog.OIDReservation) error {
	e.Catalog.RetainDatabasesLocked(next)
	if err := e.remintSubscriptionsLocked(next, oids); err != nil {
		return err
	}
	e.Catalog.RetainOtherSubscriptionsLocked(next)
	current := e.Catalog.Current()
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return err
	}
	if err := control.CheckTemporaryRuleSourcesLocked(current); err != nil {
		return err
	}
	if err := fdw.RebindReplacementProviders(current, e.Foreign, next); err != nil {
		return err
	}
	temporary, err := control.TemporarySet(current, next)
	if err != nil {
		return err
	}
	// Restore the exact live builtin codecs in this private candidate before
	// checking the full TEMP policy runtime manifest. No allocator/publication
	// work occurs before dependency compatibility is established.
	typesChanged := control.RetainTemporaryBuiltinTypes(current, next, temporary)
	if err := control.CheckTemporaryDependencies(current, next, temporary); err != nil {
		return err
	}
	if err := control.RetainTemporaryViewProvidersLocked(current, next, temporary); err != nil {
		return err
	}
	var constraints []catalog.AliasOrderConstraint
	if len(temporary) != 0 {
		constraints, err = stored.RangeIdentityConstraints(ctx, next, relations)
		if err != nil {
			return err
		}
	}
	mapping, err := control.TemporaryCollisionMapping(ctx, next, temporary, constraints, func(floor ids.OID) (ids.OID, error) { return e.Catalog.AllocateOIDFromLocked(&oids.Next, floor) })
	if err != nil {
		return err
	}
	if err := typedef.ImportScalarTypesLocked(next, mapping, e.Catalog.AllocateEnumMemberLocked); err != nil {
		return err
	}
	defer func() { next.SetScalarImports(nil) }()
	domainsChanged, err := typedef.ReownDomainTypes(next, mapping, true, routine.RangeTypeRebinder(next, mapping))
	if err != nil {
		return err
	}
	typedef.RefreshScalarImportDestinations(next)
	indexes.CaptureMapping(mapping, next.ScalarImports())
	if typesChanged || domainsChanged || len(mapping) != 0 || len(next.ScalarImports()) != 0 || next.ACLItemImport() != nil {
		if err := stored.ReownScalarTypes(next, relations, mapping); err != nil {
			return err
		}
	}
	next.SetEpoch(e.Catalog.State.Epoch() + 1)
	candidate := e.Catalog.Read(next)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}
		if target, ok := mapping[address]; ok {
			address = target
		}
		current, _ := candidate.LookupType(sqltype.ID(address.ObjectID))
		return current
	}
	routineRemap := stored.NewRoutineRemap(candidate, mapping, typ, principal, false)
	if err := control.RemapReplacementIdentities(current, next, temporary, mapping, relations, sequences, routineRemap.Procedure); err != nil {
		return err
	}
	if err := stored.ReownInstalledDomainChecks(candidate, next, mapping, false, routineRemap); err != nil {
		return err
	}
	if err := routine.RebindSQLDefinitions(next, routineRemap); err != nil {
		return err
	}
	if err := stored.ReownExpressions(candidate, next, mapping, false, routineRemap); err != nil {
		return err
	}
	if err := index.RebindImportedExclusions(next); err != nil {
		return err
	}
	installedStage, err := e.repairRemappedGenerationsLocked(ctx, next, relations, sequences, mapping, restoreOp, indexes)
	if err != nil {
		return err
	}
	operators := catalog.OperatorIndexEdit{Index: next.OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
	for a, d := range temporary {
		operators.Replace(next.Get(a), d)
		procedures.Replace(next.Get(a), d)
		next.SetDescriptor(a, d)
		next.BindName(catalog.DescriptorNameKey(d), a)
		// Repair may have published these candidate containers into its engine.
		// The target mutex is already held. A distinct stage is isolated and
		// has finished provider evaluation; no callback runs under both locks.
		if installedStage != nil && installedStage != e {
			installedStage.Mu.Lock()
		}
		if rows := e.Storage.Relations[a]; rows != nil {
			if installedStage == nil {
				relations[a] = rows
			} else {
				installedStage.Storage.InstallRelationLocked(a, rows)
			}
		}
		if g := e.Storage.Sequences[a]; g != nil {
			if installedStage == nil {
				sequences[a] = g
			} else {
				installedStage.Storage.InstallSequenceLocked(a, g)
			}
		}
		if installedStage != nil && installedStage != e {
			installedStage.Mu.Unlock()
		}
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	// These consumers originate in the captured destination, not the import.
	// Verify their principal provenance without applying source-name mapping.
	targetProcedure := e.retainedRoutineProcedureRemapper(current)
	for address, descriptor := range temporary {
		if statistics, ok := descriptor.(*catalog.Statistics); ok {
			// Genuine destination TEMP recipes keep object addresses, but every
			// expression/input/provider reference must join the new epoch too.
			relation := statistics.Relation.Address
			if statistics.Relation.Domain() != current.Domain() || statistics.Relation.Epoch != current.Epoch() ||
				temporary[relation] == nil && next.Get(relation) != current.Get(relation) {
				return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary statistics dependencies is unsupported")
			}
			view := e.Catalog.Read(next)
			remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = next.Epoch(); return ref }
			typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
			collation := stored.NewRoutineRemap(view, nil, typ, targetProcedure, false).Collation
			replacement, err := stored.RemapStatisticsExpressions(view, statistics, remap, typ, targetProcedure, collation)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, replacement)
			if err := (catalog.StatisticsProvider{}).Validate(view, replacement); err != nil {
				return err
			}
			continue
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.View == nil && len(relation.Security.Policies) == 0 && len(stored.RulesOf(relation)) == 0 && !relation.HasTriggers() {
			continue
		}
		view := e.Catalog.Read(next)
		remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = next.Epoch(); return ref }
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		// Replacement preflight certified these exact TEMP providers. They
		// enter the candidate after imported expressions have been remapped,
		// so advance their frozen definition refs without applying import IDs.
		if len(relation.Security.Policies) != 0 {
			replacement, err := stored.RemapPolicies(view, relation, nil, remap, typ, nil, targetProcedure)
			if err != nil {
				return err
			}
			relation = replacement
		}
		if len(stored.RulesOf(relation)) != 0 {
			replacement, err := stored.RemapRules(view, relation, nil, remap, typ, nil, targetProcedure)
			if err != nil {
				return err
			}
			relation = replacement
		}
		if relation.View != nil {
			original := stored.ViewOf(relation)
			retained := true
			for ref, captured := range original.References() {
				if procedure, ok := captured.(*catalog.Procedure); ok {
					if _, err := targetProcedure(procedure); err != nil {
						return err
					}
				}
				retained = retained && stored.ViewReferenceValid(view, relation, ref)
			}
			// Preserve the exact contextual TEMP lifetime when its original
			// references already bridge this candidate. Only a real lifetime
			// conversion requires a new presentation/consumer descriptor.
			if !retained {
				definition, err := stored.RemapView(view, original, nil, remap, typ, nil, targetProcedure)
				if err != nil {
					return err
				}
				copy := *relation
				copy.View = stored.ViewPayload(definition)
				relation = &copy
			}
		}
		if relation.HasTriggers() {
			replacement, err := stored.RemapTriggers(view, relation, nil, remap, typ, nil, targetProcedure)
			if err != nil {
				return err
			}
			relation = replacement
		}
		next.SetDescriptor(address, relation)
	}
	previous := make(map[ids.ObjectAddress]catalog.Descriptor, next.DescriptorCount())
	for address, descriptor := range next.Descriptors() {
		previous[address] = descriptor
	}
	if err := control.RemapViewRuleOIDs(e.Catalog.Read(next), func() (ids.OID, error) { return e.Catalog.AllocateOIDFromLocked(&oids.Next, 0) }); err != nil {
		return err
	}
	if err := table.RemapInheritanceConstraintOrigins(next, previous); err != nil {
		return err
	}
	if err := table.RemapRetiredConstraints(e.Catalog.Domain, next, previous); err != nil {
		return err
	}
	if err := table.RemapPhysicalNotNull(e.Catalog.Read(next), previous); err != nil {
		return err
	}
	for _, descriptor := range next.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Security.Policies) != 0 {
			if err := view.ValidatePolicies(e.Catalog.Read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if err := typedef.ValidateTypedAttachment(e.Catalog.Read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Inheritance != nil {
			if err := table.ValidateInheritance(e.Catalog.Read(next), relation); err != nil {
				return err
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.View != nil {
			if err := view.ValidateViewDefinition(e.Catalog.Read(next), relation); err != nil {
				return err
			}
		}
	}
	return nil
}

// Called under Engine.mu at preallocation and final replacement admission.
// Retained TEMP relations cannot substitute persistent inheritance or OF
// sources: control checks the inheritance parents, then the typed check runs.
func (e *Engine) checkTemporaryRelationSourcesReplacementLocked() error {
	if err := control.CheckTemporaryRelationSourcesLocked(e.Catalog.State); err != nil {
		return err
	}
	return typedef.CheckTemporaryTypedReplacementLocked(e.Catalog.State)
}
