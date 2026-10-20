package engine

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Engine struct {
	mu            sync.Mutex
	locks         *lock.Manager
	analysis      bindingHost                                // Binding host without an operation; immutable after New.
	notifications map[ids.SessionToken]*sessionNotifications // Engine.mu; committed registrations only.
	// Implicit row work finishes before replacement; explicit work is epoch-fenced.
	replacementMu        replacementAdmission
	catalog              catalogStore
	databaseName         string
	databaseNameError    error
	storage              storage.Store
	sessionMu            sync.Mutex
	sessions             map[ids.SessionToken]*Session
	settings             settings.Registry // Immutable, initialized once before publication.
	visibility           visibility.Registry
	indexBuildWaits      map[ids.ObjectAddress]indexBuildWait
	exclusionPending     map[*lock.Ticket]*exclusionProjection
	exclusionGeneration  uint64
	prepared             map[string]*preparedTransaction
	roles                roleStore
	importTargets        map[authorization.PrincipalRef]authorization.PrincipalRef
	foreignWrappers      []ForeignDataWrapperDefinition
	foreignValidators    []ForeignValidatorDefinition
	foreignNamespaces    map[ids.ObjectAddress]string // Immutable auto-installed namespace identities.
	foreignValueImporter exec.ForeignValueImporter
	foreignValueExporter exec.ForeignValueExporter
}
type Result struct {
	CommandTag string
	Count      int64
	Rows       exec.RowResult
	RowSets    []exec.RowResult
	created    ids.ObjectRef // Exact ordinary CREATE TABLE outcome; zero for a no-op.
}

func New() *Engine {
	e, err := newEngine(nil, nil, "", false)
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...catalog.Provider) (*Engine, error) {
	return newEngine(providers, nil, "", false)
}

// NewWithSettings consumes definitions validated before driver publication.
func NewWithSettings(definitions []settings.Definition, database string, trackCommitTimestamp bool) *Engine {
	e, err := newEngine(nil, definitions, database, trackCommitTimestamp)
	if err != nil {
		panic(err)
	}
	return e
}

func newEngine(providers []catalog.Provider, definitions []settings.Definition, database string, trackCommitTimestamp bool) (*Engine, error) {
	// Builtin descriptors use revision 1; allocations start beyond that frontier.
	e := &Engine{catalog: catalogStore{Owner: catalog.Owner{Domain: &ids.Domain{}, Providers: map[ids.OID]catalog.Provider{}}, nextOID: 16384, nextRevision: 2}}
	e.analysis = bindingHost{engine: e}
	e.mu.Lock()
	e.storage.ReplaceRelationsLocked(map[ids.ObjectAddress]*storage.Relation{})
	e.mu.Unlock()
	e.catalog.Principals, e.catalog.ACLAuthority = &e.roles.domain, &e.roles.aclAuthority
	e.visibility = visibility.NewRegistry(trackCommitTimestamp)
	e.locks = lock.New(e.catalog.Domain)
	e.mu.Lock()
	e.storage.ReplaceSequencesLocked(make(map[ids.ObjectAddress]*storage.SequenceGenerator))
	e.mu.Unlock()
	e.roles.initialize()
	e.settings = settings.NewRegistry(definitions, e.visibility.TracksCommitTimestamps())
	for _, p := range providers {
		if err := e.RegisterCatalogProvider(p); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(catalog.NamespaceProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(relationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(typeProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.CollationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.ConversionProvider{}); err != nil {
		return nil, err
	}
	for _, class := range []ids.OID{ids.ForeignWrapperClassID, ids.ForeignServerClassID, ids.ForeignMappingClassID} {
		if err := e.RegisterCatalogProvider(catalog.ForeignCatalogProvider{class}); err != nil {
			return nil, err
		}
	}
	for _, p := range []catalog.Provider{extension.CatalogProvider{ids.ExtensionClassID}, procedureProvider{}, extension.CatalogProvider{ids.TSTemplateClassID}, extension.CatalogProvider{ids.TSDictionaryClassID}} {
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
	if err := e.RegisterCatalogProvider(parameterProvider{&e.settings}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.TablespaceProvider{}); err != nil {
		return nil, err
	}
	for _, registration := range []struct {
		name           string
		implementation extensionInstaller
	}{{"cube", cubeInstaller{extension.StockCube}}, {"unaccent", unaccentInstaller{extension.StockUnaccent}}, {"intarray", intarrayInstaller{extension.StockIntarray}}, {"fuzzystrmatch", fuzzystrmatchInstaller{extension.StockFuzzystrmatch}}, {"intagg", intaggInstaller{extension.StockIntagg}}, {"pgcrypto", pgcryptoInstaller{extension.StockPgcrypto}}, {"uuid-ossp", uuidOsspInstaller{extension.StockUUIDOssp}}} {
		if err := e.registerExtension(registration.name, registration.implementation); err != nil {
			return nil, err
		}
	}
	types := sqltype.Builtins()
	e.catalog.state = catalog.NewStateSized(1, builtinCatalogCapacity(len(types)))
	for _, ns := range []struct {
		oid  ids.OID
		name string
	}{{ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {ids.PublicNamespaceOID, "public"}, {13229, "information_schema"}} {
		owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		if ns.oid == ids.PublicNamespaceOID {
			owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
		}
		d := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.oid}, ObjectName: ns.name, ObjectRevision: 1, Owner: owner}
		if ns.oid != ids.PGToastNamespaceOID {
			d.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
			}}
		}
		d.Comment = namespaceCommentBaseline(ns.oid)
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}, d.Address())
	}
	for _, typ := range types {
		d := &catalog.Type{SQLType: typ, Comment: builtinTypeComment(typ.ID()), Array: typ.AssociatedArray(), Owner: e.roles.domain.Principal(authorization.BootstrapPrincipalID)}
		if typ.IsDomain() && typ.Concrete() {
			d.ObjectRevision = 1
		}
		if typ.NamespaceName() == "information_schema" {
			d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}
		}
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	for i := range catalog.BuiltinCollations {
		d := new(catalog.Collation)
		*d = catalog.BuiltinCollations[i]
		d.Owner = e.roles.domain.Principal(10)
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.installBuiltinConversionRoutines()
	e.installBuiltinProcedures()
	e.initializeTablespaces()
	for i := range catalog.BuiltinConversions {
		d := new(catalog.Conversion)
		*d = catalog.BuiltinConversions[i]
		d.Owner = e.roles.domain.Principal(10)
		d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		d.ObjectRevision = 1
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.installBuiltinOperators()
	e.seedProcedureNames()
	e.initializeDatabase(database)
	if err := e.installBuiltinDomainChecks(); err != nil {
		return nil, err
	}
	if err := e.initializeCatalogSources(); err != nil {
		return nil, err
	}
	if err := e.validateSeed(); err != nil {
		return nil, err
	}
	return e, nil
}
func (e *Engine) CatalogRead() catalog.Read {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog.read(e.catalog.state)
}

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
func (e *Engine) Describe(op *Operation, stmt parser.Statement) (info StatementDescription, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return StatementDescription{}, err
	}
	defer func() { err = scope.Finish(err) }()
	return scope.Describe(stmt)
}
func (e *Engine) describeOperation(scope *StatementScope, stmt parser.Statement) (StatementDescription, error) {
	op := scope.op
	scope.input.LockedDomainInput = scope
	if err := e.validateOperation(op); err != nil {
		return StatementDescription{}, err
	}
	switch s := stmt.(type) {
	case *parser.CreateFunction:
		return StatementDescription{}, nil
	case *parser.ForeignDDL:
		return StatementDescription{}, nil
	case *parser.CreateTablespace:
		return StatementDescription{}, nil
	case *parser.Truncate:
		return StatementDescription{}, nil
	case *parser.RefreshMaterializedView:
		return StatementDescription{}, nil
	case *parser.PrepareSQL, *parser.DeallocateSQL, *parser.Discard:
		return StatementDescription{}, nil
	case *parser.ExecuteSQL:
		if entry := payloadOf(op.Session).preparedSQL.entries[s.Name.Text]; entry != nil {
			if !scope.rowSecurity && preparableBody(entry.body) {
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
		if cursor := payloadOf(op.Session).cursors.entries[s.Name.Text]; cursor != nil {
			return StatementDescription{Results: cursor.schema}, nil
		}
		return StatementDescription{}, nil
	case *parser.CloseCursor:
		return StatementDescription{}, nil
	case *parser.AlterOwner, *parser.ReassignOwned, *parser.DropOwned:
		return StatementDescription{}, nil
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return StatementDescription{}, nil
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return StatementDescription{}, nil
	case *parser.CreateComposite, *parser.AlterComposite, *parser.DropComposite, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateEnum, *parser.AlterEnum, *parser.CreateDomain, *parser.DropDomain, *parser.AlterDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return StatementDescription{}, nil
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
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
		e.mu.Lock()
		defer e.mu.Unlock()
		op.Session.Settings.Mu.Lock()
		defer op.Session.Settings.Mu.Unlock()
		typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
		if s.All {
			return StatementDescription{Results: settingColumns(typ, "name", "setting", "description")}, nil
		}
		d, err := e.sessionSetting(op.Session.Settings, s.Name.Text, false)
		if err != nil {
			return StatementDescription{}, err
		}
		return StatementDescription{Results: settingColumns(typ, settings.DisplayName(d.Name))}, nil
	case *parser.SettingCall:
		e.mu.Lock()
		defer e.mu.Unlock()
		binding, err := e.describeSettingCall(op.Ctx, s, &scope.input, op)
		typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
		columns := settingColumns(typ, s.Name)
		columns[0].Nullable = true
		return StatementDescription{Parameters: binding.parameters, Results: columns}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		return StatementDescription{}, err
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
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
	if err := scope.validateQuerySources(b.QuerySources); err != nil {
		return StatementDescription{}, err
	}
	if err := e.normalizeRuleCTEsLocked(scope, auth, stmt, &b); err != nil {
		return StatementDescription{}, err
	}
	if relation := bindingRuleRelation(b); relation != nil && len(rulesOf(relation)) != 0 {
		program := ruleProgram{count: -1, returning: -1}
		if _, err := e.expandRulesLocked(scope, auth, stmt, b, nil, &program); err != nil {
			return StatementDescription{}, err
		}
		for i := range program.commands {
			command := &program.commands[i]
			if err := e.normalizeRuleCTEsLocked(scope, command.auth, command.syntax, &command.binding); err != nil {
				return StatementDescription{}, err
			}
		}
		if err := checkRowSecurityRuleProgram(scope, auth, b, program); err != nil {
			return StatementDescription{}, err
		}
		b.Dependencies = append(b.Dependencies, program.events...)
		for _, command := range program.commands {
			if command.binding.Relation != nil {
				b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: command.binding.Ref, Revision: command.binding.Relation.Revision()})
			}
			b.Dependencies = append(b.Dependencies, command.binding.Dependencies...)
		}
	} else if err := checkRowSecurityBinding(scope, auth, b); err != nil {
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
	if preparableBody(stmt) {
		prepared, err = e.preparedAnalysisLocked(scope, auth, b)
		if err != nil {
			return StatementDescription{}, err
		}
	}
	return StatementDescription{Parameters: b.Parameters, Results: results, catalogOwner: auth.Catalog.Owner(), catalogEpoch: auth.Catalog.Epoch(), temporals: b.Prepared.Temporals, composites: b.Prepared.Composites, sequences: cell, regtypes: regtypes, analysis: prepared, planOwner: op.Session.SessionToken, planGeneration: op.Session.DiscardPlansGeneration}, nil
}

// requiredRef is the exact acquired DML/utility target; query source occurrences
// have their own admission manifest. Binding validates namespace authority and
// cannot retarget an acquired identity. CREATE supplies the zero reference.
func describe(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *StatementDescription, admission *analyze.LockAdmission, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return describeWithRule(ctx, host, stmt, auth, requiredRef, input, expected, admission, nil, initial...)
}

func describeWithRule(ctx context.Context, host analyze.Host, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected *StatementDescription, admission *analyze.LockAdmission, rule *analyze.RuleNamespace, initial ...[]sqltype.Usage) (analyze.Binding, error) {
	return analyze.Describe(ctx, host, stmt, authorityOf(auth), requiredRef, input, expectedOf(expected), admission, rule, false, initial...)
}

func (e *Engine) describeScoped(scope *StatementScope, stmt parser.Statement, auth AuthorizationView, expected *StatementDescription) (analyze.Binding, error) {
	return analyze.Describe(scope.op.Context(), e.analysisHost(scope.op), stmt, authorityOf(auth), scope.lockedRef, scope.input, expectedOf(expected), &scope.queryLockAdmission, nil, scope.sqlRoutine.scalar, scope.initialParameters)
}

// Resolution helpers run under e.mu, using the supplied coherent view.

func (e *Engine) resolveReference(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	ref, err := e.resolveRelationReference(auth, name, false)
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
func (e *Engine) resolveRelationReference(auth AuthorizationView, name parser.ObjectName, utility bool) (ids.ObjectRef, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, err
	}
	return e.resolveRelationSnapshot(auth, name, utility)
}

func (e *Engine) resolveRelationSnapshot(auth AuthorizationView, name parser.ObjectName, utility bool) (ids.ObjectRef, error) {
	view := auth.Catalog
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	relname := name.Parts[len(name.Parts)-1].Text
	var a ids.ObjectAddress
	var ok bool
	if utility && len(name.Parts) == 2 {
		ns := auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = temporaryNamespace(auth)
		}
		if ns == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
	}
	path, err := e.lookupNamespacesSnapshot(auth, name, analyze.RelationLookup)
	if err != nil {
		if d, yes := err.(*sqlstate.Error); yes && utility {
			copy := *d
			copy.Position = ""
			err = &copy
		}
		return ids.ObjectRef{}, err
	}
	for i := range path.Count() {
		a = auth.definitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: path.At(i), Name: relname})
		ok = a != (ids.ObjectAddress{})
		if ok {
			break
		}
	}
	if !ok {
		return ids.ObjectRef{}, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
	}
	descriptor, exists := auth.definitionDescriptor(a)
	d, valid := descriptor.(*catalog.Relation)
	if !exists || !valid {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	if auth.session != nil {
		namespace, _ := auth.definitionDescriptor(d.Namespace())
		if ns, ok := namespace.(*catalog.Namespace); ok && ns.Temporary == auth.session.SessionToken {
			auth.session.Transactions.AccessedTemporary = true
		}
	}
	return e.catalog.Domain.Ref(view.Epoch(), a), nil
}

func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}

func creationError(err error, name parser.ObjectName) error {
	if diagnostic, ok := err.(*sqlstate.Error); ok && len(name.Parts) > 0 {
		copy := *diagnostic
		copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
		return &copy
	}
	return err
}

func notNullDetailError(schema, table, column, detail string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: fmt.Sprintf("null value in column \"%s\" of relation \"%s\" violates not-null constraint", column, table),
		Schema:  schema, Table: table, Column: column, Detail: detail,
	}
}

// FrameImporter fills one canonical frame and completes input CHECKs in lexical
// order. It enters and returns without Engine.mu; SQL actual-expression import
// owns only its binding/capture lock and evaluates outside it without replay.
type FrameImporter func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error

func (e *Engine) Execute(op *Operation, stmt parser.Statement, count int, importFrame FrameImporter) (result Result, err error) {
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
