package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/routine"
)

// remapPreparedCatalog consumes an unpublished private candidate under the target
// install lock against captured authorities. No callbacks, source lock or external
// I/O run here. ReplaceCatalog already projected its immutable source into private
// maps, so principal remapping does not need another catalog clone.
func (e *Engine) remapPreparedCatalog(state *catalog.State, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalog.State, error) {
	e.retainDatabaseLocked(state)
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
	if err := catalog.RemapPrincipals(state, catalog.PrincipalRemap{Principal: remap, SeededOwner: seedOwner, OperatorSeeded: routine.OperatorSeeded}); err != nil {
		return nil, err
	}
	if err := catalog.ValidateDefaultACLs(e.roles.read(e.catalog.owner(), e.catalog.read(state), e.roles.published), e.catalog.read(state)); err != nil {
		return nil, err
	}
	return state, nil
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
	database := e.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
	e.mu.Unlock()
	stage := NewWithSettings(nil, e.databaseName, false)
	if err := stage.InstallForeignDefinitions(e.foreign.Definitions()); err != nil {
		panic(err)
	}
	stage.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef, sourceRoles.Root.Read(authorization.PrincipalRef{}).RoleCount())
	roles := sourceRoles.Root.Reprovenance(&stage.roles.domain, stage.importTargets)
	retainedDatabase := *database
	retainedDatabase.Owner = stage.roles.domain.Principal(database.Owner.ID())
	retainedDatabase.ACL.Entries = append([]authorization.ACLEntry(nil), database.ACL.Entries...)
	for i := range retainedDatabase.ACL.Entries {
		entry := &retainedDatabase.ACL.Entries[i]
		entry.Grantor = stage.roles.domain.Principal(entry.Grantor.ID())
		if !entry.Grantee.Public {
			entry.Grantee.Role = stage.roles.domain.Principal(entry.Grantee.Role.ID())
		}
	}
	stage.catalog.state.SetDescriptor(catalog.CurrentDatabaseAddress, &retainedDatabase)
	stage.roles.published = &access.RoleRecord{Root: roles, Revision: sourceRoles.Revision}
	stage.roles.nextPrincipal = nextPrincipal
	return stage
}
