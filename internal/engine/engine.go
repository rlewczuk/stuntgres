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
	nextOID             OID
	nextRevision        DefinitionRevision
	sessionMu           sync.Mutex
	sessions            map[SessionToken]*Session
	settingsDefinitions map[string]SettingDefinition
	nextRow             uint64
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
	relation      *relationDescriptor
	storage       *relationStorage
	ref           ObjectRef
	targets       []int
	usedColumns   []int
	query         *BoundQuery
	dependencies  []expressionDependency
	parameters    ParameterInfo
	expressions   [][]BoundExpr
	createColumns []ColumnDescriptor
	createNotNull []notNullConstraint
	temporals     []temporalConstant
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
	case *parser.Comment, *parser.AlterTableNotNull, *parser.AlterTableDropColumn, *parser.LockTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization, *parser.Set, *parser.Reset:
		return StatementDescription{}, nil
	case *parser.IdentitySelect:
		e.mu.Lock()
		defer e.mu.Unlock()
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
	if err := scope.validateLockedRelation(e); err != nil {
		return StatementDescription{}, err
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return StatementDescription{}, err
	}
	b, err := e.describe(op.ctx, stmt, auth, scope.lockedRef, scope.input, op, nil)
	if err != nil {
		return StatementDescription{}, err
	}
	var results []ColumnDescriptor
	if b.query != nil {
		results = b.query.Root.Schema()
	}
	return StatementDescription{Parameters: b.parameters, Results: results, temporals: b.temporals}, nil
}
func objectNameText(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}

// requiredRef is the exact acquired relation for SELECT/INSERT. Name lookup
// still validates current namespace authority, but cannot retarget that lock.
// CREATE TABLE has no existing relation and supplies the zero reference.
func (e *Engine) describe(ctx context.Context, stmt parser.Statement, auth AuthorizationView, requiredRef ObjectRef, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) (binding, error) {
	b := binding{}
	binder := newExpressionBinder(ctx, e, input, op, expected)
	bind := func(v parser.Expr, c ColumnDescriptor, mode sqltype.CoercionContext) (BoundExpr, error) {
		return binder.bind(v, expressionPolicy{context: assignmentContext, expected: c.Usage, mode: mode})
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		var columns []ColumnDescriptor
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
				}
			}
		}
		b.createColumns = columns
		var err error
		b.createNotNull, err = bindCreateNotNull(s, columns, inherited)
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
		b.relation = e.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		if s.RestoreDefaultValues && (len(b.relation.columns) != 0 || len(s.Targets) != 0 || len(s.Rows) != 1 || len(s.Rows[0]) != 0) {
			return b, sqlError("0A000", "restore DEFAULT VALUES requires a zero-column table")
		}
		if len(s.Targets) == 0 {
			width := len(s.Rows[0])
			if width > len(b.relation.columns) {
				return b, insertError("42601", "INSERT has more expressions than target columns", s.Rows[0][len(b.relation.columns)].SourcePosition())
			}
			b.targets = make([]int, width)
			for i := range b.targets {
				b.targets[i] = i
			}
		} else {
			seen := make(map[int]bool, len(s.Targets))
			for _, target := range s.Targets {
				i, ok := b.relation.index[target.Name]
				if !ok {
					return b, insertError("42703", fmt.Sprintf("column \"%s\" of relation \"%s\" does not exist", target.Name, b.relation.name), target.Pos)
				}
				if seen[i] {
					return b, insertError("42701", fmt.Sprintf("column \"%s\" specified more than once", target.Name), target.Pos)
				}
				seen[i] = true
				b.targets = append(b.targets, i)
			}
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
				c := b.relation.columns[b.targets[j]]
				plan, err := bind(v, c, sqltype.Assignment)
				if err != nil {
					return b, insertValueError(err, c, v)
				}
				b.expressions[rowIndex][j] = plan
			}
		}
	case *parser.Select:
		var err error
		b.ref, err = e.resolveReference(auth, s.Name)
		if err != nil {
			return b, err
		}
		if b.ref != requiredRef {
			return b, ErrCatalogReplaced
		}
		b.relation = e.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		binder.relation, binder.ref = b.relation, b.ref
		width := len(s.Targets)
		if s.Star {
			width = len(b.relation.columns)
		}
		projection := make([]BoundExpr, width)
		results := make([]ColumnDescriptor, width)
		var root LogicalNode = &sourceNode{querySchema: b.relation.columns, ref: b.ref}
		for i := range width {
			if err := ctx.Err(); err != nil {
				return b, err
			}
			var expr BoundExpr
			if s.Star {
				expr = binder.columnAt(i, parser.Position{Offset: -1})
			} else {
				var er error
				expr, er = binder.bind(s.Targets[i], expressionPolicy{context: projectionContext})
				if er != nil {
					return b, er
				}
			}
			projection[i] = expr
		}
		if s.Where != nil {
			filter, bindErr := binder.bind(s.Where, expressionPolicy{context: filterContext, expected: binder.typ(sqltype.Bool).Usage(), mode: sqltype.Implicit})
			err = bindErr
			if err != nil {
				return b, err
			}
			root = &filterNode{querySchema: root.Schema(), child: root, predicate: filter}
		}
		if s.Order != nil {
			expr, bindErr := binder.column(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: s.Order.Column, Pos: s.Order.Pos}}}, Pos: s.Order.Pos})
			err = bindErr
			if err != nil {
				return b, err
			}
			order := expr.(columnExpression).slot
			ordering, bindErr := sqltype.BindComparison(b.relation.columns[order].Usage.Type, b.relation.columns[order].Usage.Type)
			err = bindErr
			if err != nil {
				return b, err
			}
			root = &sortNode{querySchema: root.Schema(), child: root, slot: order, comparison: ordering, desc: s.Order.Desc}
		}
		for i, expr := range projection {
			expr, err = binder.finalizeProjection(expr)
			if err != nil {
				return b, err
			}
			projection[i] = expr
			result := ColumnDescriptor{Name: "?column?", Usage: expr.ResultUsage()}
			if !s.Star {
				if _, ok := s.Targets[i].(parser.TypedLiteralExpr); ok {
					result.Name = expr.ResultUsage().Type.Name()
				}
			}
			if c, ok := expr.(columnExpression); ok {
				result = b.relation.columns[c.slot]
			}
			results[i] = result
		}
		b.query = &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}}
	default:
		return b, sqlError("0A000", "unsupported statement")
	}
	var err error
	b.parameters, err = binder.finish()
	if err != nil {
		return b, err
	}
	b.usedColumns, b.dependencies = binder.uses, binder.dependencies
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
	if len(name.Parts) == 1 {
		path, err := e.referencePath(auth)
		if err != nil {
			return ObjectRef{}, err
		}
		for _, ns := range path {
			a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: relname}]
			if ok {
				break
			}
		}
	} else {
		ns := view.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
		if comment && ns == (ObjectAddress{}) {
			return ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
		if ns != (ObjectAddress{}) {
			if err := auth.CanUse(ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: ns}); err != nil {
				if sqlErr, ok := err.(*sqlstate.Error); ok && !comment {
					copy := *sqlErr
					copy.Position = strconv.Itoa(name.Parts[0].Pos.Character)
					return ObjectRef{}, &copy
				}
				return ObjectRef{}, err
			}
		}
		a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: relname}]
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

func insertValueError(err error, column ColumnDescriptor, raw parser.Expr) error {
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
		source := literalTypeName(value)
		if literal, ok := raw.(parser.TypedLiteralExpr); ok {
			source = literal.TypeName.Name + " without time zone"
		}
		copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", column.Name, column.Usage.Type.DiagnosticName(), source)
		copy.Hint = "You will need to rewrite or cast the expression."
		copy.Position = strconv.Itoa(value.Pos.Character)
	}
	return &copy
}

func notNullError(schema, table, column string, row []sqltype.Value) error {
	detail, err := failingRowDetail(row)
	if err != nil {
		return err
	}
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
		if err != nil && result.Rows != nil {
			result.Rows.Close()
			result = Result{}
		}
	}()
	return scope.Execute(stmt, count, importFrame)
}
func (e *Engine) executeStatement(scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	op := scope.op
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	if !op.commandAdmission {
		op.snapshotNoticeThreshold()
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.Comment:
		result, err := e.executeComment(scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema:
		result, err := e.executeSchema(op, stmt, count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization, *parser.IdentitySelect:
		result, err := e.executeRole(op, stmt, count)
		if _, identity := stmt.(*parser.IdentitySelect); err == nil && !identity {
			scope.completed = true
		}
		return result, err
	case *parser.Set, *parser.Show, *parser.Reset:
		result, err := e.executeSetting(op, stmt, count, scope.implicit)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.SettingCall:
		result, err := e.executeSettingCall(op, stmt.(*parser.SettingCall), count, importFrame, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	}
	e.mu.Lock()
	if err := scope.validateLockedRelation(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if s, ok := stmt.(*parser.AlterTableDropColumn); ok {
		return e.alterDropColumnLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.AlterTableNotNull); ok {
		return e.alterNotNullLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.Insert); ok && s.RestoreDefaultValues && !op.restoreInsertAuthority {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "INSERT defaults are unsupported")
	}
	if s, ok := stmt.(*parser.DropTable); ok {
		return e.dropTableLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.CreateTable); ok {
		if count != 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		nsref, er := e.creationNamespace(auth, s.Name)
		if er != nil {
			e.mu.Unlock()
			return Result{}, creationError(er, s.Name)
		}
		if op.restoreTableDestination {
			bootstrap, er := auth.roles.Descriptor(auth.state.Authenticated)
			if er != nil || !bootstrap.Superuser || e.importTargets == nil {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
			}
		}
		if er = auth.CheckCreate(nsref); er != nil {
			denied, ok := er.(*sqlstate.Error)
			if !op.restoreTableDestination || !ok || denied.Code != "42501" {
				e.mu.Unlock()
				return Result{}, creationError(er, s.Name)
			}
		}
		ns := nsref.Address
		key := NameKey{ClassID: RelationClassID, Namespace: ns, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
		if _, exists := e.state.names[key]; exists {
			e.mu.Unlock()
			if s.IfNotExists {
				op.existingRelationNotice(key.Name)
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
		}
		base := CatalogRead{owner: e, state: e.state}
		stage := e.BeginCatalogStage(base)
		stage.authSession = op.session
		stage.authRevision = auth.revision
		stage.roleRevision = auth.roles.state.revision
		er = e.schemaTableLocked(op, auth, stage, s, ns, auth.state.CurrentUser)
		e.mu.Unlock()
		if er != nil {
			return Result{}, er
		}
		prepared, er := e.ValidateCatalogStage(ctx, stage)
		if er != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, er
		}
		_, er = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
		if er == nil {
			scope.completed = true
		}
		if _, conflict := er.(CatalogConflict); conflict {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if s.IfNotExists {
				e.mu.Lock()
				current := CatalogRead{owner: e, state: e.state}
				currentAuth, authErr := e.authorizationLocked(op)
				if authErr != nil {
					contextErr := ctx.Err()
					e.mu.Unlock()
					if contextErr != nil {
						return Result{}, contextErr
					}
					return Result{}, authErr
				}
				resolved, resolveErr := e.creationNamespace(currentAuth, s.Name)
				_, exists := e.state.names[key]
				same := resolveErr == nil && resolved.Address == ns && current.Epoch() == base.Epoch()
				var accessErr error
				if same && exists {
					accessErr = currentAuth.CheckCreate(resolved)
				}
				contextErr := ctx.Err()
				e.mu.Unlock()
				if contextErr != nil {
					return Result{}, contextErr
				}
				if resolveErr != nil {
					return Result{}, creationError(resolveErr, s.Name)
				}
				if accessErr != nil {
					return Result{}, creationError(accessErr, s.Name)
				}
				if same && exists {
					op.existingRelationNotice(key.Name)
					return Result{}, nil
				}
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
		}
		return Result{}, er
	}
	b, err := e.describe(ctx, stmt, auth, scope.lockedRef, scope.input, op, scope.Expected)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []ColumnDescriptor
		if b.query != nil {
			results = b.query.Root.Schema()
		}
		if err := checkStatementSignature(*scope.Expected, StatementDescription{Parameters: b.parameters, Results: results}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if count != b.parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, b.parameters.Count)
	}
	canonical := make([]sqltype.Value, count)
	if count != 0 {
		if importFrame == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err = importFrame(ctx, b.parameters, canonical, scope.input); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		for i, v := range canonical {
			if v.Type() != b.parameters.Types[i].Type {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	var materialized [][]sqltype.Value
	evaluation := EvalContext{Context: ctx, Parameters: canonical}
	_, inserting := stmt.(*parser.Insert)
	if inserting {
		// Fold immutable assignment casts before executor ACL checks, without
		// allocating protected rows for unauthorized valid input.
		for i, row := range b.expressions {
			for j, expression := range row {
				if _, converted := expression.(coercionExpression); converted && !expression.Properties().ParameterDependent {
					value, foldErr := expression.Eval(&evaluation)
					if foldErr != nil {
						e.mu.Unlock()
						return Result{}, insertValueError(foldErr, b.relation.columns[b.targets[j]], stmt.(*parser.Insert).Rows[i][j])
					}
					if materialized == nil {
						materialized = make([][]sqltype.Value, len(b.expressions))
					}
					if materialized[i] == nil {
						materialized[i] = make([]sqltype.Value, len(row))
					}
					materialized[i][j] = value
				}
			}
		}
	}
	if err = auth.checkBinding(b, inserting); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.Insert:
		rows := make([][]sqltype.Value, len(s.Rows))
		for i, source := range s.Rows {
			if err = ctx.Err(); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			row := make([]sqltype.Value, len(b.relation.columns))
			for j, c := range b.relation.columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			for j, v := range source {
				target := b.targets[j]
				c := b.relation.columns[target]
				if materialized != nil && materialized[i] != nil && materialized[i][j].Type() != nil {
					row[target] = materialized[i][j]
				} else {
					row[target], err = b.expressions[i][j].Eval(&evaluation)
				}
				if err != nil {
					e.mu.Unlock()
					return Result{}, insertValueError(err, c, v)
				}
			}
			rows[i] = row
		}
		// All supported expressions are literals or canonical parameters.
		// Constant assignment casts have already folded before ACL checks;
		// executor constraints must not mask their failures.
		for _, row := range rows {
			if err = ctx.Err(); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			for j, c := range b.relation.columns {
				if row[j].IsNull() && !c.Nullable {
					schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
					e.mu.Unlock()
					return Result{}, notNullError(schema, b.relation.name, c.Name, row)
				}
			}
		}
		err = ctx.Err()
		e.mu.Unlock()
		if err == nil {
			err = scope.Writes().Apply(b.ref, rows)
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(len(rows))}, nil
	case *parser.Select:
		view, viewErr := scope.acquireDataViewLocked(e, b.ref)
		if viewErr != nil {
			e.mu.Unlock()
			return Result{}, viewErr
		}
		source := captureQuerySource(view, b.ref)
		e.mu.Unlock()
		rows, count, queryErr := finalizeQuery(ctx, b.query, source, canonical)
		if queryErr != nil {
			return Result{}, queryErr
		}
		return Result{Count: int64(count), Rows: rows}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
}
