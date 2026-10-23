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

func (s *StatementScope) executePLCursor(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (value sqltype.Value, returned bool, err error) {
	raw := in.Syntax.Cursor
	var failure plDiagnosticQuery
	action := "OPEN"
	switch raw.Action {
	case parser.PLOpenCursor:
		_, _, err = s.openPLCursor(f, in.Cursor, raw, read, &failure)
	case parser.PLFetchCursor, parser.PLMoveCursor:
		action = "FETCH"
		if raw.Action == parser.PLMoveCursor {
			action = "MOVE"
		}
		err = s.movePLCursor(f, in.Cursor, raw, read, &failure)
	case parser.PLCloseCursor:
		action = "CLOSE"
		var name string
		name, err = plCursorName(f, in.Cursor.Slot)
		if err == nil {
			restore := s.activateSQLRoutine(nil, read)
			_, err = s.closeCursor(&parser.CloseCursor{Name: parser.Identifier{Text: name}})
			err = restore(err)
		}
	case parser.PLForCursor:
		return s.forPLCursor(d, f, in, read)
	default:
		return value, false, fmt.Errorf("stuntgres: invalid PL cursor instruction")
	}
	if err != nil {
		err = plRuntimeQueryError(err, read, d, in.Syntax.Pos, "at "+action, failure)
	}
	return value, returned, err
}

func plCursorName(f *proc.PLFrame, slot int) (string, error) {
	v := f.Values[slot]
	if v.Type() == nil || v.IsNull() {
		return "", sqlError("22004", fmt.Sprintf("cursor variable %q is null", f.Program.Variables[slot].Name))
	}
	return v.Text()
}

func (s *StatementScope) openPLCursor(f *proc.PLFrame, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement, read catalog.Read, failure *plDiagnosticQuery) (opened *sqlCursor, name string, err error) {
	v := f.Values[in.Slot]
	generated := v.Type() == nil || v.IsNull()
	if !generated {
		name, err = v.Text()
		if err != nil {
			return nil, "", err
		}
		if name == "" || payloadOf(s.op.Session).cursors.entries[name] != nil || protocolPortalExists(s.op.Session, name) {
			return nil, name, sqlError("42P03", fmt.Sprintf("cursor %q already in use", name))
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
				if _, e = s.assignPLVariable(f, arg.Slot, value); e != nil {
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
		commandValue, _, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{raw.Command}, Pos: raw.Command.SourcePosition()}, read, nil)
		if e != nil {
			*failure = plCursorExpressionDiagnostic(e, f.Program.Source, raw.CommandStart, raw.CommandEnd)
			return nil, name, e
		}
		if commandValue.IsNull() {
			return nil, name, sqlError("22004", "query string argument of EXECUTE is null")
		}
		command, e = commandValue.Type().OutputWithEnvironment(s.op.Context(), commandValue, s.output)
		if e != nil {
			return nil, name, e
		}
		parameters = make([]sqltype.Value, len(raw.Using))
		inputs = make([]sqltype.Usage, len(raw.Using))
		for i, expression := range raw.Using {
			value, usage, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{expression.Expression}, Pos: expression.Expression.SourcePosition()}, read, nil)
			if e != nil {
				*failure = plCursorExpressionDiagnostic(e, f.Program.Source, expression.Start, expression.End)
				return nil, name, e
			}
			parameters[i] = value
			inputs[i] = usage
		}
		statements, e := parser.ParseWithOptions(command, parser.Options{StandardConformingStrings: !s.op.NonstandardStrings, Context: s.op.Context(), Notice: s.op.ParseNotice})
		if e != nil {
			*failure = plDiagnosticQuery{Text: command, Callback: plSQLCallback}
			return nil, name, e
		}
		if len(statements) != 1 {
			return nil, name, sqlError("42P11", "cannot open multi-query plan as cursor")
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
			return nil, name, sqlError("42P11", "cannot open "+kind+" query as cursor")
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
	if err = s.acquireRelation(query, true); err != nil {
		*failure = plDiagnosticQuery{Text: command, Callback: plSQLCallback}
		if !dynamic {
			failure.Offset = queryStart.Character - 1
		}
		return nil, name, err
	}
	_, err = s.engine().executeStatement(s, query, len(parameters), func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, parameters)
		return nil
	})
	if err != nil {
		*failure = plDiagnosticQuery{Text: command, Callback: plSQLCallback}
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
			return nil, name, sqlError("22005", fmt.Sprintf("variable %q is declared CONSTANT", variable.Name))
		}
		f.Values[in.Slot] = sqltype.String(variable.Usage.Type, name)
	}
	return opened, name, nil
}

func (s *StatementScope) movePLCursor(f *proc.PLFrame, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement, read catalog.Read, failure *plDiagnosticQuery) (err error) {
	name, err := plCursorName(f, in.Slot)
	if err != nil {
		return err
	}
	cursor, err := s.resolveCursorPortal(name)
	if err != nil {
		return err
	}
	motion := &parser.FetchCursor{Name: parser.Identifier{Text: name}, Direction: raw.Motion.Direction, Count: 1, All: raw.Motion.All, Move: raw.Action == parser.PLMoveCursor}
	if raw.Motion.Count != nil {
		value, _, _, e := s.evaluatePLExpression(f, &parser.Select{Targets: []parser.Expr{raw.Motion.Count}, Pos: raw.Motion.Count.SourcePosition()}, read, nil)
		if e != nil {
			*failure = plCursorExpressionDiagnostic(e, f.Program.Source, raw.Motion.CountStart, raw.Motion.CountEnd)
			return e
		}
		if value.IsNull() {
			return sqlError("22004", "relative or absolute cursor position is null")
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
	return s.movePLCursorPortal(f, in.Targets, cursor, motion, read)
}

func (s *StatementScope) movePLCursorPortal(f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, motion *parser.FetchCursor, read catalog.Read) (err error) {
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
	if err = s.assignPLCursorTargets(f, targets, cursor, fields, read); err != nil {
		return err
	}
	f.SetFound(present)
	return nil
}

func (s *StatementScope) assignPLCursorTargets(f *proc.PLFrame, targets []proc.PLTarget, cursor *sqlCursor, fields []sqltype.Value, read catalog.Read) error {
	if len(targets) == 1 {
		v := f.Program.Variables[targets[0].Slot]
		if len(targets[0].Steps) == 0 && (v.Record || v.Usage.Type.BaseType().IsComposite()) {
			if cursor.recordShape == nil {
				members := make([]sqltype.CompositeMember, len(cursor.schema))
				for i, column := range cursor.schema {
					members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: column.Name, Usage: column.Usage}
				}
				shape, err := sqltype.RecordShape(members)
				if err != nil {
					return err
				}
				cursor.recordShape = shape
			}
			if len(fields) == 0 && len(cursor.schema) != 0 {
				fields = make([]sqltype.Value, len(cursor.schema))
				for i, column := range cursor.schema {
					fields[i] = sqltype.Null(column.Usage.Type)
				}
			}
			row, err := sqltype.OwnedCompositeValue(cursor.recordShape, fields)
			if err != nil {
				return err
			}
			_, err = s.assignPLVariable(f, targets[0].Slot, row)
			return err
		}
		value := sqltype.Null(f.Program.Variables[f.Program.FoundSlot].Usage.Type)
		if len(fields) != 0 {
			value = fields[0]
		}
		candidate, err := s.preparePLTargetValue(f, targets[0], value, f.Values[targets[0].Slot], read)
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
		candidate, err := s.preparePLTargetValue(f, target, value, current, read)
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

func (s *StatementScope) forPLCursor(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (value sqltype.Value, returned bool, err error) {
	original := f.Values[in.Cursor.Slot]
	wasNull := original.Type() == nil || original.IsNull()
	var failure plDiagnosticQuery
	cursor, name, err := s.openPLCursor(f, in.Cursor, in.Syntax.Cursor, read, &failure)
	if err != nil {
		return value, false, plRuntimeQueryError(err, read, d, in.Syntax.Pos, "at FOR over cursor", failure)
	}
	cursor.pinned = true
	defer func() {
		cursor.pinned = false
		r := payloadOf(s.op.Session).cursors
		if r.entries[name] == cursor {
			delete(r.entries, name)
			cleanupErr := cursor.dispose(s.op.Context())
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
	for {
		if err = s.op.Context().Err(); err != nil {
			return value, false, err
		}
		previousFound := f.Values[f.Program.FoundSlot]
		err = s.movePLCursorPortal(f, targets, cursor, fetch, read)
		found, _ := f.Values[f.Program.FoundSlot].Boolean()
		f.Values[f.Program.FoundSlot] = previousFound
		if err != nil {
			return value, false, plRuntimeError(err, read, d, parser.Position{}, parser.Position{}, in.Syntax.Pos, "at FOR over cursor", false)
		}
		if !found {
			break
		}
		iterated = true
		value, returned, err = s.executePLBlock(d, f, in.Cursor.Loop, read)
		if err != nil || returned {
			return value, returned, err
		}
	}
	f.SetFound(iterated)
	return value, false, nil
}

func plCursorExpressionDiagnostic(err error, source string, start, end parser.Position) plDiagnosticQuery {
	if !plExpressionCallback(err) {
		return plDiagnosticQuery{}
	}
	return plBodyDiagnostic(source, start, end, plExpressionCallbackKind)
}

func plCursorArgumentDiagnostic(program *proc.PLProgram, in *proc.PLCursorInstruction, raw *parser.PLCursorStatement) plDiagnosticQuery {
	named := false
	for _, actual := range raw.Arguments {
		named = named || actual.Name.Text != ""
	}
	var query strings.Builder
	positions := make([]plDiagnosticPosition, len(in.Arguments))
	characters := 0
	for i, argument := range in.Arguments {
		if i != 0 {
			query.WriteString(", ")
			characters += 2
		}
		positions[i] = plDiagnosticPosition{Start: argument.Start.Character, End: argument.End.Character, Output: characters + 1}
		text := parser.PLExpressionSource(program.Source, argument.Start, argument.End)
		query.WriteString(text)
		characters += utf8.RuneCountInString(text)
		if named {
			label := " AS " + catalog.DependencyIdentifier(program.Variables[argument.Slot].Name)
			query.WriteString(label)
			characters += utf8.RuneCountInString(label)
		}
	}
	return plDiagnosticQuery{Text: query.String(), Positions: positions, Callback: plExpressionCallbackKind}
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
	if err := s.op.Context().Err(); err != nil {
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
