package core

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ValidateConnectby(n plan.InstalledConnectbyExpr) error {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return err
	}
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale installed connectby declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledConnectby(n, auth.Catalog)
}
func connectbyReturnError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "invalid connectby return type", Detail: detail}
}
func validateConnectbyReturn(r functions.ConnectbyRequest) error {
	width := 3
	if r.Branch {
		width++
	}
	if r.Ordered {
		width++
	}
	if r.Result.CompositeLen() != width {
		return connectbyReturnError(fmt.Sprintf("Return row must have %d columns, not %d.", width, r.Result.CompositeLen()))
	}
	if r.Result.CompositeMember(2).Usage.Type.ID() != sqltype.Int4 {
		return connectbyReturnError("Third return column (depth) must be type integer.")
	}
	if r.Branch && r.Result.CompositeMember(3).Usage.Type.ID() != sqltype.Text {
		return connectbyReturnError("Fourth return column (branch) must be type text.")
	}
	if r.Ordered && r.Result.CompositeMember(width-1).Usage.Type.ID() != sqltype.Int4 {
		ordinal := "Fourth"
		if r.Branch {
			ordinal = "Fifth"
		}
		return connectbyReturnError(ordinal + " return column (serial) must be type integer.")
	}
	return nil
}
func connectbyUsageName(u sqltype.Usage) string {
	if u.Type.IsArray() && !u.Type.IsCatalogVector() {
		u.Type = u.Type.Element()
		return connectbyUsageName(u) + "[]"
	}
	name := u.Type.DiagnosticName()
	if u.Typmod < 0 {
		return name
	}
	declaration, err := u.Declaration()
	if err != nil {
		return name
	}
	if u.Type.ID() == sqltype.Interval {
		return strings.ToLower(declaration)
	}
	if at := strings.IndexByte(declaration, '('); at >= 0 {
		end := strings.IndexByte(declaration[at:], ')')
		if end < 0 {
			return name
		}
		modifier := declaration[at : at+end+1]
		if zone := strings.Index(name, " with"); zone >= 0 {
			return name[:zone] + modifier + name[zone:]
		}
		return name + modifier
	}
	return name
}
func validateConnectbySource(result *sqltype.Descriptor, schema []catalog.Column) error {
	if len(schema) < 2 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid connectby source data query", Detail: "The query must return at least two columns."}
	}
	for i, label := range []string{"key", "parent key"} {
		source, target := schema[i].Usage, result.CompositeMember(i).Usage
		if source.Type.ID() != target.Type.ID() || target.Typmod >= 0 && source.Typmod != target.Typmod {
			return connectbyReturnError("Source " + label + " type " + connectbyUsageName(source) + " does not match return " + label + " type " + connectbyUsageName(target) + ".")
		}
	}
	return nil
}

type connectbyChildren struct {
	rows   [][2]sqltype.Value
	schema []catalog.Column
}
type connectbyFrame struct {
	children connectbyChildren
	index    int
	level    int64
	branch   string
}

func (h *statementHost) Connectby(r functions.ConnectbyRequest) ([]sqltype.Value, error) {
	ctx := h.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateConnectbyReturn(r); err != nil {
		return nil, err
	}
	input, output := r.Input, r.Output
	var scratch [5]sqltype.Value
	fields := scratch[:r.Result.CompositeLen()]
	serial := int64(0)
	rows := make([]sqltype.Value, 0)
	emit := func(key, parent *string, level int64, branch string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i, text := range [2]*string{key, parent} {
			usage := r.Result.CompositeMember(i).Usage
			fields[i] = sqltype.Null(usage.Type)
			if text != nil {
				value, err := usage.Type.InputWithEnvironment(ctx, *text, usage.Typmod, input)
				if err != nil {
					return err
				}
				fields[i] = value
			}
			if text == nil {
				if err := usage.Type.CheckDomainInput(ctx, fields[i], input); err != nil {
					return err
				}
			}
		}
		var err error
		fields[2], err = sqltype.Integer(r.Result.CompositeMember(2).Usage.Type, level)
		if err != nil {
			return err
		}
		if r.Branch {
			fields[3] = sqltype.String(r.Result.CompositeMember(3).Usage.Type, branch)
		}
		serial++
		if r.Ordered {
			at := len(fields) - 1
			fields[at], err = sqltype.Integer(r.Result.CompositeMember(at).Usage.Type, serial)
			if err != nil {
				return err
			}
		}
		row, err := sqltype.CompositeValue(r.Result, fields)
		if err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	}
	if err := emit(&r.Start, nil, 0, r.Start); err != nil {
		return nil, err
	}
	queryChildren := func(key string) (connectbyChildren, error) {
		var value [1]sqltype.Value
		value[0] = sqltype.String(r.Text, key)
		literal, err := functions.FormatText(ctx, "%L", functions.FormatValues{Ordinary: value[:]}, r.Result.CompositeMember(2).Usage.Type, output)
		if err != nil {
			return connectbyChildren{}, err
		}
		text := "SELECT " + r.Key + ", " + r.Parent + " FROM " + r.Relation + " WHERE " + r.Parent + " = " + literal + " AND " + r.Key + " IS NOT NULL AND " + r.Key + " <> " + r.Parent
		if r.Ordered {
			text += " ORDER BY " + r.Order
		}
		children, err := h.scope().connectbyQuery(text)
		if err != nil {
			return connectbyChildren{}, err
		}
		if len(children.rows) > 0 {
			if err := validateConnectbySource(r.Result, children.schema); err != nil {
				return connectbyChildren{}, err
			}
		}
		return children, nil
	}
	children, err := queryChildren(r.Start)
	if err != nil {
		return nil, err
	}
	stack := []connectbyFrame{{children: children, level: 1, branch: r.Start}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame := &stack[len(stack)-1]
		if frame.index == len(frame.children.rows) {
			stack = stack[:len(stack)-1]
			continue
		}
		child := frame.children.rows[frame.index]
		frame.index++
		var key, parent *string
		if !child[0].IsNull() {
			text, err := child[0].Type().OutputWithEnvironment(ctx, child[0], output)
			if err != nil {
				return nil, err
			}
			key = &text
		}
		if !child[1].IsNull() {
			text, err := child[1].Type().OutputWithEnvironment(ctx, child[1], output)
			if err != nil {
				return nil, err
			}
			parent = &text
		}
		branch := frame.branch
		if key != nil {
			if strings.Contains(r.Delimiter+branch+r.Delimiter, r.Delimiter+*key+r.Delimiter) {
				return nil, SQLError("42P19", "infinite recursion detected")
			}
			branch += r.Delimiter + *key
		}
		level := frame.level
		if err := emit(key, parent, level, branch); err != nil {
			return nil, err
		}
		if key == nil || r.MaxDepth > 0 && level >= r.MaxDepth {
			continue
		}
		children, err := queryChildren(*key)
		if err != nil {
			return nil, err
		}
		stack = append(stack, connectbyFrame{children: children, level: level + 1, branch: branch})
	}
	return rows, nil
}

// Each nested statement uses the real binder/executor, the current private
// catalog and the outer coherent data view. Only the last SELECT supplies rows.
func (s *StatementScope) connectbyQuery(text string) (children connectbyChildren, err error) {
	if err = s.enterSQLRoutine(); err != nil {
		return children, err
	}
	defer s.leaveSQLRoutine()
	defer func() {
		err = exec.RewriteSQLDiagnostic(err, text)
		if err != nil {
			children = connectbyChildren{}
		}
	}()
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	s.Op.EffectiveSettings().Mu.Lock()
	standard := s.Op.EffectiveSettings().Value("standard_conforming_strings") == "on"
	s.Op.EffectiveSettings().Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: s.executionContext(), StandardConformingStrings: standard, Notice: s.Op.ParseNotice, ClosedRoutineFormals: true})
	if err != nil {
		return children, err
	}
	for _, statement := range statements {
		part, stop, partErr := s.connectbyStatement(statement, read)
		if partErr != nil {
			return children, partErr
		}
		children = part
		if stop {
			break
		}
	}
	return children, nil
}

// activateConnectbySource borrows the saved outer frontier, not its fixed set
// of admitted source members. Activation's restoration owns the local View.
func (s *StatementScope) activateConnectbySource(read catalog.Read) func(error) error {
	outer := s.View
	restore := s.ActivateSQLRoutine(nil, read)
	if outer != nil {
		s.sqlRoutine.sourceBase = outer.Load()
	}
	return restore
}

func (s *StatementScope) connectbyStatement(statement parser.Statement, read catalog.Read) (children connectbyChildren, stop bool, err error) {
	restore := s.activateConnectbySource(read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.scalar, s.sqlRoutine.rewrite, s.sqlRoutine.readOnlySPI = false, true, true
	// SPI rejects transaction commands and frontend COPY with a negative
	// result, terminating the list without throwing an SQL error. connectby
	// consumes only SPI_OK_SELECT, so that outcome supplies no children.
	switch command := statement.(type) {
	case *parser.TransactionControl, *parser.SavepointControl:
		return children, true, nil
	case *parser.Copy:
		if command.Endpoint != parser.CopyFile && command.Endpoint != parser.CopyProgram {
			return children, true, nil
		}
	}
	// Utility plans are not executed (or execution-validated) in read-only
	// SPI. Optimizable commands must still undergo normal analysis first.
	switch statement.(type) {
	case *parser.Select, *parser.SettingCall, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
	default:
		return children, false, SQLError("0A000", RoutineCommandTag(statement)+" is not allowed in a non-volatile function")
	}
	if err = s.AcquireRelation(statement, true); err != nil {
		return children, false, err
	}
	description, err := s.Engine().describeOperation(s, statement)
	if err != nil {
		return children, false, err
	}
	s.Expected = &description
	result, err := s.Engine().executeStatement(s, statement, 0, func(_ context.Context, info analyze.ParameterInfo, _ []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != 0 {
			return fmt.Errorf("stuntgres: connectby SQL parameter frame is not empty")
		}
		return nil
	})
	defer func() {
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); err == nil {
				err = closeErr
			}
		}
		for _, rows := range result.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	if err != nil {
		return children, false, err
	}
	selecting := false
	switch command := statement.(type) {
	case *parser.Select:
		selecting = command.Into == nil
	case *parser.SettingCall: // Specialized SELECT set_config/current_setting form.
		selecting = true
	}
	if !selecting || result.Rows == nil {
		return children, false, nil
	}
	schema := result.Rows.Schema()
	kept := len(schema)
	if kept > 2 {
		kept = 2
	}
	children.schema = append([]catalog.Column(nil), schema[:kept]...)
	buffer := make([]sqltype.Value, len(schema))
	for {
		if err = s.executionContext().Err(); err != nil {
			return children, false, err
		}
		present, nextErr := result.Rows.Next(s.executionContext(), buffer)
		if nextErr != nil {
			return children, false, nextErr
		}
		if !present {
			break
		}
		var row [2]sqltype.Value
		copy(row[:], buffer)
		children.rows = append(children.rows, row)
	}
	return children, false, nil
}

// SPI classifies the analyzed executable occurrences, not outer SELECT syntax
// or analysis-only dependencies. The caller still owns the canonical binding.
// Stored sources retain the same occurrence metadata and expand by identity.
// Keep this stricter all-statement contract separate from ts_rewrite's gate.
func connectbyReadOnlyViolation(statement parser.Statement, read catalog.Read, sources []exec.Leaf, query *plan.Query) string {
	if parser.ModifyingStatement(statement) {
		return RoutineCommandTag(statement)
	}
	var active map[ids.ObjectAddress]bool
	var mode lock.Mode
	present := false
	if query != nil {
		// A parent query's explicit markers precede child-query markers.
		// Order is meaningful only among occurrences owned by this level.
		plan.VisitExecutableQuery(query.Root, func(node plan.Node) {
			project, ok := node.(*plan.ProjectNode)
			if !ok || project.Syntax == nil || present {
				return
			}
			firstOrder := 0
			for pass := range 2 {
				for _, source := range sources {
					if source.AnalysisOnly || source.Query != project.Syntax || (source.Mark.Mode != 0) != (pass == 0) {
						continue
					}
					if present && !connectbyEarlierMark(source.Mark.Order, firstOrder) {
						continue
					}
					candidate, marked := source.Mark.Mode, source.Mark.Mode != 0
					if source.Relation.RelationKind == catalog.ViewRelation {
						if active == nil {
							active = make(map[ids.ObjectAddress]bool)
						}
						candidate, marked = connectbyStoredRowMark(read, source.Relation, source.Mark, active)
					}
					if marked {
						mode, present, firstOrder = candidate, true, source.Mark.Order
					}
				}
				if present {
					return
				}
			}
		}, func(expr.Expr) {})
	}
	if present {
		tag := "SELECT"
		if mode != 0 {
			tag += " " + analyze.RowLockClause(mode)
		}
		return tag
	}
	if parser.HasModifyingCTE(statement) {
		return "SELECT"
	}
	return ""
}

func connectbyEarlierMark(candidate, current int) bool {
	return candidate > 0 && (current == 0 || candidate < current)
}

// Presence is separate from strength: SPI rejects a planned reference/COPY
// marker too, while its LCS_NONE strength renders the plain SELECT command tag.
// Inherited and stored tuple strengths still merge along the occurrence path.
func connectbyStoredRowMark(read catalog.Read, relation *catalog.Relation, inherited plan.RowMark, active map[ids.ObjectAddress]bool) (lock.Mode, bool) {
	if relation.RelationKind != catalog.ViewRelation {
		return inherited.Mode, inherited.Mode != 0
	}
	if active[relation.Address()] {
		return 0, false
	}
	active[relation.Address()] = true
	defer delete(active, relation.Address())
	definition := stored.ViewOf(relation)
	syntax := definition.Syntax()
	// These represented stored-query boundaries are retained by PostgreSQL's
	// pullup rules. An enclosing mark therefore produces a parent COPY marker
	// before the descendant tuple marks. Merely inherited locking on a plain
	// view does not create this boundary: it can still propagate to its base.
	if inherited.Mode != 0 && (len(syntax.Locking) != 0 || syntax.Order != nil || syntax.Bounds != nil || len(syntax.With) != 0 || definition.Barrier()) {
		return 0, true
	}
	var mode lock.Mode
	present := false
	plan.VisitExecutableQuery(definition.Query().Root, func(node plan.Node) {
		project, ok := node.(*plan.ProjectNode)
		if !ok || project.Syntax == nil || present {
			return
		}
		from := analyze.ViewFromRoot(project)
		firstOrder := 0
		for pass := range 2 {
			for occurrence, leaf := range definition.Sources() {
				if leaf.AnalysisOnly || !connectbyLocalSource(from, occurrence) {
					continue
				}
				mark := stored.ViewLeafRowMark(definition, occurrence, inherited)
				if (mark.Mode != 0) != (pass == 0) || present && !connectbyEarlierMark(mark.Order, firstOrder) {
					continue
				}
				if child, ok := read.Relation(leaf.Ref.Address); ok {
					if candidate, marked := connectbyStoredRowMark(read, child, mark, active); marked {
						mode, present, firstOrder = candidate, true, mark.Order
					}
				}
			}
			if present {
				return
			}
		}
	}, func(expr.Expr) {})
	return mode, present
}

// Frozen leaves have global occurrence indexes, but first-mark orders restart
// at each SELECT. Derive local ownership from the retained bound FROM graph.
func connectbyLocalSource(node plan.Node, occurrence int) bool {
	switch node := node.(type) {
	case *plan.SourceNode:
		return node.Occurrence == occurrence
	case *plan.DerivedNode, *plan.ProjectNode:
		return false
	}
	if node == nil {
		return false
	}
	left, right := plan.QueryInputs(node)
	return connectbyLocalSource(left, occurrence) || connectbyLocalSource(right, occurrence)
}
