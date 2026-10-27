package core

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Command borrows the existing statement journal and ordinary binder/executor.
// Its activation owns all producers and reached binding scratch, never a root
// completion, connection, transaction or independently selected procedure.
func (h *plHost) Command(run *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction) (err error) {
	s := h.s
	raw, checked := in.Syntax.SQL, in.SQL
	identity := s.sqlRoutine.plDiagnostic
	restore := s.ActivateSQLRoutine(nil, h.read)
	s.sqlRoutine.scalar, s.sqlRoutine.rows = false, true
	s.sqlRoutine.plDiagnostic = identity
	statement := checked.Query
	lexical := true
	switch statement.(type) {
	case *parser.Set, *parser.Reset, *parser.Show:
		// Settings are utility spelling/value syntax, not PL expression positions.
		lexical = false
	}
	if lexical {
		s.sqlRoutine.lexical = f
	}
	action := plIntoAction
	if checked.Kind == parser.PLPerformSQL {
		action = "at PERFORM"
	}
	if checked.Kind == parser.PLCallSQL {
		action = "at CALL"
	}
	origin := s.plNoticeOrigin(f, in.Syntax.Pos, action)
	noticeStart := len(s.Op.Notices)
	text := strings.TrimRight(raw.Text, " \t\r\n")
	if checked.Kind == parser.PLPerformSQL {
		text = "SELECT" + text[len("perform"):]
	}
	diagnostic := proc.PLDiagnosticQuery{Text: text, Offset: raw.Start.Character - 1, Callback: proc.PLSQLCallback}
	if checked.Kind == parser.PLPerformSQL {
		diagnostic.Offset++
	}
	failure := func(cause error) error {
		return s.plRuntimeQueryError(cause, h.read, h.d, in.Syntax.Pos, action, diagnostic)
	}
	runtimeFailure := func(cause error) error {
		return s.plRuntimeQueryError(cause, h.read, h.d, in.Syntax.Pos, action, proc.PLDiagnosticQuery{})
	}
	var result Result
	defer func() {
		err = closePLCapture(result, err, failure)
		s.appendPLNoticeContext(noticeStart, origin)
		err = restore(err)
	}()
	s.sqlRoutine.plPlanning = true
	if err = s.parallelExecutionEligibility(statement); err != nil {
		return failure(err)
	}
	finishCommand, e := s.BeginRoutineCommand()
	if e != nil {
		return e
	}
	defer func() {
		if finishCommand != nil {
			err = finishCommand(err)
		}
	}()
	if err = s.routineUtilityEligibility(statement); err != nil {
		return runtimeFailure(err)
	}
	var finishEvent func(error) error
	defer func() {
		if finishEvent != nil {
			err = finishEvent(err)
		}
	}()
	if tag, eligible := session.EventCommand(statement); eligible {
		if err = s.readOnlyUtilityEligibility(statement); err != nil {
			return failure(err)
		}
		if err = s.executionEligibility(statement); err != nil {
			return failure(err)
		}
		previous, e := s.BeginEventCommand(tag)
		if e != nil {
			return failure(e)
		}
		finishEvent = func(primary error) error { return s.FinishEventCommand(previous, primary) }
	}
	if err = s.AcquireRelation(statement, true); err != nil {
		return failure(err)
	}
	description, e := s.Engine().describeOperation(s, statement)
	if e != nil {
		return failure(e)
	}
	s.Expected = &description
	var outputs []analyze.PLCallOutput
	if checked.Kind == parser.PLCallSQL {
		if s.sqlRoutine.plCallBinding == nil {
			return fmt.Errorf("stuntgres: missing selected PL CALL binding")
		}
		outputs = s.sqlRoutine.plCallBinding.PLCallOutputs
		if e = validatePLCallOutputs(f, outputs); e != nil {
			return runtimeFailure(e)
		}
	}
	count := 0
	if lexical {
		count = f.Program.FormalCount
	}
	result, e = s.Engine().executeStatement(s, statement, count, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		for i := range dst {
			dst[i] = f.Parameter(i)
		}
		return nil
	})
	if e != nil {
		return failure(e)
	}
	present := false
	var returned []sqltype.Value
	if result.Rows != nil {
		if checked.Kind == parser.PLCallSQL {
			returned, present, e = consumePLCall(h.Context(), result.Rows)
		} else {
			present, e = drainPLSQL(h.Context(), result.Rows)
		}
		if e != nil {
			return failure(e)
		}
	}
	for _, rows := range result.RowSets {
		_, e := drainPLSQL(h.Context(), rows)
		if e != nil {
			return failure(e)
		}
	}
	hasDescriptor := result.Rows != nil || len(result.RowSets) != 0
	closeErr := closePLCapture(result, nil, failure)
	result.Rows, result.RowSets = nil, nil
	if closeErr != nil {
		return closeErr
	}
	if finishEvent != nil {
		complete := finishEvent
		finishEvent = nil
		if e = complete(nil); e != nil {
			return failure(e)
		}
	}
	complete := finishCommand
	finishCommand = nil
	if e = complete(nil); e != nil {
		return failure(e)
	}
	switch checked.Kind {
	case parser.PLPerformSQL:
		f.SetFound(present)
	case parser.PLCallSQL:
		if len(outputs) != 0 && (!present || len(returned) != len(outputs)) {
			return runtimeFailure(SQLError("XX000", "procedure returned no output row"))
		}
		at := proc.PLOrigin{Position: in.Syntax.Pos, Action: action}
		for i, output := range outputs {
			// Publish each successful assignment immediately. Repeated/overlapping
			// roots reread the current slot; a failed individual candidate publishes
			// nothing and leaves preceding writes intact for an exception handler.
			if e := h.AssignValue(f, output.Target, returned[i], at); e != nil {
				return runtimeFailure(e)
			}
		}
	default:
		if plFoundSQL(statement) {
			f.SetFound(result.Count != 0)
		}
		if hasDescriptor {
			diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "query has no destination for result data"}
			if _, selecting := dml.StatementCTERoot(statement).(*parser.Select); selecting {
				diagnostic.Hint = "If you want to discard the results of a SELECT, use PERFORM instead."
			}
			return runtimeFailure(diagnostic)
		}
	}
	return nil
}

func plFoundSQL(statement parser.Statement) bool {
	switch dml.StatementCTERoot(statement).(type) {
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

func validatePLCallOutputs(f *proc.PLFrame, outputs []analyze.PLCallOutput) error {
	for _, output := range outputs {
		if output.Target.Slot < 0 {
			name := fmt.Sprintf("%d", output.Formal)
			if output.Name != "" {
				name = fmt.Sprintf("%q", output.Name)
			}
			return SQLError("42601", "procedure parameter "+name+" is an output parameter but corresponding argument is not writable")
		}
		variable := f.Program.Variables[output.Target.Slot]
		if variable.Constant {
			return SQLError("22005", fmt.Sprintf("variable %q is declared CONSTANT", variable.Name))
		}
	}
	return nil
}

// One reusable row buffer drains every projection, including late failures.
// No scalar/capture demand and no history of discarded rows is retained.
func drainPLSQL(ctx context.Context, rows exec.RowResult) (bool, error) {
	row := make([]sqltype.Value, len(rows.Schema()))
	found := false
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		present, err := rows.Next(ctx, row)
		if err != nil {
			return false, err
		}
		if !present {
			return found, ctx.Err()
		}
		found = true
	}
}

func consumePLCall(ctx context.Context, rows exec.RowResult) ([]sqltype.Value, bool, error) {
	row := make([]sqltype.Value, len(rows.Schema()))
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	present, err := rows.Next(ctx, row)
	if err != nil || !present {
		return nil, present, err
	}
	// executeBound's common CALL handler emits exactly one already detached
	// tuple, not a lazy SELECT projection. Its producer closes before copyback.
	return row, true, ctx.Err()
}
