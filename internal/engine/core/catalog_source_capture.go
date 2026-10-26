package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Consumes Engine.mu. These cases select capture policy, never producers: all
// eligible OIDs use the same immutable registry and common row wrapper.
func (e *Engine) openCatalogSourceLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation) (*exec.Source, error) {
	oid := source.Address().ObjectID
	// These two sources own multi-stage capture/completion, not row producers.
	if oid == pgcatalog.LocksOID {
		return e.capturePGLocksLocked(scope.Op.Context(), ref, source)
	}
	if oid == 12048 {
		result, err := e.capturePGSequencesLocked(scope, ref, source)
		e.Mu.Unlock()
		return result, err
	}
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		e.Mu.Unlock()
		if source.VirtualView {
			return nil, fmt.Errorf("stuntgres: unknown virtual catalog view")
		}
		return nil, fmt.Errorf("stuntgres: unknown catalog source")
	}
	in := pgcatalog.Input{Context: scope.Op.Context(), Owner: &e.Catalog.Owner, Ref: ref, Source: source}
	runtime := EngineCatalogRuntime{engine: e}
	unlockBefore := false
	var sharedRoles *access.RoleRecord // Set only where in.Roles derives from it alone.
	switch oid {
	case pgcatalog.ProgressVacuumOID, pgcatalog.StatAllTablesOID, pgcatalog.StatSysTablesOID, pgcatalog.StatUserTablesOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		runtime.Auth = auth
	case pgcatalog.SettingsOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		runtime.Auth = auth
		runtime.settings = scope.Op.EffectiveSettings()
		runtime.registry = e.Settings
		unlockBefore = true
	case pgcatalog.CursorsOID:
		runtime.cursors = PayloadOf(scope.Op.Session).Cursors
		unlockBefore = true
	case 12081, 12085:
		in.Catalog = scope.Inquiry.CommentCatalog
		unlockBefore = true
	case 12004, 12005:
		runtime.input = scope.Input
		unlockBefore = true
	case 12014, 12007, 12008, pgcatalog.ShadowOID:
		in.Roles = scope.Inquiry.commentRoles
		unlockBefore = true
	case pgcatalog.TablePrivilegesOID, pgcatalog.DataTypePrivilegesOID, pgcatalog.InfoUserMappingsOID, pgcatalog.InfoUserMappingOptionsOID, pgcatalog.KeyColumnUsageOID, pgcatalog.InfoRoutineSequenceUsageOID, pgcatalog.InfoRoutineRoutineUsageOID, pgcatalog.InfoCheckConstraintRoutineUsageOID, pgcatalog.InfoCharacterSetsOID, pgcatalog.InfoCollationsOID, pgcatalog.InfoSchemataOID, pgcatalog.InfoColumnOptionsOID, pgcatalog.ConstraintColumnUsageOID, pgcatalog.InfoEnabledRolesOID, pgcatalog.InfoViewTableUsageOID, pgcatalog.InfoColumnColumnUsageOID, pgcatalog.InfoDomainConstraintsOID, pgcatalog.InfoSequencesOID, pgcatalog.InfoRoleColumnGrantsOID, pgcatalog.InfoColumnsOID, pgcatalog.InfoDomainsOID, pgcatalog.InfoUserDefinedTypesOID, pgcatalog.InfoViewsOID, pgcatalog.InfoTablesOID, pgcatalog.InfoRoleTableGrantsOID, pgcatalog.InfoRoutinesOID, pgcatalog.InfoRoleRoutineGrantsOID, pgcatalog.InfoRoleUdtGrantsOID, pgcatalog.InfoSQLFeaturesOID, pgcatalog.InfoRoleUsageGrantsOID, pgcatalog.InfoSQLSizingOID, pgcatalog.InfoSQLImplementationInfoOID, pgcatalog.InfoSQLPartsOID, pgcatalog.InfoRoutinePrivilegesOID, pgcatalog.InfoUsagePrivilegesOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth, runtime.Database = scope.Inquiry.deparseAuth, scope.Op.Session.DatabaseName
		runtime.temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		if oid == pgcatalog.InfoColumnsOID || oid == pgcatalog.InfoViewsOID {
			runtime.presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
		}
		if oid == pgcatalog.InfoDomainsOID || oid == pgcatalog.InfoRoutinesOID {
			in.TypeNamePath = scope.Inquiry.DeparsePath
		}
	case ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID, ids.ForeignTableClassID, pgcatalog.UserMappingsOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		runtime.Auth = auth
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
	case 2396:
		in.Roles = scope.Inquiry.commentRoles
	case 2224:
		runtime.sequenceBase = e.Catalog.State
		if scope.Owner.SequenceCatalog != nil {
			runtime.sequenceBase = scope.Owner.SequenceCatalog
		}
		runtime.sequenceOwn = scope.PendingCatalog()
	default:
		switch oid {
		case 2609, 3079, ids.ParameterClassID:
			in.Catalog = scope.Inquiry.CommentCatalog
		case 2613:
			// Page reads share the active LO query frontier. A fresh VOLATILE
			// body includes its argument effects; the outer inquiry stays frozen.
			in.Catalog = scope.largeObjectRead
			if in.Catalog.State() == nil {
				in.Catalog = scope.Inquiry.CommentCatalog
			}
		default:
			in.Catalog = e.PrivateCatalogLocked(scope.PendingCatalog())
		}
		if oid == ids.ParameterClassID {
			in.Roles = scope.Inquiry.commentRoles
		} else {
			sharedRoles = scope.Inquiry.deparseAuth.RoleRecord
			in.Roles = e.RoleStore.Read(&e.Catalog.Owner, in.Catalog, sharedRoles)
		}
		if oid == 12002 {
			auth, err := e.AuthorizationLocked(scope.Op)
			if err != nil {
				e.Mu.Unlock()
				return nil, err
			}
			auth.Catalog = in.Catalog
			auth.Recapture(auth.Catalog)
			runtime.Auth, in.Roles = auth, auth.Roles
		}
		if oid == 12006 || oid == 12000 || oid == pgcatalog.RulesOID {
			runtime.presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
			runtime.temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		}
	}
	in.Runtime = runtime
	if unlockBefore {
		e.Mu.Unlock()
		return CaptureCatalogSourceRows(in, produce)
	}
	result, err := e.captureCatalogSourceRowsLocked(in, produce, sharedRoles)
	e.Mu.Unlock()
	return result, err
}

// Engine is the sole owner of exec.Source construction, including error paths.
func CaptureCatalogSourceRows(in pgcatalog.Input, produce pgcatalog.Producer) (*exec.Source, error) {
	rows, err := produce(in)
	if err != nil {
		return nil, err
	}
	// Anonymous nil denotes a physical source requiring a visibility view.
	// A catalog producer's empty result is still an owned anonymous rowset.
	if rows == nil {
		rows = [][]sqltype.Value{}
	}
	return &exec.Source{Ref: in.Ref, Anonymous: rows}, nil
}
