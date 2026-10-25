package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func (s *StatementScope) executePLCursor(d *catalog.Procedure, run *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (value sqltype.Value, flow proc.PLFlow, err error) {
	noticeStart := len(s.Op.Notices)
	raw := in.Syntax.Cursor
	var failure proc.PLDiagnosticQuery
	action := "OPEN"
	switch raw.Action {
	case parser.PLOpenCursor:
		_, _, err = s.openPLCursor(run, f, in.Cursor, raw, read, &failure)
	case parser.PLFetchCursor, parser.PLMoveCursor:
		action = "FETCH"
		if raw.Action == parser.PLMoveCursor {
			action = "MOVE"
		}
		err = s.movePLCursor(run, f, in.Cursor, raw, read, &failure)
	case parser.PLCloseCursor:
		action = "CLOSE"
		var name string
		name, err = f.CursorName(in.Cursor.Slot)
		if err == nil {
			restore := s.activateSQLRoutine(nil, read)
			_, err = s.closeCursor(&parser.CloseCursor{Name: parser.Identifier{Text: name}})
			err = restore(err)
		}
	case parser.PLForCursor:
		return s.forPLCursor(d, run, f, in, read)
	default:
		return value, proc.PLFlow{}, fmt.Errorf("stuntgres: invalid PL cursor instruction")
	}
	if err != nil {
		err = s.plRuntimeQueryError(err, read, d, in.Syntax.Pos, "at "+action, failure)
	}
	if noticeStart != len(s.Op.Notices) {
		s.appendPLNoticeContext(noticeStart, s.plNoticeOrigin(f, in.Syntax.Pos, "at "+action))
	}
	return value, flow, err
}

func (s *StatementScope) openPLCursor(run *proc.PLRun, f *proc.PLFrame, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement, read catalog.Read, failure *proc.PLDiagnosticQuery) (opened *sqlCursor, name string, err error) {
	v := f.Values[in.Slot]
	generated := v.Type() == nil || v.IsNull()
	if !generated {
		name, err = v.Text()
		if err != nil {
			return nil, "", err
		}
		if name == "" || PayloadOf(s.Op.Session).Cursors.Entries[name] != nil || protocolPortalExists(s.Op.Session, name) {
			return nil, name, SQLError("42P03", fmt.Sprintf("cursor %q already in use", name))
		}
	}
	query, scroll := raw.Query, raw.Scroll
	queryStart, queryEnd := raw.QueryStart, raw.QueryEnd
	recipe := f.Program.Variables[in.Slot].Cursor
	if recipe != nil {
		if in.ArgumentQuery != nil {
			values, present, e := s.evaluatePLArgumentRow(f, in.ArgumentQuery, read)
			if e != nil {
				*failure = plCursorArgumentDiagnostic(f.Program, in, raw)
				return nil, name, e
			}
			f.SetFound(present)
			for i, arg := range in.Arguments {
				value := sqltype.Null(f.Program.Variables[arg.Slot].Usage.Type)
				if i < len(values) {
					value = values[i]
				}
				if _, e = run.AssignVariable(arg.Slot, value, proc.PLOrigin{}); e != nil {
					return nil, name, e
				}
			}
		}
		query, scroll = recipe.Syntax.Query, recipe.Syntax.Scroll
		queryStart, queryEnd = recipe.Syntax.QueryStart, recipe.Syntax.QueryEnd
	}
	var parameters []sqltype.Value
	var inputs []sqltype.Usage
	var command string
	dynamic := raw.Command != nil
	if dynamic {
		commandValue, _, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{raw.Command}, Pos: raw.Command.SourcePosition()}, read, nil, plNoticeOrigin{})
		if e != nil {
			*failure = proc.PLCursorExpressionDiagnostic(e, f.Program.Source, raw.CommandStart, raw.CommandEnd)
			return nil, name, e
		}
		if commandValue.IsNull() {
			return nil, name, SQLError("22004", "query string argument of EXECUTE is null")
		}
		command, e = commandValue.Type().OutputWithEnvironment(s.Op.Context(), commandValue, s.Output)
		if e != nil {
			return nil, name, e
		}
		parameters = make([]sqltype.Value, len(raw.Using))
		inputs = make([]sqltype.Usage, len(raw.Using))
		for i, expression := range raw.Using {
			value, usage, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{expression.Expression}, Pos: expression.Expression.SourcePosition()}, read, nil, plNoticeOrigin{})
			if e != nil {
				*failure = proc.PLCursorExpressionDiagnostic(e, f.Program.Source, expression.Start, expression.End)
				return nil, name, e
			}
			parameters[i] = value
			inputs[i] = usage
		}
		statements, e := parser.ParseWithOptions(command, parser.Options{StandardConformingStrings: !s.Op.NonstandardStrings, Context: s.Op.Context(), Notice: s.Op.ParseNotice})
		if e != nil {
			*failure = proc.PLDiagnosticQuery{Text: command, Callback: proc.PLSQLCallback}
			return nil, name, e
		}
		if len(statements) != 1 {
			return nil, name, SQLError("42P11", "cannot open multi-query plan as cursor")
		}
		var ok bool
		query, ok = statements[0].(*parser.Select)
		if !ok {
			kind := "utility"
			switch statements[0].(type) {
			case *parser.Insert:
				kind = "INSERT"
			case *parser.Update:
				kind = "UPDATE"
			case *parser.Delete:
				kind = "DELETE"
			case *parser.Merge:
				kind = "MERGE"
			}
			return nil, name, SQLError("42P11", "cannot open "+kind+" query as cursor")
		}
	} else {
		parameters = f.Values[:f.Program.FormalCount]
		if recipe != nil {
			saved := f.Scopes
			f.Scopes = recipe.Scopes
			defer func() { f.Scopes = saved }()
		}
		command = parser.PLExpressionSource(f.Program.Source, queryStart, queryEnd)
	}
	restore := s.activateSQLRoutine(inputs, read)
	defer func() { err = restore(err) }()
	if !dynamic {
		s.sqlRoutine.lexical = f
	}
	s.sqlRoutine.cursor = &parser.DeclareCursor{Name: parser.Identifier{Text: name}, Scroll: scroll, Query: query, Source: command}
	if !generated {
		s.sqlRoutine.cursorName = &s.sqlRoutine.cursor.Name.Text
	}
	if err = s.AcquireRelation(query, true); err != nil {
		*failure = proc.PLDiagnosticQuery{Text: command, Callback: proc.PLSQLCallback}
		if !dynamic {
			failure.Offset = queryStart.Character - 1
		}
		return nil, name, err
	}
	_, err = s.Engine().executeStatement(s, query, len(parameters), func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, parameters)
		return nil
	})
	if err != nil {
		*failure = proc.PLDiagnosticQuery{Text: command, Callback: proc.PLSQLCallback}
		if !dynamic {
			failure.Offset = queryStart.Character - 1
		}
		return nil, name, err
	}
	opened, name = s.sqlRoutine.openedCursor, s.sqlRoutine.openedCursorName
	if opened == nil {
		return nil, name, fmt.Errorf("stuntgres: PL cursor installation did not publish producer")
	}
	if generated {
		variable := &f.Program.Variables[in.Slot]
		if variable.Constant {
			return nil, name, SQLError("22005", fmt.Sprintf("variable %q is declared CONSTANT", variable.Name))
		}
		f.Values[in.Slot] = sqltype.String(variable.Usage.Type, name)
	}
	return opened, name, nil
}

func (s *StatementScope) movePLCursor(run *proc.PLRun, f *proc.PLFrame, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement, read catalog.Read, failure *proc.PLDiagnosticQuery) (err error) {
	name, err := f.CursorName(in.Slot)
	if err != nil {
		return err
	}
	cursor, err := s.resolveCursorPortal(name)
	if err != nil {
		return err
	}
	motion := &parser.FetchCursor{Name: parser.Identifier{Text: name}, Direction: raw.Motion.Direction, Count: 1, All: raw.Motion.All, Move: raw.Action == parser.PLMoveCursor}
	if raw.Motion.Count != nil {
		value, _, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{raw.Motion.Count}, Pos: raw.Motion.Count.SourcePosition()}, read, nil, plNoticeOrigin{})
		if e != nil {
			*failure = proc.PLCursorExpressionDiagnostic(e, f.Program.Source, raw.Motion.CountStart, raw.Motion.CountEnd)
			return e
		}
		if value.IsNull() {
			return SQLError("22004", "relative or absolute cursor position is null")
		}
		integer, ok := read.LookupType(sqltype.Int4)
		if !ok {
			return fmt.Errorf("stuntgres: missing cursor position type")
		}
		value, e = s.convertPLValue(value, integer.Usage())
		if e != nil {
			return e
		}
		motion.Count, e = value.Integer()
		if e != nil {
			return e
		}
	}
	return s.movePLCursorPortal(run, f, in.Targets, cursor, motion, read)
}

func (s *StatementScope) movePLCursorPortal(run *proc.PLRun, f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, motion *parser.FetchCursor, read catalog.Read) (err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	result, err := s.fetchCursorPortal(cursor, motion)
	if err != nil {
		return err
	}
	if motion.Move {
		f.SetFound(result.Count != 0)
		return nil
	}
	rows, ok := result.Rows.(*exec.FinalRows)
	if !ok {
		return fmt.Errorf("stuntgres: FETCH lacks typed descriptor")
	}
	defer func() {
		if failure := rows.Close(); err == nil {
			err = failure
		}
	}()
	// Cursor motion already produces an immutable detached typed batch of at
	// most one row. Transfer its values directly, without a driver projection
	// or a second scalar-row copy. Empty rows are shaped only when needed.
	present := rows.Count != 0
	fields := rows.Values
	if err = s.assignPLCursorTargets(run, f, targets, cursor, fields, read); err != nil {
		return err
	}
	f.SetFound(present)
	return nil
}

func (s *StatementScope) assignPLCursorTargets(run *proc.PLRun, f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, fields []sqltype.Value, read catalog.Read) error {
	if len(targets) == 1 {
		v := f.Program.Variables[targets[0].Slot]
		if len(targets[0].Steps) == 0 && (v.Record || v.Usage.Type.BaseType().IsComposite()) {
			return run.AssignRow(targets[0].Slot, &cursor.recordShape, (*plColumns)(&cursor.schema), fields, proc.PLOrigin{})
		}
		value := sqltype.Null(f.Program.Variables[f.Program.FoundSlot].Usage.Type)
		if len(fields) != 0 {
			value = fields[0]
		}
		candidate, err := s.preparePLTargetValue(run, f, targets[0], value, f.Values[targets[0].Slot], read)
		if err == nil {
			f.Values[targets[0].Slot] = candidate
		}
		return err
	}
	pending := make([]sqltype.Value, len(targets))
	for i, target := range targets {
		current := f.Values[target.Slot]
		for j := i - 1; j >= 0; j-- {
			if targets[j].Slot == target.Slot {
				current = pending[j]
				break
			}
		}
		value := sqltype.Null(f.Program.Variables[f.Program.FoundSlot].Usage.Type)
		if i < len(fields) {
			value = fields[i]
		}
		candidate, err := s.preparePLTargetValue(run, f, target, value, current, read)
		if err != nil {
			return err
		}
		pending[i] = candidate
	}
	for i, target := range targets {
		f.Values[target.Slot] = pending[i]
	}
	return nil
}

func (s *StatementScope) forPLCursor(d *catalog.Procedure, run *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (value sqltype.Value, flow proc.PLFlow, err error) {
	original := f.Values[in.Cursor.Slot]
	wasNull := original.Type() == nil || original.IsNull()
	var failure proc.PLDiagnosticQuery
	origin := s.plNoticeOrigin(f, in.Syntax.Pos, "at FOR over cursor")
	noticeStart := len(s.Op.Notices)
	cursor, name, err := s.openPLCursor(run, f, in.Cursor, in.Syntax.Cursor, read, &failure)
	s.appendPLNoticeContext(noticeStart, origin)
	if err != nil {
		return value, proc.PLFlow{}, s.plRuntimeQueryError(err, read, d, in.Syntax.Pos, "at FOR over cursor", failure)
	}
	cursor.pinned = true
	defer func() {
		noticeStart := len(s.Op.Notices)
		defer func() { s.appendPLNoticeContext(noticeStart, origin) }()
		cursor.pinned = false
		r := PayloadOf(s.Op.Session).Cursors
		if r.Entries[name] == cursor {
			delete(r.Entries, name)
			cleanupErr := cursor.dispose(context.WithoutCancel(s.Op.Context()))
			if err == nil {
				err = cleanupErr
			}
		}
		f.Values[in.Cursor.LoopSlot] = sqltype.Value{}
		if wasNull {
			f.Values[in.Cursor.Slot] = sqltype.Null(f.Program.Variables[in.Cursor.Slot].Usage.Type)
		}
	}()
	iterated := false
	fetch := &parser.FetchCursor{Name: parser.Identifier{Text: name}, Count: 1}
	targets := []proc.PLTarget{{Slot: in.Cursor.LoopSlot}}
	owner := in.Cursor.Loop.Statements[0].Block.Control
	for {
		if err = s.Op.Context().Err(); err != nil {
			return value, proc.PLFlow{}, err
		}
		previousFound := f.Values[f.Program.FoundSlot]
		noticeStart := len(s.Op.Notices)
		err = s.movePLCursorPortal(run, f, targets, cursor, fetch, read)
		s.appendPLNoticeContext(noticeStart, origin)
		found, _ := f.Values[f.Program.FoundSlot].Boolean()
		f.Values[f.Program.FoundSlot] = previousFound
		if err != nil {
			return value, proc.PLFlow{}, s.plRuntimeError(err, read, d, parser.Position{}, parser.Position{}, in.Syntax.Pos, "at FOR over cursor", false)
		}
		if !found {
			break
		}
		iterated = true
		value, flow, err = run.Block(in.Cursor.Loop)
		if err != nil {
			return value, flow, err
		}
		if flow.Kind == proc.PLNormal || flow.Kind == proc.PLContinue && flow.Target == owner {
			continue
		}
		f.SetFound(iterated)
		if flow.Kind == proc.PLExit && flow.Target == owner {
			return value, proc.PLFlow{}, nil
		}
		return value, flow, nil

	}
	f.SetFound(iterated)
	return value, proc.PLFlow{}, nil
}

func plCursorArgumentDiagnostic(program *proc.PLProgram, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement) proc.PLDiagnosticQuery {
	named := false
	for _, actual := range raw.Arguments {
		named = named || actual.Name.Text != ""
	}
	var query strings.Builder
	positions := make([]proc.PLDiagnosticPosition, len(in.Arguments))
	characters := 0
	for i, argument := range in.Arguments {
		if i != 0 {
			query.WriteString(", ")
			characters += 2
		}
		positions[i] = proc.PLDiagnosticPosition{Start: argument.Start.Character, End: argument.End.Character, Output: characters + 1}
		text := parser.PLExpressionSource(program.Source, argument.Start, argument.End)
		query.WriteString(text)
		characters += utf8.RuneCountInString(text)
		if named {
			label := " AS " + catalog.DependencyIdentifier(program.Variables[argument.Slot].Name)
			query.WriteString(label)
			characters += utf8.RuneCountInString(label)
		}
	}
	return proc.PLDiagnosticQuery{Text: query.String(), Positions: positions, Callback: proc.PLExpressionCallbackKind}
}

// Ordinary admission binds every target before opening the first-row producer.
// No second-row probe: bound cursor arguments use nonSTRICT INTO semantics,
// unlike scalar RETURN/assignment expressions.
func (s *StatementScope) evaluatePLArgumentRow(f *proc.PLFrame, query *parser.Select, read catalog.Read) (values []sqltype.Value, present bool, err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical, s.sqlRoutine.plArgumentRow = f, true
	result, err := s.executePLExpressionQuery(f, query, nil)
	if err != nil {
		return nil, false, err
	}
	rows, ok := result.Rows.(*exec.FinalRows)
	if !ok {
		return nil, false, fmt.Errorf("stuntgres: cursor argument projection lacks detached typed output")
	}
	defer func() {
		if closeErr := result.Rows.Close(); err == nil {
			err = closeErr
		}
	}()
	for _, rows := range result.RowSets {
		defer rows.Close()
	}
	if err := s.Op.Context().Err(); err != nil {
		return nil, false, err
	}
	// The shared first-row producer already owns a detached flat typed row.
	// Transfer that slice directly; Close releases its header, not the datums.
	present, values = rows.Count != 0, rows.Values
	if !present {
		schema := rows.Schema()
		values = make([]sqltype.Value, len(schema))
		for i, column := range schema {
			values[i] = sqltype.Null(column.Usage.Type)
		}
	}
	return values, present, nil
}
