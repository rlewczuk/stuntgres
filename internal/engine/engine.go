package engine

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type Engine struct {
	mu                  sync.Mutex
	owner               *catalogOwner
	state               *catalogState
	providers           map[OID]CatalogProvider
	storage             map[ObjectAddress]*relationStorage
	nextOID             OID
	nextRevision        DefinitionRevision
	sessionMu           sync.Mutex
	sessions            map[SessionToken]*Session
	settingsDefinitions map[string]SettingDefinition
}
type ParameterInfo struct {
	Count int
	Types []parser.Type
}
type Result struct {
	Count   int64
	Columns []parser.Column
	Rows    *Snapshot
}
type binding struct {
	relation   *relationDescriptor
	storage    *relationStorage
	ref        ObjectRef
	targets    []int
	projection []int
	predicate  int
	order      int
	parameters ParameterInfo
}

func New() *Engine {
	e, err := newEngine(nil)
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...CatalogProvider) (*Engine, error) {
	return newEngine(providers)
}

func newEngine(providers []CatalogProvider) (*Engine, error) {
	e := &Engine{owner: &catalogOwner{}, providers: map[OID]CatalogProvider{}, storage: map[ObjectAddress]*relationStorage{}, nextOID: 16384, nextRevision: 1}
	e.settingsDefinitions = map[string]SettingDefinition{applicationName.Name: applicationName}
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
	e.state = &catalogState{epoch: 1, descriptors: map[ObjectAddress]Descriptor{}, names: map[NameKey]ObjectAddress{}}
	for _, ns := range []struct {
		oid  OID
		name string
	}{{PGCatalogNamespaceOID, "pg_catalog"}, {PublicNamespaceOID, "public"}} {
		d := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: ns.oid}, name: ns.name, revision: 1}
		e.state.descriptors[d.address] = d
		e.state.names[NameKey{ClassID: NamespaceClassID, Name: d.name}] = d.address
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
		if _, ok := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: n}]; !ok {
			return n, nil
		}
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
func (e *Engine) Describe(op *Operation, stmt parser.Statement) (ParameterInfo, error) {
	if err := e.validateOperation(op); err != nil {
		return ParameterInfo{}, err
	}
	switch s := stmt.(type) {
	case *parser.Set:
		return ParameterInfo{}, nil
	case *parser.Show:
		_, err := e.setting(s.Name.Text)
		return ParameterInfo{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := e.describe(stmt)
	if err != nil {
		return ParameterInfo{}, err
	}
	return b.parameters, nil
}
func objectNameText(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}
func namespaceForName(view CatalogRead, name parser.ObjectName, creation bool) (ObjectAddress, error) {
	if len(name.Parts) == 1 {
		a := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
		if _, ok := view.state.descriptors[a]; ok {
			return a, nil
		}
	} else if len(name.Parts) == 2 {
		a, ok := view.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
		if ok {
			return a, nil
		}
		if creation {
			return ObjectAddress{}, sqlError("3F000", fmt.Sprintf("schema %q does not exist", name.Parts[0].Text))
		}
		return ObjectAddress{}, nil
	}
	return ObjectAddress{}, nil
}
func (e *Engine) describe(stmt parser.Statement) (binding, error) {
	b := binding{predicate: -1, order: -1}
	used := make(map[int]parser.Type)
	bind := func(v parser.Value, c parser.Column, where bool) error {
		if v.Kind != parser.ParameterValue {
			// Integer-to-int4 assignment is type-compatible. Its range check
			// belongs to execution, not preparation; unknown string input
			// still runs during binding below.
			if !where && v.Kind == parser.IntegerValue && c.Type == parser.Int4 {
				return nil
			}
			_, err := literal(v, c.Type, where)
			return err
		}
		if v.Parameter <= 0 {
			return sqlError("42P02", "undefined parameter")
		}
		if t, ok := used[v.Parameter]; ok && t != c.Type {
			return sqlError("0A000", "parameter reused with different destination types")
		}
		used[v.Parameter] = c.Type
		if v.Parameter > b.parameters.Count {
			b.parameters.Count = v.Parameter
		}
		return nil
	}
	column := func(name string) (int, error) {
		i, ok := b.relation.index[name]
		if !ok {
			return 0, sqlError("42703", fmt.Sprintf("column %q does not exist", name))
		}
		return i, nil
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		if len(s.Columns) == 0 {
			return b, sqlError("42601", "table requires columns")
		}
		seen := map[string]bool{}
		for _, c := range s.Columns {
			if seen[c.Name] {
				return b, sqlError("42701", "duplicate column")
			}
			seen[c.Name] = true
			if c.Type > parser.Bool {
				return b, sqlError("0A000", "unsupported column type")
			}
		}
	case *parser.Insert:
		var err error
		b.ref, err = e.resolveReference(CatalogRead{owner: e, state: e.state}, s.Name)
		if err != nil {
			return b, err
		}
		b.relation = e.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		if len(s.Targets) == 0 {
			width := len(s.Rows[0])
			if width > len(b.relation.columns) {
				return b, insertError("42601", "INSERT has more expressions than target columns", s.Rows[0][len(b.relation.columns)].Pos)
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
		for rowIndex, row := range s.Rows {
			if rowIndex > 0 && len(row) != len(s.Rows[0]) {
				return b, insertError("42601", "VALUES lists must all be the same length", row[0].Pos)
			}
			if len(row) > len(b.targets) {
				return b, insertError("42601", "INSERT has more expressions than target columns", row[len(b.targets)].Pos)
			}
			if rowIndex == 0 && len(s.Targets) != 0 && len(row) < len(b.targets) {
				return b, insertError("42601", "INSERT has more target columns than expressions", s.Targets[len(row)].Pos)
			}
			for j, v := range row {
				c := b.relation.columns[b.targets[j]]
				if err := bind(v, c, false); err != nil {
					return b, insertValueError(err, c, v)
				}
			}
		}
	case *parser.Select:
		var err error
		b.ref, err = e.resolveReference(CatalogRead{owner: e, state: e.state}, s.Name)
		if err != nil {
			return b, err
		}
		b.relation = e.state.descriptors[b.ref.Address].(*relationDescriptor)
		b.storage = e.storage[b.ref.Address]
		if b.storage == nil {
			return b, fmt.Errorf("stuntgres: relation storage is missing")
		}
		if s.Star {
			b.projection = make([]int, len(b.relation.columns))
			for i := range b.projection {
				b.projection[i] = i
			}
		} else {
			for _, n := range s.Columns {
				i, er := column(n)
				if er != nil {
					return b, er
				}
				b.projection = append(b.projection, i)
			}
		}
		if s.Where != nil {
			b.predicate, err = column(s.Where.Column)
			if err != nil {
				return b, err
			}
			if err = bind(s.Where.Value, b.relation.columns[b.predicate], true); err != nil {
				return b, err
			}
		}
		if s.Order != nil {
			b.order, err = column(s.Order.Column)
			if err != nil {
				return b, err
			}
		}
	default:
		return b, sqlError("0A000", "unsupported statement")
	}
	if len(used) != b.parameters.Count {
		return b, sqlError("42P18", "could not determine unused parameter type")
	}
	b.parameters.Types = make([]parser.Type, b.parameters.Count)
	for n, t := range used {
		b.parameters.Types[n-1] = t
	}
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

func (e *Engine) resolveReference(view CatalogRead, name parser.ObjectName) (ObjectRef, error) {
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
		pg := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
		a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: pg, Name: relname}]
		if !ok {
			pub := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
			a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: pub, Name: relname}]
		}
	} else {
		ns, err := namespaceForName(view, name, false)
		if err != nil {
			return ObjectRef{}, err
		}
		a, ok = view.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: relname}]
	}
	if !ok {
		return ObjectRef{}, sqlError("42P01", fmt.Sprintf("relation %q does not exist", objectNameText(name)))
	}
	if _, ok := view.state.descriptors[a].(*relationDescriptor); !ok {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	return ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: a}, nil
}

func (e *Engine) resolveCreation(view CatalogRead, name parser.ObjectName) (ObjectRef, error) {
	if err := e.validateCatalogViewLocked(view); err != nil {
		return ObjectRef{}, err
	}
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	ns, err := namespaceForName(view, name, true)
	if err != nil {
		return ObjectRef{}, err
	}
	if ns.ObjectID != PublicNamespaceOID {
		return ObjectRef{}, sqlError("0A000", "creation outside public is unsupported")
	}
	return ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: ns}, nil
}

func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}

func insertValueError(err error, column parser.Column, value parser.Value) error {
	sqlErr, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	switch sqlErr.Code {
	case "42804":
		source := "unknown"
		switch value.Kind {
		case parser.IntegerValue:
			source = "integer"
			if value.Integer < -2147483648 || value.Integer > 2147483647 {
				source = "bigint"
			}
		case parser.StringValue:
			source = "text"
		case parser.BooleanValue:
			source = "boolean"
		}
		destination := "integer"
		switch column.Type {
		case parser.Int8:
			destination = "bigint"
		case parser.Text:
			destination = "text"
		case parser.Bool:
			destination = "boolean"
		}
		sqlErr.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", column.Name, destination, source)
		sqlErr.Hint = "You will need to rewrite or cast the expression."
		sqlErr.Position = strconv.Itoa(value.Pos.Character)
	case "22P02", "22003":
		if value.Kind == parser.StringValue {
			sqlErr.Position = strconv.Itoa(value.Pos.Character)
		}
	}
	return sqlErr
}

func notNullError(table, column string, row []driver.Value) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: fmt.Sprintf("null value in column \"%s\" of relation \"%s\" violates not-null constraint", column, table),
		Schema:  "public", Table: table, Column: column, Detail: failingRowDetail(row),
	}
}

func prepareArgs(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, fmt.Errorf("stuntgres: named parameters are not supported")
		}
		if a.Ordinal != i+1 {
			return nil, fmt.Errorf("stuntgres: parameter ordinal %d, want %d", a.Ordinal, i+1)
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a.Value)
		if err != nil {
			if _, ok := a.Value.(driver.Valuer); ok {
				return nil, err
			}
			return nil, sqlError("0A000", "unsupported parameter value")
		}
		switch v.(type) {
		case nil, int64, string, []byte, bool:
		default:
			return nil, sqlError("0A000", "unsupported parameter value")
		}
		values[i] = v
	}
	return values, nil
}

func (e *Engine) Execute(op *Operation, stmt parser.Statement, args []driver.NamedValue) (Result, error) {
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.Set, *parser.Show:
		return e.executeSetting(op, stmt, args)
	}
	values, err := prepareArgs(args)
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	if err = ctx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if s, ok := stmt.(*parser.CreateTable); ok {
		if _, er := e.describe(s); er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
	}
	if s, ok := stmt.(*parser.CreateTable); ok {
		if len(values) != 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", len(values))
		}
		nsref, er := e.resolveCreation(CatalogRead{owner: e, state: e.state}, s.Name)
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		ns := nsref.Address
		key := NameKey{ClassID: RelationClassID, Namespace: ns, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
		if _, exists := e.state.names[key]; exists {
			e.mu.Unlock()
			return Result{}, sqlError("42P07", fmt.Sprintf("relation %q already exists", key.Name))
		}
		oid, er := e.allocateOIDLocked()
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		rev, er := e.revisionLocked()
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		addr := ObjectAddress{ClassID: RelationClassID, ObjectID: oid}
		rd := &relationDescriptor{address: addr, namespace: ns, name: key.Name, revision: rev, columns: append([]parser.Column(nil), s.Columns...), index: make(map[string]int, len(s.Columns)), attributes: make([]int32, len(s.Columns))}
		for i, c := range rd.columns {
			rd.index[c.Name] = i
			rd.attributes[i] = int32(i + 1)
		}
		base := CatalogRead{owner: e, state: e.state}
		stage := e.BeginCatalogStage(base)
		er = stage.Create(rd)
		e.mu.Unlock()
		if er != nil {
			return Result{}, er
		}
		prepared, er := e.ValidateCatalogStage(ctx, stage)
		if er != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, er
		}
		_, er = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
		if _, conflict := er.(CatalogConflict); conflict {
			return Result{}, sqlError("42P07", fmt.Sprintf("relation %q already exists", key.Name))
		}
		return Result{}, er
	}
	b, err := e.describe(stmt)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if len(values) != b.parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", len(values), b.parameters.Count)
	}
	canonical := make([]driver.Value, len(values))
	for i, v := range values {
		canonical[i], err = parameter(v, b.parameters.Types[i])
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	resolve := func(v parser.Value, t parser.Type, where bool) (driver.Value, error) {
		if v.Kind == parser.ParameterValue {
			return canonical[v.Parameter-1], nil
		}
		return literal(v, t, where)
	}
	switch s := stmt.(type) {
	case *parser.Insert:
		rows := make([][]driver.Value, len(s.Rows))
		for i, source := range s.Rows {
			if err = ctx.Err(); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			row := make([]driver.Value, len(b.relation.columns))
			for j, v := range source {
				target := b.targets[j]
				c := b.relation.columns[target]
				row[target], err = resolve(v, c.Type, false)
				if err != nil {
					e.mu.Unlock()
					return Result{}, insertValueError(err, c, v)
				}
			}
			rows[i] = row
		}
		// All supported expressions are literals or canonical parameters.
		// Finish assignment conversion before executor constraint checks,
		// so a deferred constant range failure is not masked by NOT NULL.
		for _, row := range rows {
			if err = ctx.Err(); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			for j, c := range b.relation.columns {
				if row[j] == nil && !c.Nullable {
					e.mu.Unlock()
					return Result{}, notNullError(b.relation.name, c.Name, row)
				}
			}
		}
		if err = ctx.Err(); err == nil {
			b.storage.rows = append(b.storage.rows, rows...)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(len(rows))}, nil
	case *parser.Select:
		var predicate driver.Value
		if s.Where != nil {
			predicate, err = resolve(s.Where.Value, b.relation.columns[b.predicate].Type, true)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
		snap := &Snapshot{rows: b.storage.rows, projection: b.projection}
		columns := make([]parser.Column, len(b.projection))
		for i, j := range b.projection {
			columns[i] = b.relation.columns[j]
		}
		e.mu.Unlock()
		if err = snap.selectRows(ctx, b.predicate, predicate, b.order, s.Order != nil && s.Order.Desc); err != nil {
			return Result{}, err
		}
		return Result{Count: int64(snap.Len()), Columns: columns, Rows: snap}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
}
