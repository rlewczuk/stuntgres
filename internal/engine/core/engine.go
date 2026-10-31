package core

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
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
	ReplacementMu                   replacementAdmission
	Catalog                         catalog.Store
	DatabaseName                    string
	DatabaseNameError               error
	Storage                         storage.Store
	sessionMu                       sync.Mutex
	sessions                        map[ids.SessionToken]*session.Session
	Settings                        settings.Registry // Immutable, initialized once before publication.
	Authority                       AuthorityInputs   // Points at settings and storage; shared by engine-built views.
	Visibility                      visibility.Registry
	IndexWaits                      index.WaitRegistry
	Exclusions                      index.ExclusionRegistry
	prepared                        session.PreparedRegistry
	RoleStore                       *access.RoleStore
	Cluster                         *DatabaseCluster
	ImportTargets                   map[authorization.PrincipalRef]authorization.PrincipalRef
	ImportParameterPredecessor      *catalog.State // Parameter authority captured before Restore input.
	importOriginProgressPredecessor *originProgressPredecessor
	Foreign                         fdw.Registry
	TextSearchFiles                 *functions.DictionaryFiles // Immutable Driver startup capability, never catalog state.
	DatabaseFrozenXID               uint64
	VacuumProgress                  map[ids.TxRef]VacuumProgress // Engine.mu; only actual work.
	Statistics                      statistics.Provider
	catalogCaptures                 catalogCaptureCache          // Engine.mu; latest shared pg_type/pg_proc rows.
	Statements                      Statements                   // Family dispatch; set by the composition, shared by every target.
	Factory                         EngineFactory                // Composed constructor; set by the composition, shared by every target.
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
	cluster := &DatabaseCluster{StartedAt: time.Now().UnixMicro() - activityEpochUnixMicros}
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
	cluster.ConfigLoadedAt = time.Now().UnixMicro() - activityEpochUnixMicros
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
	if err := e.RegisterCatalogProvider(catalog.StatisticsProvider{}); err != nil {
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
	if err := e.RegisterCatalogProvider(catalog.TransformProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.EventTriggerProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.SubscriptionProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.ReplicationOriginProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.PublicationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.PublicationRelationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.PublicationNamespaceProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.TSParserProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.TSConfigurationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.LargeObjectProvider{}); err != nil {
		return nil, err
	}
	if err := e.registerExtension("plpgsql", extension.StockPLpgSQL); err != nil {
		return nil, err
	}
	if err := e.registerExtension("tcn", extension.StockTcn); err != nil {
		return nil, err
	}
	if err := e.registerExtension("refint", extension.StockRefint); err != nil {
		return nil, err
	}
	if err := e.registerExtension("moddatetime", extension.StockModdatetime); err != nil {
		return nil, err
	}
	for _, registration := range []struct {
		name           string
		implementation extension.Installer
	}{{"autoinc", extension.StockAutoinc}, {"insert_username", extension.StockInsertUsername}, {"pageinspect", extension.StockPageinspect}, {"pgstattuple", extension.StockPgstattuple}, {"amcheck", extension.StockAmcheck}, {"hstore", extension.StockHstore}, {"isn", extension.StockIsn}, {"citext", extension.StockCitext}, {"cube", extension.StockCube}, {"earthdistance", extension.StockEarthdistance}, {"lo", extension.StockLo}, {"unaccent", extension.StockUnaccent}, {"seg", extension.StockSeg}, {"intarray", extension.StockIntarray}, {"ltree", extension.StockLtree}, {"fuzzystrmatch", extension.StockFuzzystrmatch}, {"intagg", extension.StockIntagg}, {"pgrowlocks", extension.StockPgrowlocks}, {"tablefunc", extension.StockTablefunc}, {"pgcrypto", extension.StockPgcrypto}, {"uuid-ossp", extension.StockUUIDOssp}} {
		if err := e.registerExtension(registration.name, registration.implementation); err != nil {
			return nil, err
		}
	}
	types := sqltype.Builtins()
	// The seed sets thousands of entries, so it is one bulk load.
	e.Catalog.PublishLocked(catalog.NewState(1))
	e.Catalog.BeginSeedLocked(builtinSeed, builtinCatalogCapacity(len(types)))
	bootstrap := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	principals := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	catalog.InstallBuiltinNamespaces(e.Catalog.State, principals.LookupRef("postgres"), principals.LookupRef("pg_database_owner"))
	catalog.InstallBuiltinTypes(e.Catalog.State, types, bootstrap)
	catalog.InstallBuiltinCollations(e.Catalog.State, bootstrap)
	procedures := routine.BuiltinProcedures(bootstrap)
	routine.SeedGenericFileACLs(procedures, bootstrap, principals.LookupRef("pg_monitor"))
	routine.SeedRecoveryControlACLs(procedures, bootstrap)
	routine.SeedServerSignalACLs(procedures, bootstrap)
	routine.SeedReplicationOriginACLs(procedures, bootstrap)
	routine.SeedWALPositionACLs(procedures, bootstrap)
	catalog.InstallBuiltinProcedures(e.Catalog.State, typedef.BuiltinConversionProcedures(bootstrap), procedures, routine.NativeAccessMethodProcedures(bootstrap), routine.SharedMemoryProcedures(bootstrap, principals.LookupRef("pg_read_all_stats")))
	catalog.InstallBuiltinDictionaries(e.Catalog.State, bootstrap, functions.DefaultSimpleDictionary)
	catalog.InstallBuiltinTSParsers(e.Catalog.State)
	catalog.InstallBuiltinTSConfigurations(e.Catalog.State, bootstrap)
	if err := e.Catalog.InstallBuiltinLanguagesLocked(bootstrap); err != nil {
		return nil, err
	}
	snowball, err := functions.DefaultSnowballDictionary()
	if err != nil {
		return nil, err
	}
	arabic, err := functions.DefaultArabicSnowballDictionary()
	if err != nil {
		return nil, err
	}
	armenian, err := functions.DefaultArmenianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	basque, err := functions.DefaultBasqueSnowballDictionary()
	if err != nil {
		return nil, err
	}
	danish, err := functions.DefaultDanishSnowballDictionary()
	if err != nil {
		return nil, err
	}
	dutch, err := functions.DefaultDutchSnowballDictionary()
	if err != nil {
		return nil, err
	}
	estonian, err := functions.DefaultEstonianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	german, err := functions.DefaultGermanSnowballDictionary()
	if err != nil {
		return nil, err
	}
	greek, err := functions.DefaultGreekSnowballDictionary()
	if err != nil {
		return nil, err
	}
	hindi, err := functions.DefaultHindiSnowballDictionary()
	if err != nil {
		return nil, err
	}
	hungarian, err := functions.DefaultHungarianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	irish, err := functions.DefaultIrishSnowballDictionary()
	if err != nil {
		return nil, err
	}
	italian, err := functions.DefaultItalianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	lithuanian, err := functions.DefaultLithuanianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	norwegian, err := functions.DefaultNorwegianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	portuguese, err := functions.DefaultPortugueseSnowballDictionary()
	if err != nil {
		return nil, err
	}
	yiddish, err := functions.DefaultYiddishSnowballDictionary()
	if err != nil {
		return nil, err
	}
	indonesian, err := functions.DefaultIndonesianSnowballDictionary()
	if err != nil {
		return nil, err
	}
	if err := e.Catalog.InstallSnowballLocked(bootstrap, snowball, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian); err != nil {
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
	if err := e.initializeInitialPrivileges(); err != nil {
		return nil, err
	}
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
func (e *Engine) Describe(op *session.Operation, stmt parser.Statement) (info Description, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return Description{}, err
	}
	defer func() { err = scope.Finish(err) }()
	return scope.Describe(stmt)
}
func (e *Engine) describeOperation(scope *StatementScope, stmt parser.Statement) (Description, error) {
	op := scope.Op
	scope.Input.LockedDomainInput = scope
	if err := e.validateOperation(op); err != nil {
		return Description{}, err
	}
	switch s := stmt.(type) {
	case *parser.AlterSystem, *parser.SetConstraints:
		return Description{}, nil
	case *parser.Explain:
		return e.describeExplain(scope, s)
	case *parser.CreateTransform, *parser.DropTransform:
		return Description{}, nil
	case *parser.Do, *parser.CreateFunction, *parser.CreateLanguage, *parser.AlterLanguage, *parser.DropLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateCast, *parser.DropCast:
		return Description{}, nil
	case *parser.ForeignDDL:
		return Description{}, nil
	case *parser.CreateDatabaseStmt, *parser.CreateTablespace, *parser.Load, *parser.SecurityLabel, *parser.Checkpoint, *parser.Vacuum, *parser.Analyze:
		return Description{}, nil
	case *parser.Truncate:
		return Description{}, nil
	case *parser.RefreshMaterializedView:
		return Description{}, nil
	case *parser.PrepareSQL, *parser.DeallocateSQL, *parser.Discard:
		return Description{}, nil
	case *parser.ExecuteSQL:
		if entry := PayloadOf(op.Session).PreparedSQL.Lookup(s.Name.Text); entry != nil {
			if !scope.RowSecurity && session.PreparableBody(entry.Body) {
				// Reuse the named statement's analyzed identities without changing
				// its session definition or executing its argument expressions.
				expected, parameters := scope.Expected, scope.initialParameters
				description := entry.Description
				scope.Expected = &description
				scope.initialParameters = description.Parameters.Types
				_, err := scope.Describe(entry.Body)
				scope.Expected, scope.initialParameters = expected, parameters
				if err != nil {
					return Description{}, err
				}
			}
			return Description{Results: entry.Description.Results}, nil
		}
		return Description{}, nil
	case *parser.FetchCursor:
		if s.Move {
			return Description{}, nil
		}
		if cursor := PayloadOf(op.Session).Cursors.Entries[s.Name.Text]; cursor != nil {
			return Description{Results: cursor.schema}, nil
		}
		return Description{}, nil
	case *parser.CloseCursor:
		return Description{}, nil
	case *parser.AlterOwner, *parser.ReassignOwned, *parser.DropOwned:
		return Description{}, nil
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger, *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return Description{}, nil
	case *parser.Subscription, *parser.Publication:
		return Description{}, nil
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger:
		return Description{}, nil
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return Description{}, nil
	case *parser.CreateComposite, *parser.AlterComposite, *parser.AlterType, *parser.DropType, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum, *parser.CreateDomain, *parser.DropDomain, *parser.AlterDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return Description{}, nil
	case *parser.CreateStatistics, *parser.DropStatistics, *parser.AlterStatisticsTarget, *parser.AlterStatisticsRename, *parser.AlterStatisticsSchema:
		return Description{}, nil
	case *parser.CreateParser, *parser.AlterParser, *parser.DropParser:
		return Description{}, nil
	case *parser.CreateTemplate, *parser.AlterTemplate, *parser.DropTemplate, *parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary, *parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration:
		return Description{}, nil
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return Description{}, nil
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return Description{}, nil
	case *parser.CreateExtension, *parser.DropExtension, *parser.AlterExtensionUpdate, *parser.AlterExtension:
		return Description{}, nil
	case *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		return Description{}, nil
	case *parser.RenameRole, *parser.AlterRoleSettings, *parser.AlterDatabaseSettings:
		return Description{}, nil
	case *parser.AlterTableAllInTablespace, *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableDefault, *parser.AlterTable, *parser.LockTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization, *parser.Set, *parser.Reset, *parser.TransactionCharacteristics:
		return Description{}, nil
	case *parser.RoleSelection:
		return Description{}, nil
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return Description{}, nil
	case *parser.Show:
		e.Mu.Lock()
		defer e.Mu.Unlock()
		readRestricted := false
		if !s.All {
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				return Description{}, err
			}
			readRestricted, err = auth.canReadRestrictedSettings()
			if err != nil {
				return Description{}, err
			}
		}
		op.EffectiveSettings().Mu.Lock()
		defer op.EffectiveSettings().Mu.Unlock()
		typ, _ := e.Catalog.Current().LookupType(sqltype.Text)
		if s.All {
			return Description{Results: settingColumns(typ, "name", "setting", "description")}, nil
		}
		if settings.FoldName(s.Name.Text) == "role" {
			return Description{Results: settingColumns(typ, "role")}, nil
		}
		d, err := op.EffectiveSettings().LookupDefinition(e.Settings, s.Name.Text, false)
		if err != nil {
			return Description{}, err
		}
		if err := session.SettingReadAccess(d, s.Name.Text, readRestricted); err != nil {
			return Description{}, err
		}
		return Description{Results: settingColumns(typ, settings.DisplayName(d.Name))}, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		return Description{}, err
	}
	auth, err := scope.statementAuthorizationLocked(stmt)
	if err != nil {
		return Description{}, err
	}
	return e.describeBoundOperationLocked(scope, stmt, auth, nil)
}

// The same description-only binding and rewrite admission serves Prepare and
// EXPLAIN. Its capture marks an EXPLAIN activation: the graph is borrowed only
// here, and its description has no child fold-publication rights.
func (e *Engine) describeBoundOperationLocked(scope *StatementScope, stmt parser.Statement, auth AuthorizationView, capture *explainBound) (Description, error) {
	op := scope.Op
	b, err := e.describeScoped(scope, stmt, auth, nil)
	if err != nil {
		return Description{}, err
	}
	// Literal lifetimes belong to the caller's raw SQL, including its CTEs.
	// Stored rule/view recipes carry their own captures and invalidation events.
	callerSequences, callerRegtypes := b.Prepared.SequenceConstants, b.Prepared.RegtypeConstants
	if b.ViewWrite != nil {
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			return Description{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, false); err != nil {
		return Description{}, err
	}
	if err := scope.ValidateQuerySources(b.QuerySources); err != nil {
		return Description{}, err
	}
	if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, auth, stmt, &b); err != nil {
		return Description{}, err
	}
	if relation := view.BindingRuleRelation(b); relation != nil && len(stored.RulesOf(relation)) != 0 {
		program := view.RuleProgram{Count: -1, Returning: -1}
		if _, err := (view.RuleRewriter{Owner: &e.Catalog.Owner}).ExpandLocked(auth, stmt, b, nil, &program); err != nil {
			return Description{}, err
		}
		for i := range program.Commands {
			command := &program.Commands[i]
			if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, command.Auth, command.Syntax, &command.Binding); err != nil {
				return Description{}, err
			}
		}
		if err := view.CheckRowSecurityRuleProgram(scope.RowSecurity, auth.View, b, program); err != nil {
			return Description{}, err
		}
		b.Dependencies = append(b.Dependencies, program.Events...)
		if capture != nil {
			capture.Rules = &program
		}
		for _, command := range program.Commands {
			if command.Binding.Relation != nil {
				b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: command.Binding.Ref, Revision: command.Binding.Relation.Revision()})
			}
			b.Dependencies = append(b.Dependencies, command.Binding.Dependencies...)
		}
	} else if err := view.CheckRowSecurityBinding(scope.RowSecurity, auth.View, b); err != nil {
		return Description{}, err
	}
	var results []catalog.Column
	if _, call := stmt.(*parser.Call); call {
		results = analyze.RoutineCallSchema(b)
	}
	if b.Query != nil && !b.CommandQuery {
		results = b.Query.Root.Schema()
	}
	if b.Returning != nil {
		results = b.Returning.Schema
	}
	analysis, err := e.sequenceAnalysisLocked(auth, callerSequences)
	if err != nil {
		return Description{}, err
	}
	var cell *sequence.Analysis
	if len(analysis.Constants) != 0 {
		cell = &analysis
	}
	regtypes, err := e.regtypeAnalysisLocked(auth, callerRegtypes)
	if err != nil {
		return Description{}, err
	}
	var prepared *Analysis
	if session.PreparableBody(stmt) {
		prepared, err = e.preparedAnalysisLocked(scope, auth, b)
		if err != nil {
			return Description{}, err
		}
	}
	if scope.Expected != nil {
		analyze.ReuseRecordConstructors(b.Prepared.Types, scope.Expected.Types)
	}
	// Keep the selected invocation and its nonexecuting output projection in
	// this activation only. Execution must not choose a second CALL winner.
	if scope.sqlRoutine.lexical != nil {
		if _, call := stmt.(*parser.Call); call {
			scope.sqlRoutine.plCallBinding = &b
		}
	}
	if capture != nil {
		capture.Binding = b
	}
	var folds *PreparedFolds
	if capture != nil {
		folds = &PreparedFolds{}
	} else {
		folds = NewPreparedFolds()
	}
	return Description{Parameters: b.Parameters, Results: results, CatalogOwner: auth.Catalog.Owner(), CatalogEpoch: auth.Catalog.Epoch(), Temporals: b.Prepared.Temporals, Types: b.Prepared.Types, Sequences: cell, Regtypes: regtypes, Analysis: prepared, Folds: folds, PlanOwner: op.Session.SessionToken, PlanGeneration: op.Session.DiscardPlansGeneration}, nil
}

// requiredRef is the exact acquired DML/utility target; query source occurrences
// have their own admission manifest. Binding validates namespace authority and
// cannot retarget an acquired identity. CREATE supplies the zero reference.
func Describe(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *Description, admission *analyze.LockAdmission, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return DescribeWithRule(ctx, host, stmt, auth, requiredRef, input, expected, admission, nil, initial...)
}

func DescribeWithRule(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *Description, admission *analyze.LockAdmission, rule *analyze.RuleNamespace, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return analyze.Describe(ctx, host, stmt, AuthorityOf(auth), requiredRef, input, expectedOf(expected), admission, rule, false, initial...)
}

func (e *Engine) describeScoped(scope *StatementScope, stmt parser.Statement, auth AuthorizationView, expected *Description) (binding analyze.Binding, err error) {
	defer func() {
		if err == nil {
			err = scope.routineReadOnlyEligibility(stmt, auth.Catalog, binding.QuerySources, binding.Query)
		}
	}()
	if scope.sqlRoutine.plCallBinding != nil {
		return *scope.sqlRoutine.plCallBinding, nil
	}
	if scope.sqlRoutine.binding != nil {
		return *scope.sqlRoutine.binding, nil
	}
	if scope.sqlRoutine.lexical != nil {
		return analyze.DescribePL(scope.executionContext(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, &scope.QueryLockAdmission, scope.sqlRoutine.lexical, scope.sqlRoutine.assignment)
	}
	if scope.sqlRoutine.definition != nil {
		return analyze.DescribeRoutine(scope.executionContext(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, expectedOf(expected), &scope.QueryLockAdmission, scope.sqlRoutine.definition, scope.sqlRoutine.name, scope.sqlRoutine.inputCollation)
	}
	// Dynamic USING owns a closed positional frame, including when empty.
	// Static PL queries returned above; ordinary public parameters infer freely.
	return analyze.Describe(scope.executionContext(), e.AnalysisHost(scope.Op), stmt, AuthorityOf(auth), scope.LockedRef, scope.Input, expectedOf(expected), &scope.QueryLockAdmission, nil, scope.sqlRoutine.cursor != nil || scope.sqlRoutine.closedParameters, scope.initialParameters)
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
