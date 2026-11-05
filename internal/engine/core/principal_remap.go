package core

import (
	"fmt"

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
func (e *Engine) remapPreparedCatalog(sourceCatalog catalog.Read, source, targetSnapshot authorization.Read, expected map[authorization.PrincipalRef]authorization.PrincipalRef) (*catalog.State, catalog.ProcedureRemapper, error) {
	state := sourceCatalog.State()
	e.Catalog.RetainDatabasesLocked(state)
	// Foreign storage is reminted into the destination ownership universe.
	// Preserve object OIDs, not a source heap/index incarnation locator.
	for address, relation := range state.Relations() {
		if relation.StorageLocator != 0 {
			next := *relation
			next.StorageLocator = 0
			state.SetDescriptor(address, &next)
		}
	}
	tablespaces, err := takeReplacementTablespaces(state)
	if err != nil {
		return nil, nil, err
	}
	// Resolve configured destination capabilities before the principal graph
	// memoizes private declarations. A later provider COW copy would otherwise
	// look like a foreign-source declaration to that same strict source policy.
	var foreignOriginals []*catalog.Procedure
	for _, descriptor := range state.Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Foreign != nil {
			foreignOriginals = append(foreignOriginals, procedure)
		}
	}
	if err := fdw.RebindReplacementProviders(sourceCatalog, e.Foreign, state); err != nil {
		return nil, nil, err
	}
	mapHost, err := e.hostReplacementMapper(state)
	if err != nil {
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
	policy := e.RoleStore.PrincipalRemap(&e.Catalog, source, targetSnapshot, expected, routine.OperatorSeeded)
	var graph catalog.ProcedureRemapper
	var sepgsqlTokens map[*catalog.SepgsqlRoutine]*catalog.SepgsqlRoutine
	policy.Procedure = func(original *catalog.Procedure) (*catalog.Procedure, error) {
		mapped, err := graph(original)
		if err != nil || mapped == nil {
			return mapped, err
		}
		if err = mapHost(mapped); err != nil {
			return nil, err
		}
		if original.Sepgsql != nil {
			if e.Sepgsql == nil || original.Sepgsql.Policy().ID() != e.Sepgsql.ID() || original.Sepgsql.Policy().Version() != e.Sepgsql.Version() {
				return nil, fmt.Errorf("stuntgres: incompatible sepgsql simulation registration")
			}
			if sepgsqlTokens == nil {
				sepgsqlTokens = make(map[*catalog.SepgsqlRoutine]*catalog.SepgsqlRoutine)
			}
			token := sepgsqlTokens[original.Sepgsql]
			if token == nil {
				token, err = catalog.NewSepgsqlRoutine(e.Catalog.Read(state), e.Sepgsql, original.Sepgsql.Symbol(), mapped.Address(), original.RoutineDefinition().Formals)
				if err != nil {
					return nil, err
				}
				sepgsqlTokens[original.Sepgsql], sepgsqlTokens[token] = token, token
			}
			mapped.Sepgsql = token
		}
		if original.Foreign == nil {
			return mapped, nil
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
	if err := e.retainTablespaceAuthorityLocked(state, tablespaces); err != nil {
		return nil, nil, err
	}
	if err := catalog.ValidateDefaultACLs(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Read(state)), e.Catalog.Read(state)); err != nil {
		return nil, nil, err
	}
	if err := catalog.ValidateInitialPrivileges(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Read(state)), e.Catalog.Read(state)); err != nil {
		return nil, nil, err
	}
	if err := catalog.ValidateSecurityLabels(e.Catalog.Read(state)); err != nil {
		return nil, nil, err
	}
	return state, policy.Procedure, nil
}

func (e *Engine) RestoreStage() (*Engine, error) {
	// Capture before reading: dropping and recreating an owner while a reader
	// is blocked cannot silently adopt the replacement role with the same name.
	e.Mu.Lock()
	if err := e.CheckDatabaseLiveLocked(); err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	sourceRoles := e.RoleStore.CaptureLocked()
	database := e.Catalog.State.MustDatabase(e.Catalog.DatabaseAddress())
	parameterPredecessor := e.Catalog.State
	originPredecessor := e.originProgressPredecessorLocked()
	tablespaceRepairs := e.captureTablespaceRepairsLocked()
	e.Mu.Unlock()
	stage, err := e.Factory(nil, database.Name(), false)
	if err != nil {
		return nil, err
	}
	stage.WALNames = e.WALNames
	if err := stage.InstallForeignDefinitions(e.Foreign.Definitions()); err != nil {
		return nil, err
	}
	if err := stage.InstallHostDefinitions(e.Catalog.HostRuntimes); err != nil {
		return nil, err
	}
	if err := stage.InstallSepgsqlSimulation(e.Sepgsql, false); err != nil {
		return nil, err
	}
	stage.TextSearchFiles = e.TextSearchFiles
	stage.IdentFile = e.IdentFile
	stage.HBARules = e.HBARules
	stage.ImportTargets = stage.RoleStore.InstallCapture(sourceRoles)
	stage.ImportParameterPredecessor = parameterPredecessor
	stage.importOriginProgressPredecessor = &originPredecessor
	stage.importTablespaceRepairs = tablespaceRepairs
	retainedDatabase := *database
	// The private staging engine has its own principal authority, but retains
	// the selected database address rather than silently reverting to default.
	initial := stage.Catalog.State.Get(stage.Catalog.DatabaseAddress())
	stage.Catalog.State.UnbindName(catalog.DescriptorNameKey(initial))
	stage.Catalog.State.RemoveDescriptor(initial.Address())
	stage.Catalog.Database = database.Address()
	retainedDatabase.Owner = stage.RoleStore.Domain.Principal(database.Owner.ID())
	capturedPrincipal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return stage.RoleStore.Domain.Principal(ref.ID()), nil
	}
	retainedDatabase.ACL, err = catalog.RemapACL(database.ACL, capturedPrincipal)
	if err != nil {
		return nil, err
	}
	stage.Catalog.State.SetDescriptor(database.Address(), &retainedDatabase)
	stage.Catalog.State.BindName(catalog.DescriptorNameKey(&retainedDatabase), database.Address())
	if stage.Sepgsql != nil {
		stage.restoreInitializationOrigin = &restoreInitializationOrigin{engine: stage, domain: stage.Catalog.Domain, epoch: stage.Catalog.State.Epoch(), policy: stage.Sepgsql}
	}
	// The candidate starts from canonical seed names. Dump replay establishes
	// aliases explicitly before its exact canonical OWNER/ACL repair statements.
	// Only captured authority fields, not destination alias snapshots, initialize
	// this private stage.
	for _, repair := range tablespaceRepairs {
		original := repair.Original
		mapped := *stage.Catalog.State.Get(original.Address()).(*catalog.Tablespace)
		mapped.ObjectRevision = original.Revision()
		mapped.Owner = stage.RoleStore.Domain.Principal(original.Owner.ID())
		mapped.ACL, err = catalog.RemapACL(original.ACL, capturedPrincipal)
		if err != nil {
			return nil, err
		}
		stage.Catalog.State.SetDescriptor(mapped.Address(), &mapped)
	}
	stage.Catalog.InstallRevisionFrontierLocked(catalog.RevisionFrontier(stage.Catalog.NextRevision(), stage.Catalog.State.Descriptors()))
	return stage, nil
}

// retainedRoutineProcedureRemapper is only for destination TEMP consumers
// selected by replacement preflight, never imported-source containers.
func (e *Engine) retainedRoutineProcedureRemapper(current catalog.Read) catalog.ProcedureRemapper {
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, current)
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
