package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const plIntoAction = "at SQL statement"

func (h *plHost) Capture(run *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction) (err error) {
	s := h.s
	raw, checked := in.Syntax.Into, in.Into
	diagnosticIdentity := s.sqlRoutine.plDiagnostic
	restore := s.activateSQLRoutine(nil, h.read)
	s.sqlRoutine.lexical, s.sqlRoutine.scalar, s.sqlRoutine.rows = f, false, true
	s.sqlRoutine.plDiagnostic = diagnosticIdentity
	var result Result
	origin := s.plNoticeOrigin(f, in.Syntax.Pos, plIntoAction)
	noticeStart := len(s.Op.Notices)
	diagnostic := proc.PLDiagnosticQuery{Text: raw.Text, Offset: raw.Start.Character - 1, Callback: proc.PLSQLCallback}
	failure := func(cause error) error {
		return s.plRuntimeQueryError(cause, h.read, h.d, in.Syntax.Pos, plIntoAction, diagnostic)
	}
	cardinality := func(many bool) error {
		e := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0002", Message: "query returned no rows"}
		if many {
			e.Code = "P0003"
			e.Message = "query returned more than one row"
			e.Hint = "Make sure the query returns a single row, or use LIMIT 1."
		}
		return s.plRuntimeQueryError(e, h.read, h.d, in.Syntax.Pos, plIntoAction, proc.PLDiagnosticQuery{})
	}
	defer func() {
		err = closePLCapture(result, err, failure)
		s.appendPLNoticeContext(noticeStart, origin)
		err = restore(err)
	}()
	s.sqlRoutine.plPlanning = true
	if err = s.AcquireRelation(checked.Query, true); err != nil {
		return failure(err)
	}
	description, e := s.Engine().describeOperation(s, checked.Query)
	if e != nil {
		return failure(e)
	}
	s.Expected = &description
	root := dml.StatementCTERoot(checked.Query)
	mutation := parser.ModifyingStatement(root)
	s.sqlRoutine.plCapture = !mutation
	if mutation {
		capture := &dml.CaptureCardinality{}
		if len(s.mutations) == 0 {
			s.Command.Capture = capture
		} else {
			index := s.mutationIndex(root)
			if index < 0 {
				return fmt.Errorf("stuntgres: missing capture command")
			}
			if index == s.ActiveMutation {
				s.Command.Capture = capture
			} else {
				s.mutations[index].Command.Capture = capture
			}
		}
	}
	finish, e := s.beginRoutineCommand()
	if e != nil {
		return e
	}
	result, e = s.Engine().executeStatement(s, checked.Query, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, f.Values[:f.Program.FormalCount])
		return nil
	})
	e = finish(e)
	if e != nil {
		var excess *dml.CaptureCardinality
		if errors.As(e, &excess) {
			f.SetFound(true)
			return cardinality(true)
		}
		return failure(e)
	}
	if result.Rows == nil {
		return s.plRuntimeQueryError(SQLError("42601", "INTO used with a command that cannot return data"), h.read, h.d, in.Syntax.Pos, plIntoAction, proc.PLDiagnosticQuery{})
	}
	schema := result.Rows.Schema()
	fields, present, more, e := demandPLCapture(h.Context(), result.Rows, plColumns(schema), checked.Strict || mutation)
	if e != nil {
		return failure(e)
	}
	f.SetFound(present)
	if more {
		return cardinality(true)
	}
	if !present && checked.Strict {
		return cardinality(false)
	}
	at := proc.PLOrigin{Position: in.Syntax.Pos, Action: plIntoAction}
	if len(checked.Targets) == 1 {
		target := checked.Targets[0]
		variable := f.Program.Variables[target.Slot]
		if len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite()) {
			var shape *sqltype.Descriptor
			columns := plColumns(schema)
			if e := run.AssignRow(target.Slot, &shape, &columns, fields, at); e != nil {
				return proc.PLRuntimeQueryError(e, run.Definition, h.Identity, in.Syntax.Pos, plIntoAction, proc.PLDiagnosticQuery{})
			}
			return nil
		}
	}
	for i, target := range checked.Targets {
		value := sqltype.Null(f.Program.Variables[f.Program.FoundSlot].Usage.Type)
		if i < len(fields) {
			value = fields[i]
		}
		candidate, e := s.preparePLTargetValue(run, f, target, value, f.Values[target.Slot], h.read)
		if e != nil {
			return proc.PLRuntimeQueryError(e, run.Definition, h.Identity, in.Syntax.Pos, plIntoAction, proc.PLDiagnosticQuery{})
		}
		f.Values[target.Slot] = candidate
	}
	return nil
}

type plCaptureRows interface {
	Next(context.Context, []sqltype.Value) (bool, error)
}

// demandPLCapture retains only the first row; a failing second projection
// precedes cardinality publication. The caller owns producer close/activation.
func demandPLCapture(ctx context.Context, rows plCaptureRows, columns plColumns, exact bool) ([]sqltype.Value, bool, bool, error) {
	fields := make([]sqltype.Value, len(columns))
	if err := ctx.Err(); err != nil {
		return nil, false, false, err
	}
	present, err := rows.Next(ctx, fields)
	if err != nil {
		return nil, false, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, false, err
	}
	if !present {
		for i := range fields {
			fields[i] = sqltype.Null(columns[i].Usage.Type)
		}
		return fields, false, false, nil
	}
	if !exact {
		return fields, true, false, nil
	}
	second := make([]sqltype.Value, len(columns))
	more, err := rows.Next(ctx, second)
	if err != nil {
		return nil, false, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, false, err
	}
	return fields, true, more, nil
}

// Preserve both the primary diagnostic and every cleanup cause. The activation
// owner restores its scratch only after all producers have been closed.
func closePLCapture(result Result, primary error, format func(error) error) error {
	closeFailure := func(closeErr error) {
		if closeErr == nil {
			return
		}
		if primary == nil {
			primary = format(closeErr)
			return
		}
		var diagnostic *sqlstate.Error
		if errors.As(primary, &diagnostic) {
			primary = sqlstate.WithDiagnostic(errors.Join(primary, closeErr), diagnostic)
		} else {
			primary = errors.Join(primary, closeErr)
		}
	}
	if result.Rows != nil {
		closeFailure(result.Rows.Close())
	}
	for _, rows := range result.RowSets {
		closeFailure(rows.Close())
	}
	return primary
}
