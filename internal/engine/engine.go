package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Engine struct {
	mu    sync.Mutex
	locks *relationLockManager
	// Implicit row work finishes before replacement; explicit work is epoch-fenced.
	replacementMu       replacementAdmission
	owner               *catalogOwner
	state               *catalogState
	providers           map[OID]CatalogProvider
	storage             map[ObjectAddress]*relationStorage
	sequences           map[ObjectAddress]*sequenceGenerator
	nextOID             OID
	nextRevision        DefinitionRevision
	sessionMu           sync.Mutex
	sessions            map[SessionToken]*Session
	settingsDefinitions map[string]SettingDefinition
	nextRow             uint64
	nextVersion         uint64
	commitSeq           CommitSeq
	roles               *roleCatalog
	nextPrincipal       uint64
	importTargets       map[PrincipalRef]PrincipalRef
	roleReferences      map[string][]PrincipalRef
	settingsOrder       []string
}
type ParameterInfo struct {
	Count int
	Types []sqltype.Usage
}
type Result struct {
	Count int64
	Rows  RowResult
}
type binding struct {
	relation          *relationDescriptor
	storage           *relationStorage
	ref               ObjectRef
	targets           []int
	usedColumns       []int
	query             *BoundQuery
	querySources      []queryLeaf
	viewLiterals      map[viewLiteralPath]literalExpression
	viewColumns       map[viewLiteralPath]int
	viewStars         map[int][]int
	viewNamespaces    []viewNamespace
	commandQuery      bool
	update            *boundUpdate
	dependencies      []expressionDependency
	parameters        ParameterInfo
	expressions       [][]BoundExpr
	createColumns     []ColumnDescriptor
	createNotNull     []notNullConstraint
	createDefaults    map[int32]columnDefault
	defaults          []BoundExpr
	temporals         []temporalConstant
	predicate         BoundExpr
	sequenceConstants []sequenceConstant
}

func New() *Engine {
	e, err := newEngine(nil, nil)
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...CatalogProvider) (*Engine, error) {
	return newEngine(providers, nil)
}

// NewWithSettings consumes definitions validated before driver publication.
func NewWithSettings(definitions []SettingDefinition) *Engine {
	e, err := newEngine(nil, definitions)
	if err != nil {
		panic(err)
	}
	return e
}

func newEngine(providers []CatalogProvider, definitions []SettingDefinition) (*Engine, error) {
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
	e.state = &catalogState{epoch: 1, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}}
	for _, ns := range []struct {
		oid  OID
		name string
	}{{PGCatalogNamespaceOID, "pg_catalog"}, {PublicNamespaceOID, "public"}} {
		rights := UsagePrivilege
		if ns.oid == PublicNamespaceOID {
			rights |= CreatePrivilege
		}
		bootstrap := e.roles.names["postgres"]
		d := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: ns.oid}, name: ns.name, revision: 1, owner: bootstrap,
			acl: ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: bootstrap, Grantee: ACLGrantee{Public: true}, Privileges: rights}, {Grantor: bootstrap, Grantee: ACLGrantee{Role: bootstrap}, Privileges: UsagePrivilege | CreatePrivilege, GrantOptions: UsagePrivilege | CreatePrivilege}}}}
		e.state.descriptors[d.address] = d
		e.state.names[NameKey{ClassID: NamespaceClassID, Name: d.name}] = d.address
	}
	for _, typ := range sqltype.Builtins() {
		d := &typeDescriptor{typ: typ}
		e.state.descriptors[d.Address()] = d
		e.state.names[NameKey{ClassID: TypeClassID, Namespace: d.Namespace(), Name: d.Name()}] = d.Address()
	}
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
		usedRule := false
		for _, descriptor := range e.state.descriptors {
			if relation, ok := descriptor.(*relationDescriptor); ok && relation.view != nil && relation.view.rule == n {
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
	if err := e.validateOperation(op); err != nil {
		return StatementDescription{}, err
	}
	switch s := stmt.(type) {
	case *parser.Comment, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableNotNull, *parser.AlterTableDropColumn, *parser.LockTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization, *parser.Set, *parser.Reset:
		return StatementDescription{}, nil
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return StatementDescription{}, nil
	case *parser.IdentitySelect:
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := e.validateIdentityCall(op, s); err != nil {
			return StatementDescription{}, err
		}
		typ, _ := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Name)
		columns := make([]ColumnDescriptor, len(s.Names))
		for i, name := range s.Names {
			columns[i] = ColumnDescriptor{Name: name, Usage: typ.Usage(), Nullable: name == "current_schema", NullabilityKnown: name == "current_schema"}
		}
		return StatementDescription{Results: columns}, nil
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
		return StatementDescription{Results: settingColumns(typ, d.Name)}, nil
	case *parser.SettingCall:
		e.mu.Lock()
		defer e.mu.Unlock()
		info, _, err := e.describeSettingCall(op.ctx, s, scope.input, op)
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
	b, err := e.describe(op.ctx, stmt, auth, scope.lockedRef, scope.input, op, nil)
	if err != nil {
		return StatementDescription{}, err
	}
	if err := scope.validateQuerySources(b.querySources); err != nil {
		return StatementDescription{}, err
	}
	var results []ColumnDescriptor
	if b.query != nil && !b.commandQuery {
		results = b.query.Root.Schema()
	}
	analysis, err := e.sequenceAnalysisLocked(auth, b.sequenceConstants)
	if err != nil {
		return StatementDescription{}, err
	}
	var cell *sequenceAnalysis
	if len(analysis.constants) != 0 {
		cell = &analysis
	}
	if _, view := stmt.(*parser.CreateView); view {
		e.mu.Unlock()
		err = scope.acquireViewDefinitionReferences(b)
		e.mu.Lock()
		if err != nil {
			return StatementDescription{}, err
		}
	}
	return StatementDescription{Parameters: b.parameters, Results: results, temporals: b.temporals, sequences: cell}, nil
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
func (e *Engine) describe(ctx context.Context, stmt parser.Statement, auth AuthorizationView, requiredRef ObjectRef, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) (binding, error) {
	b := binding{}
	binder := newExpressionBinder(ctx, e, input, op, expected)
	var view *parser.CreateView
	if s, ok := stmt.(*parser.CreateView); ok {
		view = s
		stmt = s.Query
		binder.noParameters = true
		b.commandQuery = true
	}
	bind := func(v parser.Expr, c ColumnDescriptor, mode sqltype.CoercionContext) (BoundExpr, error) {
		return binder.bind(v, expressionPolicy{context: assignmentContext, expected: c.Usage, mode: mode, column: c.Name})
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		var columns []ColumnDescriptor
		declarations := make([]createColumnDeclaration, 0, len(s.Elements))
		table := s.Name.Parts[len(s.Name.Parts)-1].Text
		var inherited []createLikeConstraint
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
			if kinds != 1 {
				return b, fmt.Errorf("stuntgres: invalid table element")
			}
			if element.NotNull != nil {
				continue
			}
			if c := element.Column; c != nil {
				u, err := e.resolveType(c.TypeName, op)
				if err != nil {
					return b, err
				}
				if u.Type.ID() == sqltype.Regclass {
					return b, typeDiagnostic(c.TypeName, "0A000", "unsupported column type")
				}
				declaration, err := bindCreateColumnDeclaration(c, table)
				if err != nil {
					return b, err
				}
				declarations = append(declarations, declaration)
				columns = append(columns, ColumnDescriptor{Name: c.Name, Usage: u, Nullable: true, NullabilityKnown: true})
			} else {
				like := element.Like
				if len(like.Options) != 0 {
					if like.Options[0].Pos.Character == 0 {
						return b, sqlError("0A000", "LIKE options are unsupported")
					}
					return b, insertError("0A000", "LIKE options are unsupported", like.Options[0].Pos)
				}
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
				if err := auth.Check(AccessRequirement{Object: ref, Privileges: SelectPrivilege}); err != nil {
					return b, err
				}
				source := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
				if err := tableKind(source, "like"); err != nil {
					diagnostic := err.(*sqlstate.Error)
					if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
						diagnostic.Position = strconv.Itoa(pos)
					}
					return b, diagnostic
				}
				offset := len(columns)
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
		b.createColumns = columns
		var err error
		b.createNotNull, err = bindCreateNotNull(s, columns, declarations, inherited)
		if err != nil {
			return b, err
		}
		b.createDefaults, err = bindCreateDefaults(binder, columns, declarations)
		if err != nil {
			return b, err
		}
	case *parser.Insert:
		var err error
		b.ref, err = e.resolveReference(auth, s.Name)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ErrCatalogReplaced
		}
		b.relation = auth.catalog.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil && b.relation.relationKind == tableRelation {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		if len(s.Targets) == 0 && s.Source == nil {
			width := len(s.Rows[0])
			if width > len(b.relation.columns) {
				return b, insertError("42601", "INSERT has more expressions than target columns", s.Rows[0][len(b.relation.columns)].SourcePosition())
			}
			b.targets = make([]int, width)
			for i := range b.targets {
				b.targets[i] = i
			}
		} else {
			b.targets = make([]int, len(s.Targets))
			if err := bindInsertTargets(b.relation, s.Targets, b.targets); err != nil {
				return b, err
			}
		}
		if len(b.relation.defaults) != 0 {
			b.defaults = make([]BoundExpr, len(b.relation.columns))
			for i, attribute := range b.relation.attributes {
				b.defaults[i] = b.relation.defaults[attribute].expression
			}
		}
		if s.Source != nil {
			if err := e.bindInsertSource(&b, binder, auth, s); err != nil {
				return b, err
			}
			break
		}
		b.expressions = make([][]BoundExpr, len(s.Rows))
		for rowIndex, row := range s.Rows {
			if rowIndex > 0 && len(row) != len(s.Rows[0]) {
				return b, insertError("42601", "VALUES lists must all be the same length", row[0].SourcePosition())
			}
			if len(row) > len(b.targets) {
				return b, insertError("42601", "INSERT has more expressions than target columns", row[len(b.targets)].SourcePosition())
			}
			if rowIndex == 0 && len(s.Targets) != 0 && len(row) < len(b.targets) {
				return b, insertError("42601", "INSERT has more target columns than expressions", s.Targets[len(row)].Pos)
			}
			b.expressions[rowIndex] = make([]BoundExpr, len(row))
			for j, v := range row {
				if _, requested := v.(parser.DefaultExpr); requested {
					continue
				}
				c := b.relation.columns[b.targets[j]]
				plan, err := bind(v, c, sqltype.Assignment)
				if err != nil {
					var source *sqltype.Descriptor
					if plan != nil {
						source = plan.ResultUsage().Type
					}
					return b, insertValueError(auth.catalog, err, c, v, source)
				}
				b.expressions[rowIndex][j] = plan
			}
		}
		// Retain a targeted default only if at least one row requests it.
		// Explicit NULL is a bound expression, not a DEFAULT marker.
		if len(b.defaults) != 0 {
			remaining := len(b.relation.defaults)
			for j, target := range b.targets {
				if b.defaults[target] == nil {
					continue
				}
				consumed := false
				for _, row := range b.expressions {
					if j >= len(row) || row[j] == nil {
						consumed = true
						break
					}
				}
				if !consumed {
					b.defaults[target] = nil
					remaining--
				}
			}
			if remaining == 0 {
				b.defaults = nil
			}
		}
	case *parser.Delete:
		var err error
		b.ref, err = e.resolveReference(auth, s.Name)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ErrCatalogReplaced
		}
		b.relation = auth.catalog.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil && b.relation.relationKind == tableRelation {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		binder.relation, binder.ref = b.relation, b.ref
		binder.deleteTarget, binder.targetName, binder.targetAlias = true, b.relation.name, s.Alias.Text
		if s.Where != nil {
			b.predicate, err = binder.bind(s.Where, expressionPolicy{context: filterContext})
			if err != nil {
				return b, err
			}
		}
	case *parser.Update:
		var err error
		b.ref, err = e.resolveReference(auth, s.Name)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ErrCatalogReplaced
		}
		b.relation = e.state.descriptors[b.ref.Address].(*relationDescriptor)
		if b.relation.relationKind == viewRelation || b.relation.relationKind == catalogRelation {
			return b, tableKind(b.relation, "change")
		}
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		if err = e.bindUpdate(&b, binder, s); err != nil {
			return b, err
		}
	case *parser.Select:
		var err error
		if s.From == nil && !s.IsSingleton() {
			return b, sqlError("0A000", "SELECT without FROM is unsupported")
		}
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
	var err error
	b.parameters, err = binder.finish()
	if err != nil {
		return b, err
	}
	b.usedColumns, b.dependencies = binder.uses, binder.dependencies
	b.sequenceConstants = binder.sequenceConstants
	b.viewLiterals = binder.viewLiterals
	b.viewColumns, b.viewStars = binder.viewColumns, binder.viewStars
	b.viewNamespaces = binder.viewNamespaces
	slices.SortFunc(binder.temporals, func(a, b temporalConstant) int { return a.offset - b.offset })
	b.temporals = binder.temporals
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
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42P01" && len(name.Parts) > 0 {
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

// COMMENT utility lookup checks a qualified namespace before the relation and
// reports namespace denial without the position attached by DML analysis.
func (e *Engine) resolveRelationReference(auth AuthorizationView, name parser.ObjectName, comment bool) (ObjectRef, error) {
	view := auth.catalog
	if err := e.validateCatalogViewLocked(view); err != nil {
		return ObjectRef{}, err
	}
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	relname := name.Parts[len(name.Parts)-1].Text
	var a ObjectAddress
	var ok bool
	if comment && len(name.Parts) == 2 {
		ns := view.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
		if name.Parts[0].Text == "pg_temp" {
			ns = temporaryNamespace(auth)
		}
		if ns == (ObjectAddress{}) {
			return ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
	}
	path, err := e.lookupNamespaces(auth, name, relationLookup)
	if err != nil {
		if d, yes := err.(*sqlstate.Error); yes && comment {
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

func notNullError(schema, table, column string, row []sqltype.Value, output sqltype.OutputEnvironment) error {
	detail, err := failingRowDetail(row, output, nil, nil)
	if err != nil {
		return err
	}
	return notNullDetailError(schema, table, column, detail)
}

func notNullDetailError(schema, table, column, detail string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: fmt.Sprintf("null value in column \"%s\" of relation \"%s\" violates not-null constraint", column, table),
		Schema:  schema, Table: table, Column: column, Detail: detail,
	}
}

// FrameImporter runs only codec conversion of already owned adapter values.
// It must not invoke user code or external I/O under the engine mutex.
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
