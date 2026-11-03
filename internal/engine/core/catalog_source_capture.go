package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"time"
)

// Scalar getter authority is shared by whole-query preparation and low-level
// capture rechecks. nil means full projection; nonnil empty is presence-only.
func admitBGWriterGetters(auth AuthorizationView, uses []int) error {
	procedures := [...]ids.OID{2772, 2773, 2859, 3075}
	if uses == nil {
		uses = []int{0, 1, 2, 3}
	}
	for _, column := range uses {
		if column < 0 || column >= len(procedures) {
			return fmt.Errorf("stuntgres: invalid bgwriter column demand")
		}
		if err := auth.Check(access.Requirement{Object: auth.Ref(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procedures[column]}), Privileges: authorization.ExecutePrivilege}); err != nil {
			return err
		}
	}
	return nil
}

// Consumes Engine.mu. These cases select capture policy, never producers: all
// eligible OIDs use the same immutable registry and common row wrapper.
func (e *Engine) openCatalogSourceLocked(scope *StatementScope, ref ids.ObjectRef, source *catalog.Relation, keep pgcatalog.Keep, outputUses []int, suppressBGWriterRead bool) (*exec.Source, error) {
	oid := source.Address().ObjectID
	if oid == pgcatalog.StatActivityOID {
		return e.capturePGStatActivityLocked(scope, ref, source, keep)
	}
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
	in := pgcatalog.Input{Context: scope.Op.Context(), Owner: &e.Catalog.Owner, Ref: ref, Source: source, Keep: keep}
	runtime := CatalogRuntimeFields{Engine: e}
	unlockBefore := false
	var sharedRoles *access.RoleRecord // Set only where in.Roles derives from it alone.
	switch oid {
	case pgcatalog.StatBGWriterOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		if err := admitBGWriterGetters(auth, outputUses); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		if (outputUses == nil || len(outputUses) != 0) && !suppressBGWriterRead {
			reader := statsReader(scope.Op.Session, scope.Op.EffectiveSettings())
			var now time.Time
			if reader.NeedsSnapshot() {
				now = time.Now().UTC().Truncate(time.Microsecond)
			}
			in.BGWriter = reader.ReadShared(&e.Statistics, statistics.BGWriter, now)
		}
		e.Mu.Unlock()
		return catalogrt.CaptureRows(in, produce)
	case pgcatalog.StatArchiverOID, pgcatalog.StatSLRUOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		// View/column SELECT was admitted by the common query manifest. Check
		// current backing EXECUTE before even configuring the session Reader.
		procedure := ids.OID(3195)
		if oid == pgcatalog.StatSLRUOID {
			procedure = 2306
		}
		if err := auth.Check(access.Requirement{Object: auth.Ref(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procedure}), Privileges: authorization.ExecutePrivilege}); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		reader := statsReader(scope.Op.Session, scope.Op.EffectiveSettings())
		var now time.Time
		if reader.NeedsSnapshot() {
			now = time.Now().UTC().Truncate(time.Microsecond)
		}
		if oid == pgcatalog.StatSLRUOID {
			in.SLRU = reader.ReadSLRUs(&e.Statistics, now)
		} else {
			in.Archiver = reader.ReadShared(&e.Statistics, statistics.Archiver, now)
		}
		e.Mu.Unlock()
		return catalogrt.CaptureRows(in, produce)
	case pgcatalog.StatSubscriptionOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		// SELECT uses the common column manifest. The backing routine has
		// independent current EXECUTE authority, including held statements.
		if err := auth.Check(access.Requirement{Object: auth.Ref(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6118}), Privileges: authorization.ExecutePrivilege}); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		e.Mu.Unlock()
		// Identity-only inactive rows need no cumulative Reader or provider
		// mutation. The shared provider still owns actual counter lifetime.
		return catalogrt.CaptureRows(in, produce)
	case pgcatalog.StatRecoveryPrefetchOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		// Engine.Mu serializes reset/replacement before the leaf provider lock.
		// Copy the current fact without constructing a cumulative snapshot.
		in.RecoveryPrefetch = e.Statistics.ReadShared(statistics.RecoveryPrefetch)
		e.Mu.Unlock()
		return catalogrt.CaptureRows(in, produce)
	case pgcatalog.ShdependOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		databases := scope.Inquiry.sharedDependencyDatabases
		if databases == nil {
			databases = scope.Inquiry.sharedDependencySingle[:]
		}
		e.Mu.Unlock()
		var err error
		in.SharedDependencies, err = catalogrt.ClassifySharedDependencies(in.Context, in.Catalog, in.Roles, databases)
		if err != nil {
			return nil, err
		}
		return catalogrt.CaptureRows(in, produce)
	case pgcatalog.ClassOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		runtime.Auth = auth
	case pgcatalog.ShmemAllocationsOID, pgcatalog.ShmemAllocationsNUMAOID, pgcatalog.AIOSOID, pgcatalog.BackendMemoryContextsOID, pgcatalog.ConfigOID, pgcatalog.FileSettingsOID, pgcatalog.IdentFileMappingsOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		procedure := ids.OID(5052)
		if oid == pgcatalog.ShmemAllocationsNUMAOID {
			procedure = 4100
		} else if oid == pgcatalog.AIOSOID {
			procedure = 6399
		} else if oid == pgcatalog.BackendMemoryContextsOID {
			procedure = 2282
		} else if oid == pgcatalog.ConfigOID {
			procedure = 3400
		} else if oid == pgcatalog.FileSettingsOID {
			procedure = 3329
		} else if oid == pgcatalog.IdentFileMappingsOID {
			procedure = 6250
		}
		// The common query manifest has already admitted view SELECT. Its
		// backing routine has independent current EXECUTE authority; no
		// provider or simulated row is evaluated before this check.
		if err := auth.Check(access.Requirement{Object: auth.Ref(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procedure}), Privileges: authorization.ExecutePrivilege}); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		if oid == pgcatalog.BackendMemoryContextsOID {
			in.Runtime = newCatalogRuntime(runtime)
			e.Mu.Unlock()
			// Operation validation may take Session.Mu. Admission to the view
			// and backing routine is complete; never nest it under Engine.Mu.
			if err := session.ValidateOperation(DomainOf(e), scope.Op); err != nil {
				return nil, err
			}
			in.BackendMemory, err = captureBackendMemory(in.Context, scope.Op.Session)
			if err != nil {
				return nil, err
			}
			return catalogrt.CaptureRows(in, produce)
		}
		if oid == pgcatalog.IdentFileMappingsOID {
			in.IdentFile = e.IdentFile
			unlockBefore = true
		}
	case pgcatalog.StatReplicationSlotsOID, pgcatalog.PublicationRelationOID, pgcatalog.PublicationTablesOID, pgcatalog.ReplicationOriginOID, pgcatalog.PublicationOID, pgcatalog.PublicationNamespaceOID, pgcatalog.SubscriptionOID, pgcatalog.SubscriptionRelOID, pgcatalog.StatisticExtDataOID, pgcatalog.StatsOID, pgcatalog.ProgressVacuumOID, pgcatalog.ProgressCopyOID, pgcatalog.ProgressCreateIndexOID, pgcatalog.ProgressClusterOID, pgcatalog.StatAllTablesOID, pgcatalog.StatSysTablesOID, pgcatalog.StatUserTablesOID, pgcatalog.StatioAllSequencesOID, pgcatalog.StatioSysSequencesOID, pgcatalog.StatioUserSequencesOID, pgcatalog.StatAllIndexesOID, pgcatalog.StatioAllIndexesOID, pgcatalog.StatioUserIndexesOID, pgcatalog.StatioAllTablesOID, pgcatalog.StatioSysTablesOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		runtime.Auth = auth
		runtime.Settings = scope.Op.EffectiveSettings()
	case pgcatalog.SettingsOID:
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		in.Catalog, in.Roles = auth.Catalog, auth.Roles
		runtime.Auth = auth
		runtime.Settings = scope.Op.EffectiveSettings()
		runtime.Registry = e.Settings
		unlockBefore = true
	case pgcatalog.CursorsOID:
		runtime.Cursors = PayloadOf(scope.Op.Session).Cursors
		unlockBefore = true
	case pgcatalog.PreparedStatementsOID:
		runtime.PreparedStatements = PayloadOf(scope.Op.Session).PreparedSQL
		unlockBefore = true
	case 12081, 12085:
		in.Catalog = scope.Inquiry.CommentCatalog
		unlockBefore = true
	case 12004, 12005:
		runtime.Input = scope.Input
		unlockBefore = true
	case ids.RoleClassID, pgcatalog.AuthMembersOID, 12014, 12007, 12008, pgcatalog.ShadowOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.commentRoles
		in.RoleSettings = catalogRoleSettings{settings: scope.Inquiry.deparseAuth.RoleRecord.Settings}
		unlockBefore = true
	case ids.PolicyClassID, ids.TriggerClassID, pgcatalog.ShseclabelOID:
		// Pair definition and principal roots with the statement's admitted
		// inquiry authority, including transaction-private definitions.
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth = scope.Inquiry.deparseAuth
		unlockBefore = true
	case pgcatalog.PoliciesOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth = scope.Inquiry.deparseAuth
		runtime.Presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
		unlockBefore = true
	case pgcatalog.InfoReferentialConstraintsOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth, runtime.Database = scope.Inquiry.deparseAuth, scope.Op.Session.DatabaseName
		unlockBefore = true
	case pgcatalog.InfoTriggersOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth, runtime.Database = scope.Inquiry.deparseAuth, scope.Op.Session.DatabaseName
		runtime.Temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		runtime.Presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
		unlockBefore = true
	case pgcatalog.InfoTransformsOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth, runtime.Database = scope.Inquiry.deparseAuth, scope.Op.Session.DatabaseName
		runtime.Temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		unlockBefore = true
	case pgcatalog.InfoForeignDataWrapperOptionsOID, pgcatalog.InfoForeignDataWrappersOID, pgcatalog.InfoForeignServerOptionsOID, pgcatalog.InfoForeignServersOID, pgcatalog.InfoForeignTableOptionsOID, pgcatalog.InfoForeignTablesOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		runtime.Auth, runtime.Database = scope.Inquiry.deparseAuth, scope.Op.Session.DatabaseName
		runtime.Temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
	case pgcatalog.InfoColumnUdtUsageOID, pgcatalog.InfoColumnPrivilegesOID, pgcatalog.InfoAttributesOID, pgcatalog.InfoApplicableRolesOID, pgcatalog.InfoElementTypesOID, pgcatalog.InfoUdtPrivilegesOID, pgcatalog.InfoViewColumnUsageOID, pgcatalog.InfoViewRoutineUsageOID,
		pgcatalog.InfoInformationSchemaCatalogNameOID, pgcatalog.TablePrivilegesOID, pgcatalog.DataTypePrivilegesOID, pgcatalog.InfoUserMappingsOID, pgcatalog.InfoUserMappingOptionsOID, pgcatalog.KeyColumnUsageOID, pgcatalog.InfoRoutineSequenceUsageOID, pgcatalog.InfoRoutineRoutineUsageOID, pgcatalog.InfoCheckConstraintRoutineUsageOID, pgcatalog.InfoCheckConstraintsOID, pgcatalog.InfoCharacterSetsOID, pgcatalog.InfoCollationsOID, pgcatalog.InfoSchemataOID, pgcatalog.InfoColumnOptionsOID, pgcatalog.InfoTableConstraintsOID, pgcatalog.ConstraintColumnUsageOID, pgcatalog.ConstraintTableUsageOID, pgcatalog.InfoEnabledRolesOID, pgcatalog.InfoViewTableUsageOID, pgcatalog.InfoColumnColumnUsageOID, pgcatalog.InfoColumnDomainUsageOID, pgcatalog.InfoDomainUdtUsageOID, pgcatalog.InfoDomainConstraintsOID, pgcatalog.InfoSequencesOID, pgcatalog.InfoRoleColumnGrantsOID, pgcatalog.InfoColumnsOID, pgcatalog.InfoDomainsOID, pgcatalog.InfoUserDefinedTypesOID, pgcatalog.InfoViewsOID, pgcatalog.InfoTablesOID, pgcatalog.InfoRoleTableGrantsOID, pgcatalog.InfoRoutinesOID, pgcatalog.InfoParametersOID, pgcatalog.InfoRoleRoutineGrantsOID, pgcatalog.InfoRoleUdtGrantsOID, pgcatalog.InfoSQLFeaturesOID, pgcatalog.InfoRoleUsageGrantsOID, pgcatalog.InfoSQLSizingOID, pgcatalog.InfoSQLImplementationInfoOID, pgcatalog.InfoSQLPartsOID, pgcatalog.InfoRoutinePrivilegesOID, pgcatalog.InfoUsagePrivilegesOID:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.deparseAuth.Roles
		if oid == pgcatalog.InfoColumnUdtUsageOID || oid == pgcatalog.InfoAttributesOID || oid == pgcatalog.InfoElementTypesOID || oid == pgcatalog.InfoInformationSchemaCatalogNameOID || oid == pgcatalog.InfoViewRoutineUsageOID {
			// Relation opening initially resolves the live descriptor. This
			// inquiry's source and column generation belong to its captured root.
			var exists bool
			in.Source, exists = in.Catalog.Relation(in.Ref.Address)
			if !exists {
				e.Mu.Unlock()
				return nil, fmt.Errorf("stuntgres: missing captured %s source", source.Name())
			}
		}
		runtime.Auth = scope.Inquiry.deparseAuth
		if oid == pgcatalog.InfoColumnUdtUsageOID || oid == pgcatalog.InfoAttributesOID || oid == pgcatalog.InfoElementTypesOID || oid == pgcatalog.InfoDomainUdtUsageOID || oid == pgcatalog.InfoInformationSchemaCatalogNameOID || oid == pgcatalog.InfoViewRoutineUsageOID {
			runtime.Database = scope.Inquiry.databaseName
		} else {
			runtime.Database = scope.Op.Session.DatabaseName
		}
		runtime.Temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		if oid == pgcatalog.InfoAttributesOID || oid == pgcatalog.InfoColumnsOID || oid == pgcatalog.InfoViewsOID || oid == pgcatalog.InfoParametersOID || oid == pgcatalog.InfoCheckConstraintsOID {
			runtime.Presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
		}
		if oid == pgcatalog.InfoElementTypesOID || oid == pgcatalog.InfoDomainsOID || oid == pgcatalog.InfoRoutinesOID || oid == pgcatalog.InfoParametersOID {
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
	case 2964:
		in.Catalog, in.Roles = scope.Inquiry.CommentCatalog, scope.Inquiry.commentRoles
		in.RoleSettings = catalogRoleSettings{settings: scope.Inquiry.deparseAuth.RoleRecord.Settings}
		unlockBefore = true
	case 2396:
		in.Roles = scope.Inquiry.commentRoles
	case 2224:
		runtime.SequenceBase = e.Catalog.State
		if scope.Owner.SequenceCatalog != nil {
			runtime.SequenceBase = scope.Owner.SequenceCatalog
		}
		runtime.SequenceOwn = scope.PendingCatalog()
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
			runtime.Presentation = stored.Presentation{Deparse: true, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: scope.Op.NonstandardStrings}
			runtime.Temporary = CatalogSourceTemporaryOwner(scope.Inquiry.deparseAuth)
		}
	}
	in.Runtime = newCatalogRuntime(runtime)
	if unlockBefore {
		e.Mu.Unlock()
		return catalogrt.CaptureRows(in, produce)
	}
	result, err := e.catalogCaptures.CaptureLocked(in, produce, sharedRoles, e.Catalog.State, e.RoleStore.Published)
	e.Mu.Unlock()
	return result, err
}

// captureSelectSourcesLocked is CaptureQuerySourcesLocked for a SELECT whose
// root reads the leaves. While it runs, a pg_settings, pg_attribute, pg_class,
// pg_proc or information_schema routines/parameters leaf filtered by one of its
// key columns builds only the rows the filter can accept.
func (e *Engine) captureSelectSourcesLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf, parameters []sqltype.Value, singleton bool) (*exec.Source, error) {
	previous := scope.catalogKeep
	scope.catalogKeep = &catalogrt.KeepQuery{Root: root, Leaves: leaves, Parameters: parameters}
	defer func() { scope.catalogKeep = previous }()
	return e.CaptureQuerySourcesLocked(scope, leaves, singleton)
}
