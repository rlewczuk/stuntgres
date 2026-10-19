package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
)

type Engine struct {
	mu            sync.Mutex
	locks         *lock.Manager
	notifications map[ids.SessionToken]*sessionNotifications // Engine.mu; committed registrations only.
	// Implicit row work finishes before replacement; explicit work is epoch-fenced.
	replacementMu        replacementAdmission
	catalog              catalogStore
	databaseName         string
	databaseNameError    error
	storage              storage.Store
	sessionMu            sync.Mutex
	sessions             map[ids.SessionToken]*Session
	settingsDefinitions  map[string]SettingDefinition
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
	foreignValueImporter ForeignValueImporter
	foreignValueExporter ForeignValueExporter
	settingsOrder        []string
}
type ParameterInfo struct {
	Count int
	Types []sqltype.Usage
}
type Result struct {
	CommandTag string
	Count      int64
	Rows       RowResult
	RowSets    []RowResult
	created    ids.ObjectRef // Exact ordinary CREATE TABLE outcome; zero for a no-op.
}
type binding struct {
	ruleTemplate          bool
	insertSubquerySource  bool // Analyzed SELECT or VALUES with query clauses; normalization never changes source ownership.
	relation              *catalog.Relation
	storage               *storage.Relation
	ref                   ids.ObjectRef
	viewWrite             *viewWrite
	targets               []int
	arrayTargets          []*arrayAssignmentTarget
	usedColumns           []int
	query                 *BoundQuery
	ctes                  []*cteDefinition
	cteSelectBodies       bool
	ruleCTEs              map[*cteDefinition]*ruleCTECommand
	querySources          []queryLeaf
	visibilitySnapshot    bool
	subqueryDemand        bool
	viewLiterals          map[viewLiteralPath]literalExpression
	viewCastUsages        map[viewLiteralPath]sqltype.Usage
	viewIntaggRefs        map[viewLiteralPath]ids.ObjectRef
	viewOperatorRefs      map[viewLiteralPath]ids.ObjectRef
	viewComposites        map[viewLiteralPath]compositeOccurrence
	viewSimilarSubstrings map[viewLiteralPath]bool
	viewPosixSubstrings   map[viewLiteralPath]bool
	viewCollations        map[viewLiteralPath]uint32
	viewColumns           map[viewLiteralPath]viewColumn
	viewStars             map[viewLiteralPath][]viewColumn
	viewNamespaces        []viewNamespace
	commandQuery          bool
	update                *boundUpdate
	merge                 *boundMerge
	deleteUsing           LogicalNode
	deleteSourceWidth     int
	currentOf             string
	returning             *boundReturning
	dependencies          []catalog.ExpressionDependency
	parameters            ParameterInfo
	expressions           [][]expr.Expr
	insertRows            [][]parser.Expr
	createColumns         []catalog.Column
	createNotNull         []catalog.NotNullConstraint
	createColumnIndex     map[string]int
	createUniques         []createUniqueConstraint
	createDeclarations    []createColumnDeclaration
	createLikes           []createLikeInput
	createOfType          *catalog.Type
	createInheritance     []createInheritanceInput
	createLocalColumns    map[string]bool
	defaults              []expr.Expr
	defaultOrigins        []defaultOrigin
	identityDiscard       []bool
	insertProjection      []int
	conflict              *boundInsertConflict
	temporals             []temporalConstant
	composites            map[preparedCompositeKey]compositeOccurrence
	predicate             expr.Expr
	sequenceConstants     []sequenceConstant
	regclassConcat        bool
	regclassInput         sqltype.RegclassResolver
	regtypeConstants      []regtypeConstant
	regtypeInput          sqltype.RegtypeResolver
	regroleInput          sqltype.RegroleResolver
	operatorInput         sqltype.OperatorResolver
	regprocInput          sqltype.RoutineResolver
	regcollationInput     sqltype.RegcollationResolver
	regnamespaceInput     sqltype.RegnamespaceResolver
	regTSInput            sqltype.RegTSResolver
	aclitemInput          sqltype.ACLItemResolver
	ftsInput              sqltype.FTSInputNotices
	typeDiagnosticName    func(sqltype.ID) string
	enumMemberSafe        func(sqltype.ID, int64) bool
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
func NewWithSettings(definitions []SettingDefinition, database string, trackCommitTimestamp bool) *Engine {
	e, err := newEngine(nil, definitions, database, trackCommitTimestamp)
	if err != nil {
		panic(err)
	}
	return e
}

func newEngine(providers []catalog.Provider, definitions []SettingDefinition, database string, trackCommitTimestamp bool) (*Engine, error) {
	// Builtin descriptors use revision 1; allocations start beyond that frontier.
	e := &Engine{catalog: catalogStore{Owner: catalog.Owner{Domain: &ids.Domain{}, Providers: map[ids.OID]catalog.Provider{}}, nextOID: 16384, nextRevision: 2}, storage: storage.Store{Relations: map[ids.ObjectAddress]*storage.Relation{}}}
	e.catalog.Principals, e.catalog.ACLAuthority = &e.roles.domain, &e.roles.aclAuthority
	e.visibility = visibility.NewRegistry(trackCommitTimestamp)
	e.locks = lock.New(e.catalog.Domain)
	e.storage.Sequences = make(map[ids.ObjectAddress]*storage.SequenceGenerator)
	e.roles.initialize()
	e.initializeSettings(definitions)
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
	for _, class := range []ids.OID{ids.ExtensionClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		if err := e.RegisterCatalogProvider(extensionCatalogProvider{class}); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(catalog.DatabaseProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(operatorProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(parameterProvider{e.settingsDefinitions}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(catalog.TablespaceProvider{}); err != nil {
		return nil, err
	}
	for _, extension := range []struct {
		name           string
		implementation extensionInstaller
	}{{"unaccent", stockUnaccent}, {"intarray", stockIntarray}, {"fuzzystrmatch", stockFuzzystrmatch}, {"intagg", stockIntagg}, {"pgcrypto", stockPgcrypto}, {"uuid-ossp", stockUUIDOssp}} {
		if err := e.registerExtension(extension.name, extension.implementation); err != nil {
			return nil, err
		}
	}
	e.catalog.state = catalog.NewState(1)
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
	for _, typ := range sqltype.Builtins() {
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
	e.initializeConversionRoutines()
	e.initializeBuiltinRoutines()
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
	e.initializeOperators()
	e.initializeDatabase(database)
	if err := e.initializeBuiltinDomainChecks(); err != nil {
		return nil, err
	}
	if err := e.initializeCatalogSources(); err != nil {
		return nil, err
	}
	view := e.catalog.read(e.catalog.state)
	for _, d := range e.catalog.state.Descriptors() {
		p := e.catalog.Provider(d.Address().ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: missing catalog provider for built-in %q (class %d)", d.Name(), d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return nil, fmt.Errorf("stuntgres: invalid built-in catalog descriptor %q (class %d): %w", d.Name(), d.Address().ClassID, err)
		}
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
		if entry := op.session.preparedSQL.entries[s.Name.Text]; entry != nil {
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
		if cursor := op.session.cursors.entries[s.Name.Text]; cursor != nil {
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
	case *parser.CreateExtension, *parser.DropExtension:
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
		op.session.settings.mu.Lock()
		defer op.session.settings.mu.Unlock()
		typ, _ := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
		if s.All {
			return StatementDescription{Results: settingColumns(typ, "name", "setting", "description")}, nil
		}
		d, err := e.sessionSetting(op.session.settings, s.Name.Text, false)
		if err != nil {
			return StatementDescription{}, err
		}
		return StatementDescription{Results: settingColumns(typ, settingDisplayName(d.Name))}, nil
	case *parser.SettingCall:
		e.mu.Lock()
		defer e.mu.Unlock()
		binding, err := e.describeSettingCall(op.ctx, s, &scope.input, op)
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
	b, err := e.describe(op.ctx, stmt, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission, scope.initialParameters)
	if err != nil {
		return StatementDescription{}, err
	}
	if b.viewWrite != nil {
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			return StatementDescription{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, false); err != nil {
		return StatementDescription{}, err
	}
	if err := scope.validateQuerySources(b.querySources); err != nil {
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
		b.dependencies = append(b.dependencies, program.events...)
		for _, command := range program.commands {
			if command.binding.relation != nil {
				b.dependencies = append(b.dependencies, catalog.ExpressionDependency{Ref: command.binding.ref, Revision: command.binding.relation.Revision()})
			}
			b.dependencies = append(b.dependencies, command.binding.dependencies...)
			b.sequenceConstants = append(b.sequenceConstants, command.binding.sequenceConstants...)
			b.regtypeConstants = append(b.regtypeConstants, command.binding.regtypeConstants...)
		}
	} else if err := checkRowSecurityBinding(scope, auth, b); err != nil {
		return StatementDescription{}, err
	}
	var results []catalog.Column
	if b.query != nil && !b.commandQuery {
		results = b.query.Root.Schema()
	}
	if b.returning != nil {
		results = b.returning.schema
	}
	analysis, err := e.sequenceAnalysisLocked(auth, b.sequenceConstants)
	if err != nil {
		return StatementDescription{}, err
	}
	var cell *sequenceAnalysis
	if len(analysis.constants) != 0 {
		cell = &analysis
	}
	regtypes, err := e.regtypeAnalysisLocked(auth, b.regtypeConstants)
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
	return StatementDescription{Parameters: b.parameters, Results: results, catalogOwner: auth.Catalog.Owner(), catalogEpoch: auth.Catalog.Epoch(), temporals: b.temporals, composites: b.composites, sequences: cell, regtypes: regtypes, analysis: prepared, planOwner: op.session.token, planGeneration: op.session.discardPlansGeneration}, nil
}
func objectNameText(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}

// requiredRef is the exact acquired DML/utility target; query source occurrences
// have their own admission manifest. Binding validates namespace authority and
// cannot retarget an acquired identity. CREATE supplies the zero reference.
func (e *Engine) describe(ctx context.Context, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription, admission *queryLockAdmission, initial ...[]sqltype.Usage) (binding, error) {
	return e.describeWithRule(ctx, stmt, auth, requiredRef, input, op, expected, admission, nil, initial...)
}

func (e *Engine) describeWithRule(ctx context.Context, stmt parser.Statement, auth AuthorizationView, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription, admission *queryLockAdmission, rule *ruleNamespace, initial ...[]sqltype.Usage) (binding, error) {
	b := binding{}
	binder := newExpressionBinder(ctx, e, input, op, expected)
	binder.ruleNamespace = rule
	binder.noParameters = rule != nil
	if len(initial) != 0 && initial[0] != nil {
		binder.seedParameters(initial[0])
	}
	binder.queryLockAdmission = admission
	binder.queryAuth = &auth
	if err := e.captureEnumOperationInput(auth, &binder.input); err != nil {
		return b, err
	}
	if binder.queryLockAdmission == nil {
		manifest, _, _, err := newQueryLockAdmission(stmt)
		if err != nil {
			return b, err
		}
		binder.queryLockAdmission = &manifest
	}
	cteRestriction := ""
	switch original := stmt.(type) {
	case *parser.CreateQueryTable:
		if original.Form == parser.CreateMaterializedView && parser.HasModifyingCTE(original.Query) {
			cteRestriction = "materialized views must not use data-modifying statements in WITH"
		}
	case *parser.DeclareCursor:
		if parser.HasModifyingCTE(original.Query) {
			cteRestriction = "DECLARE CURSOR must not contain data-modifying statements in WITH"
		}
	}
	var view *parser.CreateView
	materialized := false
	if s, ok := stmt.(*parser.CreateView); ok {
		if parser.LeftmostSelect(s.Query).Into != nil {
			return b, sqlError("0A000", "views must not contain SELECT INTO")
		}
		view = s
		stmt = s.Query
		binder.noParameters = true
		b.commandQuery = true
	}
	if s, ok := stmt.(*parser.CreateQueryTable); ok {
		materialized = s.Form == parser.CreateMaterializedView
		binder.materializedDefinition = materialized
		stmt = s.Query
		b.commandQuery = true
	}
	if _, selecting := stmt.(*parser.Select); !selecting {
		if definitions := parser.StatementWith(stmt); len(definitions) != 0 {
			var err error
			binder.statementCTEs, err = binder.bindCTEDefinitions(definitions, parser.StatementWithRecursive(stmt))
			if err != nil {
				return b, err
			}
		}
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		var columns []catalog.Column
		declarations := make([]createColumnDeclaration, 0, len(s.Elements))
		table := s.Name.Parts[len(s.Name.Parts)-1].Text
		var inherited []createLikeConstraint
		if s.OfType != nil {
			var err error
			columns, declarations, b.createOfType, err = e.bindTypedColumns(auth, s)
			if err != nil {
				return b, err
			}
		}
		for elementIndex, element := range s.Elements {
			kinds := 0
			if element.Column != nil {
				kinds++
			}
			if element.Like != nil {
				kinds++
			}
			if element.NotNull != nil {
				kinds++
			}
			if element.Check != nil {
				kinds++
			}
			if element.Unique != nil {
				kinds++
			}
			if element.Exclusion != nil {
				kinds++
			}
			if element.ForeignKey != nil {
				kinds++
			}
			if kinds != 1 {
				return b, fmt.Errorf("stuntgres: invalid table element")
			}
			if element.NotNull != nil || element.Check != nil || element.Unique != nil || element.Exclusion != nil || element.ForeignKey != nil {
				continue
			}
			if c := element.Column; c != nil {
				if s.OfType != nil {
					continue
				}
				typeName := c.TypeName
				if lowered := serialTypeName(typeName); lowered != "" {
					typeName.Name = lowered
				}
				u, err := e.resolveTableColumnType(typeName, op)
				if err != nil {
					return b, err
				}
				u, err = e.resolveColumnCollation(u, c.Collation, op)
				if err != nil {
					return b, err
				}
				declaration, err := bindCreateColumnDeclaration(c, table)
				if err != nil {
					return b, err
				}
				if declaration.identity != nil && u.Type.ID() != sqltype.Int2 && u.Type.ID() != sqltype.Int4 && u.Type.ID() != sqltype.Int8 {
					return b, sequenceOptionError("sequence type must be smallint, integer, or bigint")
				}
				declarations = append(declarations, declaration)
				columns = append(columns, catalog.Column{Name: c.Name, Usage: u, DeclaredDimensions: int16(len(typeName.ArrayBounds)), Nullable: true, NullabilityKnown: true})
			} else {
				like := element.Like
				ref, err := e.resolveReference(auth, like.Name)
				if err != nil {
					if diagnostic, ok := err.(*sqlstate.Error); ok {
						copy := *diagnostic
						if copy.Code == "42P01" {
							copy.Message = fmt.Sprintf(`relation "%s" does not exist`, objectNameText(like.Name))
						}
						copy.Position = ""
						if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
							copy.Position = strconv.Itoa(pos)
						}
						err = &copy
					}
					return b, err
				}
				source := auth.Catalog.MustRelation(ref.Address)
				if source.RelationKind != catalog.TableRelation && source.RelationKind != catalog.ForeignRelation && source.RelationKind != catalog.ViewRelation && source.RelationKind != catalog.CompositeRelation {
					kind := "relations of this kind"
					if source.RelationKind == catalog.SequenceRelation {
						kind = "sequences"
					}
					if source.RelationKind == catalog.IndexRelation {
						kind = "indexes"
					}
					diagnostic := sqlError("42809", fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, source.Name())).(*sqlstate.Error)
					diagnostic.Detail = "This operation is not supported for " + kind + "."
					if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
						diagnostic.Position = strconv.Itoa(pos)
					}
					return b, diagnostic
				}
				var sourceType *catalog.Type
				if source.RelationKind == catalog.CompositeRelation {
					typeRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
					var ok bool
					sourceType, ok = auth.Catalog.Type(typeRef.Address)
					if !ok || sourceType.OwningRelation != source.Address() || sourceType.SQLType.ID() != source.RowType {
						return b, fmt.Errorf("stuntgres: invalid LIKE composite type provider")
					}
					if err := auth.CanUse(typeRef); err != nil {
						return b, err
					}
				} else if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
					return b, err
				}
				offset := len(columns)
				b.createLikes = append(b.createLikes, createLikeInput{ref: ref, source: source, sourceType: sourceType, offset: offset, options: tableLikeOptions(like.Options)})
				start := len(inherited)
				for _, constraint := range source.Constraints.NotNull {
					for i, attribute := range source.Attributes {
						if constraint.Attribute == attribute {
							inherited = append(inherited, createLikeConstraint{Element: elementIndex, NotNullConstraint: catalog.NotNullConstraint{Name: constraint.Name, Attribute: int32(offset + i + 1)}})
							break
						}
					}
				}
				// PostgreSQL copies via its relation/type/name constraint index.
				slices.SortFunc(inherited[start:], func(a, b createLikeConstraint) int {
					return strings.Compare(a.Name, b.Name)
				})
				for _, c := range source.Columns {
					columns = append(columns, catalog.Column{Name: c.Name, Usage: c.Usage, Nullable: c.Nullable, NullabilityKnown: c.NullabilityKnown})
					declarations = append(declarations, createColumnDeclaration{})
				}
			}
		}
		var mergeErr error
		columns, declarations, inherited, mergeErr = e.describeInheritance(op, auth, s, &b, columns, declarations, inherited)
		if mergeErr != nil {
			return b, mergeErr
		}
		if len(columns) > 1600 {
			return b, sqlError("54011", "tables can have at most 1600 columns")
		}
		b.createColumns = columns
		var err error
		b.createColumnIndex, err = bindCreateColumnIndex(columns)
		if err != nil {
			return b, err
		}
		b.createUniques, err = bindCreateUniques(s, b.createColumnIndex, b.createLikes, auth.Catalog)
		if err != nil {
			return b, err
		}
		b.createNotNull, err = bindCreateNotNull(s, columns, declarations, inherited, b.createUniques, b.createColumnIndex)
		if err != nil {
			return b, err
		}
		b.createDeclarations = declarations
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		var err error
		b, err = e.bindMutationCommand(binder, auth, requiredRef, stmt)
		if err != nil {
			return b, err
		}
	case *parser.DeclareCursor:
		var err error
		binder.queryAuth = &auth
		b.query, err = binder.bindSelect(s.Query, false)
		if err != nil {
			return b, err
		}
		// PostgreSQL checks the analyzed query's own rowmarks before rewrite;
		// rowmarks inside derived queries or stored views remain admissible.
		if s.Hold && len(s.Query.Locking) != 0 {
			err := sqlError("0A000", "DECLARE CURSOR WITH HOLD ... "+s.Query.Locking[0].Mode+" is not supported")
			err.(*sqlstate.Error).Detail = "Holdable cursors must be READ ONLY."
			return b, err
		}
		if s.Scroll == parser.CursorScrollEnabled && len(s.Query.Locking) != 0 {
			err := sqlError("0A000", "DECLARE SCROLL CURSOR ... "+s.Query.Locking[0].Mode+" is not supported")
			err.(*sqlstate.Error).Detail = "Scrollable cursors must be READ ONLY."
			return b, err
		}
		b.commandQuery = true
	case *parser.Select:
		var err error
		binder.queryAuth = &auth
		b.query, err = binder.bindSelect(s, false)
		if err != nil {
			return b, err
		}
		b.querySources = binder.querySources
		if view != nil {
			if err := bindViewOutput(b.query, view); err != nil {
				return b, err
			}
		}
	default:
		return b, sqlError("0A000", "unsupported statement")
	}
	b.ctes = binder.statementCTEs
	b.cteSelectBodies = binder.cteSelectBodies
	b.querySources = binder.querySources
	var err error
	b.parameters, err = binder.finish()
	if err != nil {
		return b, err
	}
	if cteRestriction != "" {
		return b, sqlError("0A000", cteRestriction)
	}
	if binder.cteScanOrdinal != 0 {
		queries := binder.cteExecutables
		if len(queries) == 0 && b.query != nil {
			queries = []*BoundQuery{b.query}
		}
		for _, query := range queries {
			activateCTESources(query.Root, b.querySources)
			query.Root, err = foldCTERestrictions(auth.Catalog, query.Root)
			if err != nil {
				return b, err
			}
		}
	}
	if materialized {
		if b.parameters.Count != 0 {
			return b, sqlError("0A000", "materialized views may not be defined using bound parameters")
		}
		for _, source := range binder.querySources {
			if ns := auth.Catalog.MustNamespace(source.relation.Namespace()); ns.Temporary != (ids.SessionToken{}) {
				return b, sqlError("0A000", "materialized views must not use temporary tables or views")
			}
		}
	}
	if len(b.querySources) != 0 && view == nil {
		if _, err := validateViewReadGraph(auth.Catalog, b.querySources); err != nil {
			return b, err
		}
	}
	// Virtual catalog views still undergo the existing semantic
	// analysis. Reject only after predicates, assignments, RETURNING and
	// parameters have bound, without ever requiring storage or evaluating writes.
	if b.relation != nil && b.relation.VirtualView {
		switch stmt.(type) {
		case *parser.Insert:
			return b, virtualViewWriteError(b.relation, "insert", b.targets)
		case *parser.Update:
			return b, virtualViewWriteError(b.relation, "update", b.targets)
		case *parser.Delete:
			return b, virtualViewWriteError(b.relation, "delete", nil)
		}
	}
	b.usedColumns, b.dependencies = binder.uses, binder.dependencies
	b.visibilitySnapshot = binder.visibilitySnapshot
	b.subqueryDemand = binder.subqueryDemand
	b.sequenceConstants = binder.sequenceConstants
	b.regclassConcat = binder.regclassConcat
	b.regtypeConstants = binder.regtypeConstants
	b.viewLiterals = binder.viewLiterals
	b.viewCastUsages = binder.viewCastUsages
	b.viewIntaggRefs = binder.viewIntaggRefs
	b.viewOperatorRefs = binder.viewOperatorRefs
	b.viewComposites = binder.viewComposites
	b.viewSimilarSubstrings = binder.viewSimilarSubstrings
	b.viewPosixSubstrings = binder.viewPosixSubstrings
	b.viewCollations = binder.viewCollations
	b.viewColumns, b.viewStars = binder.viewColumns, binder.viewStars
	b.viewNamespaces = binder.viewNamespaces
	slices.SortFunc(binder.temporals, func(a, b temporalConstant) int { return a.offset - b.offset })
	b.temporals = binder.temporals
	b.composites = binder.composites
	if rule == nil && !parser.ModifyingStatement(stmt) {
		if err := e.bindViewWrite(&b, auth, stmt); err != nil {
			return b, err
		}
	}
	for _, definition := range b.ctes {
		if definition.mutation == nil {
			continue
		}
		mutation := definition.mutation
		mutation.querySources = b.querySources
		mutation.parameters = b.parameters
		// Auxiliary commands share this binder; borrow its completed original
		// manifest, including bindings discovered after the auxiliary body.
		mutation.dependencies = b.dependencies
		if mutation.relation.VirtualView {
			switch definition.statement.(type) {
			case *parser.Insert:
				return b, virtualViewWriteError(mutation.relation, "insert", mutation.targets)
			case *parser.Update:
				return b, virtualViewWriteError(mutation.relation, "update", mutation.targets)
			case *parser.Delete:
				return b, virtualViewWriteError(mutation.relation, "delete", nil)
			}
		}
	}
	b.regclassInput = binder.input.Regclass
	b.regtypeInput = binder.input.Regtype
	b.regroleInput = binder.input.Regrole
	b.operatorInput = binder.input.Operator
	b.regprocInput = binder.input.Regproc
	b.regcollationInput, b.regnamespaceInput = binder.input.Regcollation, binder.input.Regnamespace
	b.regTSInput = binder.input.RegTS
	b.aclitemInput = binder.input.ACLItem
	b.ftsInput = binder.input.FTS
	b.typeDiagnosticName = binder.input.TypeDiagnosticName
	b.enumMemberSafe = binder.input.EnumMemberSafe
	return b, nil
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
	path, err := e.lookupNamespacesSnapshot(auth, name, relationLookup)
	if err != nil {
		if d, yes := err.(*sqlstate.Error); yes && utility {
			copy := *d
			copy.Position = ""
			err = &copy
		}
		return ids.ObjectRef{}, err
	}
	for i := range path.count() {
		a = auth.definitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: path.at(i), Name: relname})
		ok = a != (ids.ObjectAddress{})
		if ok {
			break
		}
	}
	if !ok {
		return ids.ObjectRef{}, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, objectNameText(name)))
	}
	descriptor, exists := auth.definitionDescriptor(a)
	d, valid := descriptor.(*catalog.Relation)
	if !exists || !valid {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	if auth.session != nil {
		namespace, _ := auth.definitionDescriptor(d.Namespace())
		if ns, ok := namespace.(*catalog.Namespace); ok && ns.Temporary == auth.session.token {
			auth.session.transactions.accessedTemporary = true
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

func insertValueError(view catalog.Read, err error, column catalog.Column, raw parser.Expr, sourceType *sqltype.Descriptor) error {
	sqlErr, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	copy := *sqlErr
	value := parser.Value{Pos: raw.SourcePosition()}
	if literal, ok := raw.(parser.LiteralExpr); ok {
		value = literal.Value
	}
	switch copy.Code {
	case "42804":
		if strings.HasPrefix(copy.Message, "column \"") {
			return &copy
		}
		var source string
		if sourceType != nil {
			source = sourceType.DiagnosticName()
		} else {
			source = literalTypeName(value)
			if literal, ok := raw.(parser.TypedLiteralExpr); ok {
				found := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: literal.TypeName.Name})
				if len(found) == 1 {
					if typ, ok := view.LookupType(sqltype.ID(found[0].ObjectID)); ok {
						source = typ.DiagnosticName()
					}
				}
			}
		}
		copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", column.Name, column.Usage.Type.DiagnosticName(), source)
		copy.Hint = "You will need to rewrite or cast the expression."
		copy.Position = strconv.Itoa(value.Pos.Character)
	}
	return &copy
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
type FrameImporter func(context.Context, ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error

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
