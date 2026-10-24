package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/routine"
)

// remapPreparedCatalog consumes an unpublished private candidate under the target
// install lock against captured authorities. No callbacks, source lock or external
// I/O run here. ReplaceCatalog already projected its immutable source into private
// maps, so principal remapping does not need another catalog clone.
func (e *Engine) remapPreparedCatalog(state *catalog.State, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalog.State, catalog.ProcedureRemapper, error) {
	e.retainDatabaseLocked(state)
	// Resolve configured destination capabilities before the principal graph
	// memoizes private declarations. A later provider COW copy would otherwise
	// look like a foreign-source declaration to that same strict source policy.
	var foreignOriginals []*catalog.Procedure
	for _, descriptor := range state.Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Foreign != nil {
			foreignOriginals = append(foreignOriginals, procedure)
		}
	}
	if err := fdw.RebindReplacementProviders(e.catalog.read(e.catalog.state), e.foreign, state); err != nil {
		return nil, nil, err
	}
	var foreignTokens map[*catalog.ForeignProcedure]*catalog.ForeignProcedure
	if len(foreignOriginals) != 0 {
		foreignTokens = make(map[*catalog.ForeignProcedure]*catalog.ForeignProcedure, len(foreignOriginals))
		for _, original := range foreignOriginals {
			destination := state.MustProcedure(original.Address()).Foreign
			foreignTokens[original.Foreign] = destination
			foreignTokens[destination] = destination
		}
	}
	policy := e.replacementPrincipalRemap(source, targetSnapshot, expected)
	var graph catalog.ProcedureRemapper
	policy.Procedure = func(original *catalog.Procedure) (*catalog.Procedure, error) {
		mapped, err := graph(original)
		if err != nil || mapped == nil || original.Foreign == nil {
			return mapped, err
		}
		destination := foreignTokens[original.Foreign]
		if destination == nil {
			return nil, fmt.Errorf("stuntgres: replacement foreign provider is not installed in destination")
		}
		// graph owns this private clone. Historical carriers retain their own
		// declaration/revision and acquire only the checked destination token;
		// neither their original callback nor a current descriptor is borrowed.
		mapped.Foreign = destination
		return mapped, nil
	}
	graph = catalog.NewProcedureRemapper(policy)
	if err := catalog.RemapPrincipals(state, policy); err != nil {
		return nil, nil, err
	}
	if err := catalog.ValidateDefaultACLs(e.roles.read(e.catalog.owner(), e.catalog.read(state), e.roles.published), e.catalog.read(state)); err != nil {
		return nil, nil, err
	}
	return state, policy.Procedure, nil
}

func (e *Engine) replacementPrincipalRemap(source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) catalog.PrincipalRemap {
	remap := e.replacementPrincipalMapper(source, targetSnapshot, expected)
	seedOwner := func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error) {
		if !seeded || ref.ID() != authorization.BootstrapPrincipalID {
			return remap(ref)
		}
		if _, err := source.Descriptor(ref); err != nil {
			return authorization.PrincipalRef{}, err
		}
		target := e.roles.domain.Principal(authorization.BootstrapPrincipalID)
		if _, err := targetSnapshot.Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		if _, err := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published).Descriptor(target); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return target, nil
	}
	return catalog.PrincipalRemap{Principal: remap, SeededOwner: seedOwner, OperatorSeeded: routine.OperatorSeeded}
}

// Both authorization ACLs and typed ACL data use the same captured name mapping
// and target-incarnation fences. Each replacement owns its private memoization.
func (e *Engine) replacementPrincipalMapper(source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) func(authorization.PrincipalRef) (authorization.PrincipalRef, error) {
	mapping := make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	return func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if target, ok := mapping[ref]; ok {
			return target, nil
		}
		d, err := source.Descriptor(ref)
		if err != nil {
			return authorization.PrincipalRef{}, err
		}
		target, ok := targetSnapshot.Lookup(d.Name)
		if !ok {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q does not exist", d.Name)
		}
		if _, err := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published).Descriptor(target.Ref); err != nil {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		if expected != nil && expected[ref] != target.Ref {
			return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: replacement principal %q changed", d.Name)
		}
		mapping[ref] = target.Ref
		return target.Ref, nil
	}
}

func (e *Engine) restoreStage() *Engine {
	// Capture before reading: dropping and recreating an owner while a reader
	// is blocked cannot silently adopt the replacement role with the same name.
	e.mu.Lock()
	sourceRoles, nextPrincipal := e.roles.published, e.roles.nextPrincipal
	database := e.catalog.state.MustDatabase(e.catalog.DatabaseAddress())
	e.mu.Unlock()
	stage := NewWithSettings(nil, e.databaseName, false)
	if err := stage.InstallForeignDefinitions(e.foreign.Definitions()); err != nil {
		panic(err)
	}
	stage.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef, sourceRoles.Root.Read(authorization.PrincipalRef{}).RoleCount())
	roles := sourceRoles.Root.Reprovenance(&stage.roles.domain, stage.importTargets)
	retainedDatabase := *database
	// The private staging engine has its own principal authority, but retains
	// the selected database address rather than silently reverting to default.
	initial := stage.catalog.state.Get(stage.catalog.DatabaseAddress())
	stage.catalog.state.UnbindName(catalog.DescriptorNameKey(initial))
	stage.catalog.state.RemoveDescriptor(initial.Address())
	stage.catalog.Database = database.Address()
	retainedDatabase.Owner = stage.roles.domain.Principal(database.Owner.ID())
	retainedDatabase.ACL.Entries = append([]authorization.ACLEntry(nil), database.ACL.Entries...)
	for i := range retainedDatabase.ACL.Entries {
		entry := &retainedDatabase.ACL.Entries[i]
		entry.Grantor = stage.roles.domain.Principal(entry.Grantor.ID())
		if !entry.Grantee.Public {
			entry.Grantee.Role = stage.roles.domain.Principal(entry.Grantee.Role.ID())
		}
	}
	stage.catalog.state.SetDescriptor(database.Address(), &retainedDatabase)
	stage.catalog.state.BindName(catalog.DescriptorNameKey(&retainedDatabase), database.Address())
	stage.roles.published = &access.RoleRecord{Root: roles, Revision: sourceRoles.Revision}
	stage.roles.nextPrincipal = nextPrincipal
	return stage
}

// retainedRoutineProcedureRemapper is only for destination TEMP consumers
// selected by replacement preflight, never imported-source containers.
func (e *Engine) retainedRoutineProcedureRemapper(current catalog.Read) catalog.ProcedureRemapper {
	roles := e.roles.read(e.catalog.owner(), current, e.roles.published)
	identity := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if err := roles.ValidatePrincipal(ref); err != nil {
			return authorization.PrincipalRef{}, err
		}
		return ref, nil
	}
	graph := catalog.NewProcedureRemapper(catalog.PrincipalRemap{
		Principal:   identity,
		SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) { return identity(ref) },
	})
	return func(original *catalog.Procedure) (*catalog.Procedure, error) {
		if original == nil {
			return nil, nil
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(current, original); err != nil {
			return nil, err
		}
		return graph(original)
	}
}
