package core

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The store is the ddl.Allocator of identity-only callers such as partition
// trigger synchronization. A pointer fits an interface without allocating, so
// those callers add no allocation per publication. Engine.mu is held.
var _ ddl.Allocator = (*catalog.Store)(nil)

type Engine struct {
	Mu            *sync.Mutex
	Locks         *lock.Manager
	Analysis      BindingHost                  // Binding host without an operation; immutable after New.
	notifications session.NotificationRegistry // Engine.mu; committed registrations only.
	// Implicit row work finishes before replacement; explicit work is epoch-fenced.
	ReplacementMu     replacementAdmission
	Catalog           catalog.Store
	DatabaseName      string
	DatabaseNameError error
	Storage           storage.Store
	sessionMu         sync.Mutex
	sessions          map[ids.SessionToken]*session.Session
	Settings          settings.Registry // Immutable, initialized once before publication.
	Authority         AuthorityInputs   // Points at settings and storage; shared by engine-built views.
	Visibility        visibility.Registry
	IndexWaits        index.WaitRegistry
	Exclusions        index.ExclusionRegistry
	prepared          session.PreparedRegistry
	RoleStore         *access.RoleStore
	Cluster           *DatabaseCluster
	ImportTargets     map[authorization.PrincipalRef]authorization.PrincipalRef
	Foreign           fdw.Registry
	DatabaseFrozenXID uint64
	VacuumProgress    map[ids.TxRef]VacuumProgress // Engine.mu; only actual work.
	catalogCaptures   catalogCaptureCache          // Engine.mu; latest shared pg_type/pg_proc rows.
	Statements        Statements                   // Family dispatch; set by the composition, shared by every target.
	Factory           EngineFactory                // Composed constructor; set by the composition, shared by every target.
}

// EngineFactory builds a fresh composed engine, as NewWithSettings does: the
// private template1 seed and the Restore stage.
type EngineFactory func(definitions []settings.Definition, database string, trackCommitTimestamp bool) (*Engine, error)
type Result struct {
	CommandTag string
	Count      int64
	Rows       exec.RowResult
	RowSets    []exec.RowResult
	Created    ids.ObjectRef // Exact ordinary CREATE TABLE outcome; zero for a no-op.
}

// NewEngine seeds one engine's state: the catalog, storage and role stores,
// settings, visibility, locks, the catalog providers (feature-owned ones
// first), the bundled extension registry and the builtin catalog seed. It
// registers no statement dispatch or factory; the composition does.
func NewEngine(providers []catalog.Provider, definitions []settings.Definition, database string, trackCommitTimestamp bool) (*Engine, error) {
	// Builtin descriptors use revision 1; allocations start beyond that frontier.
	cluster := &DatabaseCluster{}
	e := &Engine{Mu: &cluster.Mu, RoleStore: &cluster.Roles, Cluster: cluster, Catalog: catalog.NewStore(catalog.Owner{Providers: map[ids.OID]catalog.Provider{}}, 16384, 2)}
	e.Analysis = BindingHost{Engine: e}
	e.Authority = AuthorityInputs{Domain: e.Catalog.Domain, Settings: &e.Settings, Storage: &e.Storage}
	e.Mu.Lock()
	e.Storage.ReplaceRelationsLocked(map[ids.ObjectAddress]*storage.Relation{})
	e.Mu.Unlock()
	e.Catalog.Principals, e.Catalog.ACLAuthority = &e.RoleStore.Domain, &e.RoleStore.ACLAuthority
	e.Visibility = visibility.NewRegistry(trackCommitTimestamp)
	e.DatabaseFrozenXID = 3
	cluster.Locks = lock.New(e.Catalog.Domain)
	if err := cluster.Locks.SetRoleDomain(&cluster.RoleDomain, 1); err != nil {
		return nil, err
	}
	e.Locks = cluster.Locks
	e.Mu.Lock()
	e.Storage.ReplaceSequencesLocked(make(map[ids.ObjectAddress]*storage.SequenceGenerator))
	e.Mu.Unlock()
	e.RoleStore.Initialize()
	e.Settings = settings.NewRegistry(definitions, e.Visibility.TracksCommitTimestamps())
	for _, p := range providers {
		if err := e.RegisterCatalogProvider(p); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(catalog.NamespaceProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(RelationProvider); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(stored.TypeProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.CollationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.ConversionProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.AccessMethodProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.CastProvider{}); err != nil {
		return nil, err
	}
	for _, class := range []ids.OID{ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID} {
		if err := e.RegisterCatalogProvider(catalog.ForeignCatalogProvider{class}); err != nil {
			return nil, err
		}
	}
	for _, p := range []catalog.Provider{extension.CatalogProvider{ids.ExtensionClassID}, stored.ProcedureProvider{}, extension.CatalogProvider{ids.TSTemplateClassID}, extension.CatalogProvider{ids.TSDictionaryClassID}} {
		if err := e.RegisterCatalogProvider(p); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(catalog.DatabaseProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(routine.OperatorProvider{}); err != nil {
		return nil, err
	}
	for _, class := range []ids.OID{ids.OperatorFamilyClassID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID} {
		if err := e.RegisterCatalogProvider(catalog.OperatorFamilyProvider{Class: class}); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(catalog.OperatorClassProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(role.ParameterProvider{Settings: &e.Settings}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.TablespaceProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.LanguageProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.EventTriggerProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.LargeObjectProvider{}); err != nil {
		return nil, err
	}
	for _, registration := range []struct {
		name           string
		implementation extension.Installer
	}{{"amcheck", extension.StockAmcheck}, {"hstore", extension.StockHstore}, {"isn", extension.StockIsn}, {"citext", extension.StockCitext}, {"cube", extension.StockCube}, {"earthdistance", extension.StockEarthdistance}, {"lo", extension.StockLo}, {"unaccent", extension.StockUnaccent}, {"intarray", extension.StockIntarray}, {"fuzzystrmatch", extension.StockFuzzystrmatch}, {"intagg", extension.StockIntagg}, {"pgrowlocks", extension.StockPgrowlocks}, {"pgcrypto", extension.StockPgcrypto}, {"uuid-ossp", extension.StockUUIDOssp}} {
		if err := e.registerExtension(registration.name, registration.implementation); err != nil {
			return nil, err
		}
	}
	types := sqltype.Builtins()
	// The seed sets thousands of entries, so it is one bulk load.
	e.Catalog.PublishLocked(catalog.NewState(1))
	e.Catalog.State.BeginBulkLoad(builtinCatalogCapacity(len(types)))
	bootstrap := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	principals := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	catalog.InstallBuiltinNamespaces(e.Catalog.State, principals.LookupRef("postgres"), principals.LookupRef("pg_database_owner"))
	catalog.InstallBuiltinTypes(e.Catalog.State, types, bootstrap)
	catalog.InstallBuiltinCollations(e.Catalog.State, bootstrap)
	catalog.InstallBuiltinProcedures(e.Catalog.State, typedef.BuiltinConversionProcedures(bootstrap), routine.BuiltinProcedures(bootstrap), routine.NativeAccessMethodProcedures(bootstrap))
	if err := e.Catalog.InstallBuiltinLanguagesLocked(bootstrap); err != nil {
		return nil, err
	}
	catalog.InstallBuiltinTablespaces(e.Catalog.State, bootstrap)
	catalog.InstallBuiltinConversions(e.Catalog.State, bootstrap)
	catalog.InstallBuiltinOperators(e.Catalog.State, routine.SeededOperatorNames(), routine.BuiltinOperatorDescriptors(bootstrap))
	catalog.InstallBuiltinFamilies(e.Catalog.State, bootstrap)
	catalog.InstallBuiltinClasses(e.Catalog.State, bootstrap)
	e.Catalog.ShareProcedureNamesLocked(builtinSeed)
	e.initializeDatabase(database)
	if err := e.installBuiltinDomainChecks(); err != nil {
		return nil, err
	}
	if err := e.initializeCatalogSources(); err != nil {
		return nil, err
	}
	if err := e.Catalog.ValidateSeed(builtinSeed); err != nil {
		return nil, err
	}
	e.Catalog.FinishSeedLocked(builtinSeed)
	cluster.DefaultTarget, cluster.Targets, cluster.NextOID = e, map[string]*Engine{e.DatabaseName: e}, 16384
	return e, nil
}
func (e *Engine) CatalogRead() catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Catalog.Current()
}

func SQLError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
func (e *Engine) Describe(op *session.Operation, stmt parser.Statement) (info StatementDescription, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return StatementDescription{}, err
	}
	defer func() { err = scope.Finish(err) }()
	return scope.Describe(stmt)
}
func (e *Engine) describeOperation(scope *StatementScope, stmt parser.Statement) (StatementDescription, error) {
	op := scope.Op
	scope.Input.LockedDomainInput = scope
	if err := e.validateOperation(op); err != nil {
		return StatementDescription{}, err
	}
	switch s := stmt.(type) {
	case *parser.Do, *parser.CreateFunction, *parser.CreateLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateCast, *parser.DropCast:
		return StatementDescription{}, nil
	case *parser.ForeignDDL:
		return StatementDescription{}, nil
	case *parser.CreateDatabaseStmt, *parser.CreateTablespace, *parser.Load, *parser.Vacuum:
		return StatementDescription{}, nil
	case *parser.Truncate:
		return StatementDescription{}, nil
	case *parser.RefreshMaterializedView:
		return StatementDescription{}, nil
	case *parser.PrepareSQL, *parser.DeallocateSQL, *parser.Discard:
		return StatementDescription{}, nil
	case *parser.ExecuteSQL:
		if entry := PayloadOf(op.Session).preparedSQL.entries[s.Name.Text]; entry != nil {
			if !scope.RowSecurity && session.PreparableBody(entry.body) {
				// Reuse the named statement's analyzed identities without changing
				// its session definition or executing its argument expressions.
				expected, parameters := scope.Expected, scope.initialParameters
				description := entry.description
				scope.Expected = &description
				scope.initialParameters = description.Parameters.Types
				_, err := scope.Describe(entry.body)
				scope.Expected, scope.initialParameters = expected, parameters
				if err != nil {
					return StatementDescription{}, err
				}
			}
			return StatementDescription{Results: entry.description.Results}, nil
		}
		return StatementDescription{}, nil
	case *parser.FetchCursor:
		if s.Move {
			return StatementDescription{}, nil
		}
		if cursor := PayloadOf(op.Session).Cursors.Entries[s.Name.Text]; cursor != nil {
			return StatementDescription{Results: cursor.schema}, nil
		}
		return StatementDescription{}, nil
	case *parser.CloseCursor:
		return StatementDescription{}, nil
	case *parser.AlterOwner, *parser.ReassignOwned, *parser.DropOwned:
		return StatementDescription{}, nil
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger, *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return StatementDescription{}, nil
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger:
		return StatementDescription{}, nil
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return StatementDescription{}, nil
	case *parser.CreateComposite, *parser.AlterComposite, *parser.AlterType, *parser.DropType, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum, *parser.CreateDomain, *parser.DropDomain, *parser.AlterDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return StatementDescription{}, nil
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return StatementDescription{}, nil
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return StatementDescription{}, nil
	case *parser.CreateExtension, *parser.DropExtension, *parser.AlterExtensionUpdate:
		return StatementDescription{}, nil
	case *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		return StatementDescription{}, nil
	case *parser.AlterTableAllInTablespace, *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableDefault, *parser.AlterTable, *parser.LockTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization, *parser.Set, *parser.Reset:
		return StatementDescription{}, nil
	case *parser.RoleSelection:
		return StatementDescription{}, nil
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return StatementDescription{}, nil
	case *parser.Show:
		e.Mu.Lock()
		defer e.Mu.Unlock()
		readRestricted := false
		if !s.All {
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				return StatementDescription{}, err
			}
			readRestricted, err = auth.canReadRestrictedSettings()
			if err != nil {
				return StatementDescription{}, err
			}
		}
		op.Session.Settings.Mu.Lock()
		defer op.Session.Settings.Mu.Unlock()
		typ, _ := e.Catalog.Current().LookupType(sqltype.Text)
		if s.All {
			return StatementDescription{Results: settingColumns(typ, "name", "setting", "description")}, nil
		}
		d, err := op.Session.Settings.LookupDefinition(e.Settings, s.Name.Text, false)
		if err != nil {
			return StatementDescription{}, err
		}
		if err := session.SettingReadAccess(d, s.Name.Text, readRestricted); err != nil {
			return StatementDescription{}, err
		}
		return StatementDescription{Results: settingColumns(typ, settings.DisplayName(d.Name))}, nil
	case *parser.SettingCall:
		e.Mu.Lock()
		defer e.Mu.Unlock()
		binding, err := e.describeSettingCall(op.Ctx, s, &scope.Input, op)
		typ, _ := e.Catalog.Current().LookupType(sqltype.Text)
		columns := settingColumns(typ, s.Name)
		columns[0].Nullable = true
		return StatementDescription{Parameters: binding.parameters, Results: columns}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		return StatementDescription{}, err
	}
	auth, err := scope.statementAuthorizationLocked(stmt)
	if err != nil {
		return StatementDescription{}, err
	}
	b, err := e.describeScoped(scope, stmt, auth, nil)
	if err != nil {
		return StatementDescription{}, err
	}
	// Literal lifetimes belong to the caller's raw SQL, including its CTEs.
	// Stored rule/view recipes carry their own captures and invalidation events.
	callerSequences, callerRegtypes := b.Prepared.SequenceConstants, b.Prepared.RegtypeConstants
	if b.ViewWrite != nil {
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			return StatementDescription{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, false); err != nil {
		return StatementDescription{}, err
	}
	if err := scope.ValidateQuerySources(b.QuerySources); err != nil {
		return StatementDescription{}, err
	}
	if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, auth, stmt, &b); err != nil {
		return StatementDescription{}, err
	}
	if relation := view.BindingRuleRelation(b); relation != nil && len(stored.RulesOf(relation)) != 0 {
		program := view.RuleProgram{Count: -1, Returning: -1}
		if _, err := (view.RuleRewriter{Owner: &e.Catalog.Owner}).ExpandLocked(auth, stmt, b, nil, &program); err != nil {
			return StatementDescription{}, err
		}
		for i := range program.Commands {
			command := &program.Commands[i]
			if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, command.Auth, command.Syntax, &command.Binding); err != nil {
				return StatementDescription{}, err
			}
		}
		if err := view.CheckRowSecurityRuleProgram(scope.RowSecurity, auth.View, b, program); err != nil {
			return StatementDescription{}, err
		}
		b.Dependencies = append(b.Dependencies, program.Events...)
		for _, command := range program.Commands {
			if command.Binding.Relation != nil {
				b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: command.Binding.Ref, Revision: command.Binding.Relation.Revision()})
			}
			b.Dependencies = append(b.Dependencies, command.Binding.Dependencies...)
		}
	} else if err := view.CheckRowSecurityBinding(scope.RowSecurity, auth.View, b); err != nil {
		return StatementDescription{}, err
	}
	var results []catalog.Column
	if b.Query != nil && !b.CommandQuery {
		results = b.Query.Root.Schema()
	}
	if b.Returning != nil {
		results = b.Returning.Schema
	}
	analysis, err := e.sequenceAnalysisLocked(auth, callerSequences)
	if err != nil {
		return StatementDescription{}, err
	}
	var cell *sequence.Analysis
	if len(analysis.Constants) != 0 {
		cell = &analysis
	}
	regtypes, err := e.regtypeAnalysisLocked(auth, callerRegtypes)
	if err != nil {
		return StatementDescription{}, err
	}
	var prepared *preparedAnalysis
	if session.PreparableBody(stmt) {
		prepared, err = e.preparedAnalysisLocked(scope, auth, b)
		if err != nil {
			return StatementDescription{}, err
		}
	}
	return StatementDescription{Parameters: b.Parameters, Results: results, catalogOwner: auth.Catalog.Owner(), catalogEpoch: auth.Catalog.Epoch(), Temporals: b.Prepared.Temporals, types: b.Prepared.Types, Sequences: cell, regtypes: regtypes, analysis: prepared, planOwner: op.Session.SessionToken, planGeneration: op.Session.DiscardPlansGeneration}, nil
}

// requiredRef is the exact acquired DML/utility target; query source occurrences
// have their own admission manifest. Binding validates namespace authority and
// cannot retarget an acquired identity. CREATE supplies the zero reference.
func Describe(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *StatementDescription, admission *analyze.LockAdmission, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return DescribeWithRule(ctx, host, stmt, auth, requiredRef, input, expected, admission, nil, initial...)
}

func DescribeWithRule(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *StatementDescription, admission *analyze.LockAdmission, rule *analyze.RuleNamespace, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return analyze.Describe(ctx, host, stmt, AuthorityOf(auth), requiredRef, input, expectedOf(expected), admission, rule, false, initial...)
}

func (e *Engine) describeScoped(scope *StatementScope, stmt parser.Statement, auth AuthorizationView, expected *StatementDescription) (analyze.Binding, error) {
	if scope.sqlRoutine.binding != nil {
		return *scope.sqlRoutine.binding, nil
	}
	if scope.sqlRoutine.lexical != nil {
		return analyze.DescribePL(scope.Op.Context(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, &scope.QueryLockAdmission, scope.sqlRoutine.lexical, scope.sqlRoutine.assignment)
	}
	if scope.sqlRoutine.definition != nil {
		return analyze.DescribeRoutine(scope.Op.Context(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, expectedOf(expected), &scope.QueryLockAdmission, scope.sqlRoutine.definition, scope.sqlRoutine.name, scope.sqlRoutine.inputCollation)
	}
	// Dynamic cursor USING is a closed positional frame, not a public
	// parameter-inference request. Static PL queries returned above.
	return analyze.Describe(scope.Op.Context(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, expectedOf(expected), &scope.QueryLockAdmission, nil, scope.sqlRoutine.cursor != nil, scope.initialParameters)
}

// Resolution helpers run under e.mu, using the supplied coherent view.

func (e *Engine) ResolveReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	ref, err := e.ResolveRelationReference(auth, name, false)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42P01" || diagnostic.Code == "42501") && len(name.Parts) > 0 {
		// Analyzed relation names retain their raw identifier occurrence.
		// Utility lookup owns its separate, intentionally unpositioned record.
		if position := name.Parts[0].Pos.Character; position > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(position)
			err = &copy
		}
	}
	return ref, err
}

// Utility lookup checks a qualified namespace before the relation and reports
// namespace denial without the position attached by DML analysis.
func (e *Engine) ResolveRelationReference(auth AuthorizationView, name parser.ObjectName, utility bool) (ids.ObjectRef, error) {
	if err := e.Catalog.ValidateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, err
	}
	return resolveRelationSnapshot(auth, name, utility)
}

func resolveRelationSnapshot(auth AuthorizationView, name parser.ObjectName, utility bool) (ids.ObjectRef, error) {
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	if utility && len(name.Parts) == 2 {
		ns := auth.DefinitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = temporaryNamespace(auth)
		}
		if ns == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, SQLError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
	}
	path, err := LookupNamespacesSnapshot(auth, name, analyze.RelationLookup)
	if err != nil {
		if d, yes := err.(*sqlstate.Error); yes && utility {
			copy := *d
			copy.Position = ""
			err = &copy
		}
		return ids.ObjectRef{}, err
	}
	return ResolveRelationPathSnapshot(auth, name, path)
}

// Namespace authority is decided by the caller's lookup policy. Object lookup,
// diagnostics and own-TEMP admission remain identical for ordinary and VACUUM
// consumers; maintenance never changes the data-access policy.
func ResolveRelationPathSnapshot(auth AuthorizationView, name parser.ObjectName, path analyze.NamespaceSelection) (ids.ObjectRef, error) {
	relname := name.Parts[len(name.Parts)-1].Text
	var a ids.ObjectAddress
	var ok bool
	for i := range path.Count() {
		a = auth.DefinitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: path.At(i), Name: relname})
		ok = a != (ids.ObjectAddress{})
		if ok {
			break
		}
	}
	if !ok {
		return ids.ObjectRef{}, SQLError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
	}
	descriptor, exists := auth.definitionDescriptor(a)
	d, valid := descriptor.(*catalog.Relation)
	if !exists || !valid {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	if auth.Session != nil {
		namespace, _ := auth.definitionDescriptor(d.Namespace())
		if ns, ok := namespace.(*catalog.Namespace); ok && ns.Temporary == auth.Session.SessionToken {
			auth.Session.Transactions.AccessedTemporary = true
		}
	}
	return auth.ref(a), nil
}

func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}

// FrameImporter fills one canonical frame and completes input CHECKs in lexical
// order. It enters and returns without Engine.mu; SQL actual-expression import
// owns only its binding/capture lock and evaluates outside it without replay.
type FrameImporter func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error

func (e *Engine) Execute(op *session.Operation, stmt parser.Statement, count int, importFrame FrameImporter) (result Result, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = Result{}
		}
	}()
	return scope.Execute(stmt, count, importFrame)
}
