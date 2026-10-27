package core

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type plDynamicResult struct {
	columns    []catalog.Column
	first      []sqltype.Value
	count      int   // Saturated at two for ordinary INTO.
	processed  int64 // Complete primary processed count, independent of INTO demand.
	descriptor bool
}

// Dynamic borrows the main binder/executor with a private typed USING frame.
// Neither the lexical frame nor computed syntax escapes this reached command.
func (h *plHost) Dynamic(run *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction) (err error) {
	s := h.s
	raw, checked := in.Syntax.Dynamic, in.Dynamic
	action := "at EXECUTE"
	if checked.ReturnQuery {
		action = "at RETURN QUERY"
	}
	at := proc.PLOrigin{Position: in.Syntax.Pos, Action: action}
	previous := h.setOrigin(at)
	defer func() { h.live.origin = previous }()
	origin := s.plNoticeOrigin(f, in.Syntax.Pos, action)
	noticeStart := len(s.Op.Notices)
	defer func() { s.appendPLNoticeContext(noticeStart, origin) }()
	var diagnostic proc.PLDiagnosticQuery
	failure := func(cause error) error {
		return s.plRuntimeQueryError(cause, h.read, h.d, in.Syntax.Pos, action, diagnostic)
	}
	value, _, _, err := s.evaluatePLExpression(f, checked.Command, h.read, nil, plNoticeOrigin{})
	if err != nil {
		diagnostic = proc.PLCursorExpressionDiagnostic(err, f.Program.Source, raw.Command.Start, raw.Command.End)
		return failure(err)
	}
	if value.IsNull() {
		return failure(SQLError("22004", "query string argument of EXECUTE is null"))
	}
	command, err := value.Type().OutputWithEnvironment(h.Context(), value, s.Output)
	if err != nil {
		return failure(err)
	}
	parameters := make([]sqltype.Value, len(checked.Using))
	inputs := make([]sqltype.Usage, len(checked.Using))
	for i, query := range checked.Using {
		value, usage, _, e := s.evaluatePLExpression(f, query, h.read, nil, plNoticeOrigin{})
		if e != nil {
			expression := raw.Using[i]
			diagnostic = proc.PLCursorExpressionDiagnostic(e, f.Program.Source, expression.Start, expression.End)
			return failure(e)
		}
		parameters[i], inputs[i] = value, usage
	}
	diagnostic = proc.PLDiagnosticQuery{Text: command, Callback: proc.PLSQLCallback}
	statements, err := parser.ParseWithOptions(command, parser.Options{StandardConformingStrings: !s.Op.NonstandardStrings, Context: h.Context(), Notice: s.Op.ParseNotice})
	if err != nil {
		return failure(err)
	}
	var final plDynamicResult
	sinkStart := 0
	if checked.ReturnQuery && run.Values != nil {
		sinkStart = len(*run.Values)
	}
	for i, statement := range statements {
		if _, transaction := statement.(*parser.TransactionControl); transaction {
			// SPI reports unsupported transaction execution after its SQL
			// error callback has been removed; only the PL frame remains.
			diagnostic = proc.PLDiagnosticQuery{}
		}
		capture := !checked.ReturnQuery && len(checked.Targets) != 0 && i == len(statements)-1
		final, err = h.dynamicCommand(run, statement, command, inputs, parameters, capture, checked.ReturnQuery)
		if err != nil {
			return failure(err)
		}
	}
	if checked.ReturnQuery {
		if len(statements) == 0 {
			return failure(SQLError("42601", "empty query does not return tuples"))
		}
		processed := int64(len(*run.Values) - sinkStart)
		f.SetRowCount(processed)
		f.SetFound(processed != 0)
		return nil
	}
	// SPI callbacks end with SQL execution, not cardinality/target publication.
	diagnostic = proc.PLDiagnosticQuery{}
	f.SetRowCount(final.processed)
	if len(checked.Targets) == 0 {
		return nil
	}
	if !final.descriptor {
		return failure(SQLError("42601", "INTO used with a command that cannot return data"))
	}
	if checked.Strict && final.count != 1 {
		if final.count == 0 {
			return failure(SQLError("P0002", "query returned no rows"))
		}
		return failure(SQLError("P0003", "query returned more than one row"))
	}
	if len(checked.Targets) == 1 {
		target := checked.Targets[0]
		variable := f.Program.Variables[target.Slot]
		if len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite()) {
			var shape *sqltype.Descriptor
			columns := plColumns(final.columns)
			return failure(run.AssignRow(target.Slot, &shape, &columns, final.first, at))
		}
	}
	for i, target := range checked.Targets {
		value := sqltype.Null(f.Program.Variables[f.Program.FoundSlot].Usage.Type)
		if i < len(final.first) {
			value = final.first[i]
		}
		candidate, e := s.preparePLTargetValue(run, f, target, value, f.Values[target.Slot], h.read)
		if e != nil {
			return failure(e)
		}
		f.Values[target.Slot] = candidate
	}
	return nil
}

func (h *plHost) dynamicCommand(run *proc.PLRun, statement parser.Statement, command string, inputs []sqltype.Usage, parameters []sqltype.Value, capture, set bool) (out plDynamicResult, err error) {
	s := h.s
	if _, ok := statement.(*parser.TransactionControl); ok {
		return out, SQLError("0A000", "EXECUTE of transaction commands is not implemented")
	}
	live := routineLive{parent: s.routineLive, query: command}
	s.routineLive = &live
	defer func() { s.routineLive = live.parent; live = routineLive{} }()
	previousAtomic := s.plAtomic
	s.plAtomic = true
	defer func() { s.plAtomic = previousAtomic }()
	identity := s.sqlRoutine.plDiagnostic
	restore := s.ActivateSQLRoutine(inputs, h.read)
	s.sqlRoutine.scalar, s.sqlRoutine.rows, s.sqlRoutine.plPlanning = false, true, true
	s.sqlRoutine.closedParameters = true
	s.sqlRoutine.plDiagnostic = identity
	var result Result
	var finishCommand, finishEvent func(error) error
	defer func() {
		err = closePLCapture(result, err, func(e error) error { return e })
		if finishEvent != nil {
			err = finishEvent(err)
		}
		if finishCommand != nil {
			err = finishCommand(err)
		}
		err = restore(err)
	}()
	if err = s.parallelExecutionEligibility(statement); err != nil {
		return out, err
	}
	finishCommand, err = s.BeginRoutineCommand()
	if err != nil {
		return out, err
	}
	if err = s.routineUtilityEligibility(statement); err != nil {
		return out, err
	}
	if tag, eligible := session.EventCommand(statement); eligible {
		if err = s.readOnlyUtilityEligibility(statement); err != nil {
			return out, err
		}
		if err = s.executionEligibility(statement); err != nil {
			return out, err
		}
		previous, e := s.BeginEventCommand(tag)
		if e != nil {
			return out, e
		}
		finishEvent = func(primary error) error { return s.FinishEventCommand(previous, primary) }
	}
	if err = s.AcquireRelation(statement, true); err != nil {
		return out, err
	}
	description, e := s.Engine().describeOperation(s, statement)
	if e != nil {
		return out, e
	}
	s.Expected = &description
	result, err = s.Engine().executeStatement(s, statement, description.Parameters.Count, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, parameters)
		return nil
	})
	if err != nil {
		return out, err
	}
	consume := func(rows exec.RowResult) error {
		out.descriptor = true
		out.columns = rows.Schema()
		if capture {
			out.columns = slices.Clone(out.columns)
		}
		if set {
			if run.Values == nil {
				return fmt.Errorf("stuntgres: missing PL set result sink")
			}
			if e := h.checkDynamicSetShape(run.Definition, out.columns); e != nil {
				return e
			}
		}
		return drainPLDynamic(h.Context(), rows, capture, func(row []sqltype.Value) error {
			if set {
				value := row[0]
				if run.Definition.Result.Type.BaseType().IsComposite() {
					var e error
					value, e = sqltype.OwnedCompositeValue(run.Definition.Result.Type, slices.Clone(row))
					if e != nil {
						return e
					}
				}
				*run.Values = append(*run.Values, value)
			}
			return nil
		}, &out)
	}
	if result.Rows != nil {
		if err = consume(result.Rows); err != nil {
			return out, err
		}
	}
	primaryProcessed := out.processed
	for _, rows := range result.RowSets {
		if err = consume(rows); err != nil {
			return out, err
		}
	}
	if _, selecting := dml.StatementCTERoot(statement).(*parser.Select); selecting {
		out.processed = primaryProcessed
	} else {
		out.processed = result.Count
	}
	if set && !out.descriptor {
		return out, SQLError("42601", "query does not return tuples")
	}
	return out, nil
}

// Complete all projections before STRICT or target assignment. Retain the full
// count separately from the detached first tuple and saturated INTO cardinality.
func drainPLDynamic(ctx context.Context, rows exec.RowResult, capture bool, appendRow func([]sqltype.Value) error, out *plDynamicResult) error {
	scratch := make([]sqltype.Value, len(rows.Schema()))
	if capture {
		out.first = make([]sqltype.Value, len(scratch))
		for i, column := range rows.Schema() {
			out.first[i] = sqltype.Null(column.Usage.Type)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		present, err := rows.Next(ctx, scratch)
		if err != nil {
			return err
		}
		if !present {
			return ctx.Err()
		}
		if capture && out.count == 0 {
			copy(out.first, scratch)
		}
		if out.count < 2 {
			out.count++
		}
		out.processed++
		if err := appendRow(scratch); err != nil {
			return err
		}
	}
}

func (h *plHost) checkDynamicSetShape(d *proc.Definition, columns []catalog.Column) error {
	shape := d.Result.Type.BaseType()
	width := 1
	if shape.IsComposite() {
		width = shape.CompositeLen()
	}
	mismatch := func(detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "structure of query does not match function result type", Detail: detail}
	}
	if len(columns) != width {
		return mismatch(fmt.Sprintf("Number of returned columns (%d) does not match expected column count (%d).", len(columns), width))
	}
	for i, column := range columns {
		usage, name := d.Result, ""
		if shape.IsComposite() {
			member := shape.CompositeMember(i)
			usage, name = member.Usage, member.Name
		}
		if column.Usage.Type.ID() != usage.Type.ID() || usage.Typmod >= 0 && column.Usage.Typmod != usage.Typmod {
			return mismatch(fmt.Sprintf("Returned type %s does not match expected type %s in column %q (position %d).", resultUsageName(column.Usage, h.TypeName), resultUsageName(usage, h.TypeName), name, i+1))
		}
	}
	return nil
}
