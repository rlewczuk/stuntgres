package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
)

type Engine struct {
	mu    sync.Mutex
	locks *relationLockManager
	// Implicit row work finishes before replacement; explicit work is epoch-fenced.
	replacementMu       replacementAdmission
	owner               *catalogOwner
	state               *catalogState
	providers           map[OID]CatalogProvider
	extensions          map[string]registeredExtension
	databaseName        string
	databaseNameError   error
	storage             map[ObjectAddress]*relationStorage
	sequences           map[ObjectAddress]*sequenceGenerator
	indexRoots          map[ObjectAddress]*logicalIndexRoot
	nextOID             OID
	nextRevision        DefinitionRevision
	nextEnumMember      int64
	sessionMu           sync.Mutex
	sessions            map[SessionToken]*Session
	settingsDefinitions map[string]SettingDefinition
	nextRow             uint64
	nextVersion         uint64
	commitSeq           CommitSeq
	diagnosticTx        uint32
	visibilityXID       uint64
	visibility          map[TxRef]transactionVisibility
	indexBuildWaits     map[ObjectAddress]indexBuildWait
	exclusionPending    map[*relationTicket]*exclusionProjection
	exclusionGeneration uint64
	serializable        serializableTracker
	prepared            map[string]*preparedTransaction
	authorizationDomain authorization.Domain
	aclAuthority        sqltype.ACLAuthority
	roles               *roleCatalog
	nextPrincipal       uint64
	importTargets       map[authorization.PrincipalRef]authorization.PrincipalRef
	roleReferences      map[string][]authorization.PrincipalRef
	settingsOrder       []string
}
type ParameterInfo struct {
	Count int
	Types []sqltype.Usage
}
type Result struct {
	CommandTag string
	Count      int64
	Rows       RowResult
	created    ObjectRef // Exact ordinary CREATE TABLE outcome; zero for a no-op.
}
type binding struct {
	relation              *relationDescriptor
	storage               *relationStorage
	ref                   ObjectRef
	viewWrite             *viewWrite
	targets               []int
	arrayTargets          []*arrayAssignmentTarget
	usedColumns           []int
	query                 *BoundQuery
	ctes                  []*cteDefinition
	cteSelectBodies       bool
	querySources          []queryLeaf
	visibilitySnapshot    bool
	subqueryDemand        bool
	viewLiterals          map[viewLiteralPath]literalExpression
	viewCastUsages        map[viewLiteralPath]sqltype.Usage
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
	dependencies          []expressionDependency
	parameters            ParameterInfo
	expressions           [][]BoundExpr
	insertRows            [][]parser.Expr
	createColumns         []ColumnDescriptor
	createNotNull         []notNullConstraint
	createColumnIndex     map[string]int
	createUniques         []createUniqueConstraint
	createDeclarations    []createColumnDeclaration
	createLikes           []createLikeInput
	createOfType          *typeDescriptor
	createInheritance     []createInheritanceInput
	createLocalColumns    map[string]bool
	defaults              []BoundExpr
	identityDiscard       []bool
	insertProjection      []int
	conflict              *boundInsertConflict
	temporals             []temporalConstant
	composites            map[preparedCompositeKey]compositeOccurrence
	predicate             BoundExpr
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
	typeDiagnosticName    func(sqltype.ID) string
	enumMemberSafe        func(sqltype.ID, int64) bool
}

func New() *Engine {
	e, err := newEngine(nil, nil, "")
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...CatalogProvider) (*Engine, error) {
	return newEngine(providers, nil, "")
}

// NewWithSettings consumes definitions validated before driver publication.
func NewWithSettings(definitions []SettingDefinition, database string) *Engine {
	e, err := newEngine(nil, definitions, database)
	if err != nil {
		panic(err)
	}
	return e
}

func newEngine(providers []CatalogProvider, definitions []SettingDefinition, database string) (*Engine, error) {
	// Builtin descriptors use revision 1; allocations start beyond that frontier.
	e := &Engine{owner: &catalogOwner{}, providers: map[OID]CatalogProvider{}, storage: map[ObjectAddress]*relationStorage{}, nextOID: 16384, nextRevision: 2}
	e.locks = newRelationLockManager(e)
	e.sequences = make(map[ObjectAddress]*sequenceGenerator)
	e.initializeRoles()
	e.initializeSettings(definitions)
	for _, p := range providers {
		if err := e.RegisterCatalogProvider(p); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(namespaceProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(relationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(typeProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(collationProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(conversionProvider{}); err != nil {
		return nil, err
	}
	for _, class := range []OID{ExtensionClassID, ProcedureClassID, TSTemplateClassID, TSDictionaryClassID} {
		if err := e.RegisterCatalogProvider(extensionCatalogProvider{class}); err != nil {
			return nil, err
		}
	}
	if err := e.RegisterCatalogProvider(databaseProvider{}); err != nil {
		return nil, err
	}
	if err := e.RegisterCatalogProvider(operatorProvider{}); err != nil {
		return nil, err
	}
	comment := "text search dictionary that removes accents"
	if err := e.registerExtension("unaccent", registeredExtension{version: "1.1", trusted: true, superuser: true, relocatable: true, comment: &comment, provider: stockUnaccent}); err != nil {
		return nil, err
	}
	comment = "functions, operators, and index support for 1-D arrays of integers"
	if err := e.registerExtension("intarray", registeredExtension{version: "1.5", trusted: true, superuser: true, relocatable: true, comment: &comment, provider: stockIntarray}); err != nil {
		return nil, err
	}
	comment = "determine similarities and distance between strings"
	if err := e.registerExtension("fuzzystrmatch", registeredExtension{version: "1.2", trusted: true, superuser: true, relocatable: true, comment: &comment, provider: stockFuzzystrmatch}); err != nil {
		return nil, err
	}
	comment = "cryptographic functions"
	if err := e.registerExtension("pgcrypto", registeredExtension{version: "1.4", trusted: true, superuser: true, relocatable: true, comment: &comment, provider: stockPgcrypto}); err != nil {
		return nil, err
	}
	e.state = &catalogState{epoch: 1, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}}
	for _, ns := range []struct {
		oid  OID
		name string
	}{{PGCatalogNamespaceOID, "pg_catalog"}, {PGToastNamespaceOID, "pg_toast"}, {PublicNamespaceOID, "public"}, {13229, "information_schema"}} {
		owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		if ns.oid == PublicNamespaceOID {
			owner = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
		}
		d := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: ns.oid}, name: ns.name, revision: 1, owner: owner}
		if ns.oid != PGToastNamespaceOID {
			d.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
			}}
		}
		d.comment = namespaceCommentBaseline(ns.oid)
		e.state.descriptors[d.address] = d
		e.state.names[NameKey{ClassID: NamespaceClassID, Name: d.name}] = d.address
	}
	for _, typ := range sqltype.Builtins() {
		d := &typeDescriptor{typ: typ, comment: builtinTypeComment(typ.ID()), array: typ.AssociatedArray()}
		if typ.IsDomain() && typ.Concrete() {
			d.revision = 1
		}
		if typ.NamespaceName() == "information_schema" {
			d.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: 13229}
		}
		e.state.descriptors[d.Address()] = d
		e.state.names[descriptorNameKey(d)] = d.Address()
	}
	for i := range builtinCollations {
		d := new(collationDescriptor)
		*d = builtinCollations[i]
		d.owner = e.authorizationDomain.Principal(10)
		e.state.descriptors[d.Address()] = d
		e.state.names[descriptorNameKey(d)] = d.Address()
	}
	e.initializeConversionRoutines()
	for i := range builtinConversions {
		d := new(conversionDescriptor)
		*d = builtinConversions[i]
		d.owner = e.authorizationDomain.Principal(10)
		d.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
		d.revision = 1
		e.state.descriptors[d.Address()] = d
		e.state.names[descriptorNameKey(d)] = d.Address()
	}
	e.initializeOperators()
	e.initializeDatabase(database)
	if err := e.initializeCatalogSources(); err != nil {
		return nil, err
	}
	view := CatalogRead{owner: e, state: e.state}
	for _, d := range e.state.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil || p.Validate(view, d) != nil {
			return nil, fmt.Errorf("stuntgres: invalid built-in catalog descriptor")
		}
	}
	return e, nil
}
func (e *Engine) CatalogRead() CatalogRead {
	e.mu.Lock()
	defer e.mu.Unlock()
	return CatalogRead{owner: e, state: e.state}
}
func (e *Engine) allocateOIDLocked() (OID, error) {
	for e.nextOID != 0 {
		n := e.nextOID
		if n == ^OID(0) {
			e.nextOID = 0
		} else {
			e.nextOID++
		}
		if n == NamespaceClassID || n == RelationClassID || n == PGCatalogNamespaceOID || n == PublicNamespaceOID {
			continue
		}
		if _, ok := e.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: n}]; ok {
			continue
		}
		if _, ok := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: n}]; ok {
			continue
		}
		if _, ok := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: n}]; ok {
			continue
		}
		usedRule := false
		for _, descriptor := range e.state.descriptors {
			if definitionOIDUsed(descriptor, n) {
				usedRule = true
				break
			}
		}
		if usedRule {
			continue
		}
		return n, nil
	}
	return 0, fmt.Errorf("stuntgres: catalog object identity exhausted")
}
func (e *Engine) revisionLocked() (DefinitionRevision, error) {
	if e.nextRevision == DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: catalog definition revision exhausted")
	}
	r := e.nextRevision
	e.nextRevision++
	return r, nil
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
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return StatementDescription{}, nil
	case *parser.CreateComposite, *parser.AlterComposite, *parser.DropComposite, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateEnum, *parser.AlterEnum, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return StatementDescription{}, nil
	case *parser.CreateExtension:
		return StatementDescription{}, nil
	case *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		return StatementDescription{}, nil
	case *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableDefault, *parser.AlterTable, *parser.LockTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization, *parser.Set, *parser.Reset:
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
		typ, _ := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
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
		info, _, err := e.describeSettingCall(op.ctx, s, &scope.input, op)
		typ, _ := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
		columns := settingColumns(typ, s.Name)
		columns[0].Nullable = true
		return StatementDescription{Parameters: info, Results: columns}, err
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
	var results []ColumnDescriptor
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
	return StatementDescription{Parameters: b.parameters, Results: results, catalogOwner: auth.catalog.owner, catalogEpoch: auth.catalog.Epoch(), temporals: b.temporals, composites: b.composites, sequences: cell, regtypes: regtypes, analysis: prepared, planOwner: op.session.token, planGeneration: op.session.discardPlansGeneration}, nil
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
func (e *Engine) describe(ctx context.Context, stmt parser.Statement, auth AuthorizationView, requiredRef ObjectRef, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription, admission *queryLockAdmission, initial ...[]sqltype.Usage) (binding, error) {
	b := binding{}
	binder := newExpressionBinder(ctx, e, input, op, expected)
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
		var columns []ColumnDescriptor
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
				columns = append(columns, ColumnDescriptor{Name: c.Name, Usage: u, DeclaredDimensions: int16(len(typeName.ArrayBounds)), Nullable: true, NullabilityKnown: true})
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
				source := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
				if source.relationKind != tableRelation && source.relationKind != viewRelation && source.relationKind != compositeRelation {
					kind := "relations of this kind"
					if source.relationKind == sequenceRelation {
						kind = "sequences"
					}
					if source.relationKind == indexRelation {
						kind = "indexes"
					}
					diagnostic := sqlError("42809", fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, source.name)).(*sqlstate.Error)
					diagnostic.Detail = "This operation is not supported for " + kind + "."
					if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
						diagnostic.Position = strconv.Itoa(pos)
					}
					return b, diagnostic
				}
				var sourceType *typeDescriptor
				if source.relationKind == compositeRelation {
					typeRef := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.rowType)}}
					var ok bool
					sourceType, ok = auth.catalog.state.descriptors[typeRef.Address].(*typeDescriptor)
					if !ok || sourceType.owningRelation != source.address || sourceType.typ.ID() != source.rowType {
						return b, fmt.Errorf("stuntgres: invalid LIKE composite type provider")
					}
					if err := auth.CanUse(typeRef); err != nil {
						return b, err
					}
				} else if err := auth.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
					return b, err
				}
				offset := len(columns)
				b.createLikes = append(b.createLikes, createLikeInput{ref: ref, source: source, sourceType: sourceType, offset: offset, options: tableLikeOptions(like.Options)})
				start := len(inherited)
				for _, constraint := range source.notNullConstraints {
					for i, attribute := range source.attributes {
						if constraint.Attribute == attribute {
							inherited = append(inherited, createLikeConstraint{Element: elementIndex, notNullConstraint: notNullConstraint{Name: constraint.Name, Attribute: int32(offset + i + 1)}})
							break
						}
					}
				}
				// PostgreSQL copies via its relation/type/name constraint index.
				slices.SortFunc(inherited[start:], func(a, b createLikeConstraint) int {
					return strings.Compare(a.Name, b.Name)
				})
				for _, c := range source.columns {
					columns = append(columns, ColumnDescriptor{Name: c.Name, Usage: c.Usage, Nullable: c.Nullable, NullabilityKnown: c.NullabilityKnown})
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
		b.createUniques, err = bindCreateUniques(s, b.createColumnIndex, b.createLikes, auth.catalog)
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
			query.Root, err = foldCTERestrictions(auth.catalog, query.Root)
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
			if ns := auth.catalog.state.descriptors[source.relation.namespace].(*namespaceDescriptor); ns.temporary != (SessionToken{}) {
				return b, sqlError("0A000", "materialized views must not use temporary tables or views")
			}
		}
	}
	if len(b.querySources) != 0 && view == nil {
		if _, err := validateViewReadGraph(auth.catalog, b.querySources); err != nil {
			return b, err
		}
	}
	// Virtual catalog views still undergo the existing semantic
	// analysis. Reject only after predicates, assignments, RETURNING and
	// parameters have bound, without ever requiring storage or evaluating writes.
	if b.relation != nil && b.relation.virtualView {
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
	b.viewComposites = binder.viewComposites
	b.viewSimilarSubstrings = binder.viewSimilarSubstrings
	b.viewPosixSubstrings = binder.viewPosixSubstrings
	b.viewCollations = binder.viewCollations
	b.viewColumns, b.viewStars = binder.viewColumns, binder.viewStars
	b.viewNamespaces = binder.viewNamespaces
	slices.SortFunc(binder.temporals, func(a, b temporalConstant) int { return a.offset - b.offset })
	b.temporals = binder.temporals
	b.composites = binder.composites
	if !parser.ModifyingStatement(stmt) {
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
		if mutation.relation.virtualView {
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
	b.typeDiagnosticName = binder.input.TypeDiagnosticName
	b.enumMemberSafe = binder.input.EnumMemberSafe
	return b, nil
}

// Resolution helpers run under e.mu, using the supplied coherent view.
func (e *Engine) validateCatalogViewLocked(view CatalogRead) error {
	if e.owner == nil || view.owner != e || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if view.Epoch() != e.state.epoch {
		return ErrCatalogReplaced
	}
	return nil
}

func (e *Engine) resolveReference(auth AuthorizationView, name parser.ObjectName) (ObjectRef, error) {
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
func (e *Engine) resolveRelationReference(auth AuthorizationView, name parser.ObjectName, utility bool) (ObjectRef, error) {
	if err := e.validateCatalogViewLocked(auth.catalog); err != nil {
		return ObjectRef{}, err
	}
	return e.resolveRelationSnapshot(auth, name, utility)
}

func (e *Engine) resolveRelationSnapshot(auth AuthorizationView, name parser.ObjectName, utility bool) (ObjectRef, error) {
	view := auth.catalog
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	relname := name.Parts[len(name.Parts)-1].Text
	var a ObjectAddress
	var ok bool
	if utility && len(name.Parts) == 2 {
		ns := view.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
		if name.Parts[0].Text == "pg_temp" {
			ns = temporaryNamespace(auth)
		}
		if ns == (ObjectAddress{}) {
			return ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
	}
	path, err := e.lookupNamespacesSnapshot(auth, name, relationLookup)
	if err != nil {
		if d, yes := err.(*sqlstate.Error); yes && utility {
			copy := *d
			copy.Position = ""
			err = &copy
		}
		return ObjectRef{}, err
	}
	for i := range path.count() {
		a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: path.at(i), Name: relname}]
		if ok {
			break
		}
	}
	if !ok {
		return ObjectRef{}, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, objectNameText(name)))
	}
	if _, ok := view.state.descriptors[a].(*relationDescriptor); !ok {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	if d := view.state.descriptors[a].(*relationDescriptor); auth.session != nil {
		if ns, ok := view.state.descriptors[d.namespace].(*namespaceDescriptor); ok && ns.temporary == auth.session.token {
			auth.session.transactions.accessedTemporary = true
		}
	}
	return ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: a}, nil
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

func insertValueError(view CatalogRead, err error, column ColumnDescriptor, raw parser.Expr, sourceType *sqltype.Descriptor) error {
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
				found := view.Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, Name: literal.TypeName.Name})
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
