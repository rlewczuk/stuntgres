package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// assignPLRow takes ownership of fields when publishing an anonymous record.
// Named rows use the existing ordinal assignment conversion and retain their
// declared descriptor. The source descriptor is private to this open producer.
func (s *StatementScope) assignPLRow(f *proc.PLFrame, slot int, shape **sqltype.Descriptor, schema []catalog.Column, fields []sqltype.Value) error {
	if *shape == nil {
		members := make([]sqltype.CompositeMember, len(schema))
		for i, column := range schema {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: column.Name, Usage: column.Usage}
		}
		var err error
		*shape, err = sqltype.RecordShape(members)
		if err != nil {
			return err
		}
	}
	if len(fields) == 0 && len(schema) != 0 {
		fields = make([]sqltype.Value, len(schema))
		for i, column := range schema {
			fields[i] = sqltype.Null(column.Usage.Type)
		}
	}
	row, err := sqltype.OwnedCompositeValue(*shape, fields)
	if err != nil {
		return err
	}
	_, err = s.assignPLVariable(f, slot, row)
	return err
}

func (s *StatementScope) forPLQuery(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (value sqltype.Value, flow plFlow, err error) {
	iterated, consumed := false, false
	restore := s.activateSQLRoutine(nil, read)
	defer func() {
		err = restore(err)
		if err == nil && consumed {
			f.SetFound(iterated)
		}
	}()
	s.sqlRoutine.lexical, s.sqlRoutine.scalar, s.sqlRoutine.rows = f, false, true
	raw := in.Syntax.QueryLoop
	const action = "at FOR over SELECT rows"
	queryError := func(cause error, planning bool) error {
		diagnostic := plDiagnosticQuery{}
		if planning {
			diagnostic = plDiagnosticQuery{Text: parser.PLExpressionSource(f.Program.Source, raw.Start, raw.End), Offset: raw.Start.Character - 1, Callback: plSQLCallback}
		}
		return plRuntimeQueryError(cause, read, d, in.Syntax.Pos, action, diagnostic)
	}
	s.sqlRoutine.plPlanning = true
	if err = s.acquireRelation(in.QueryLoop.Query, true); err != nil {
		return value, flow, queryError(err, true)
	}
	description, err := s.engine().describeOperation(s, in.QueryLoop.Query)
	if err != nil {
		return value, flow, queryError(err, true)
	}
	s.Expected = &description
	if !rewriteTupleStatement(in.QueryLoop.Query) {
		return value, flow, queryError(sqlError("42P11", "cannot open "+routineCommandTag(in.QueryLoop.Query)+" query as cursor"), false)
	}
	finishCommand, err := s.beginRoutineCommand()
	if err != nil {
		return value, flow, err
	}
	result, executionErr := s.engine().executeStatement(s, in.QueryLoop.Query, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, f.Values[:f.Program.FormalCount])
		return nil
	})
	// DML and modifying WITH have completed eagerly. Settle their command before
	// any body SQL can observe the effects or create its own command identity.
	err = finishCommand(executionErr)
	defer func() {
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); err == nil {
				err = queryError(closeErr, false)
			}
		}
		for _, rows := range result.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = queryError(closeErr, false)
			}
		}
	}()
	if err != nil {
		return value, flow, queryError(err, s.sqlRoutine.plPlanning)
	}
	if result.Rows == nil {
		return value, flow, fmt.Errorf("stuntgres: query FOR has no row producer")
	}
	consumed = true
	return s.consumePLQuery(d, f, in, read, result.Rows, &iterated)
}

// consumePLQuery uses one width-sized demand buffer. Only published anonymous
// record values need detached backing; no result history or second result buffer.
func (s *StatementScope) consumePLQuery(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read, rows exec.RowResult, iterated *bool) (value sqltype.Value, flow plFlow, err error) {
	schema := rows.Schema()
	fields := make([]sqltype.Value, len(schema))
	var shape *sqltype.Descriptor
	owner := in.QueryLoop.Body.Control
	failure := func(cause error) error {
		return plRuntimeQueryError(cause, read, d, in.Syntax.Pos, "at FOR over SELECT rows", plDiagnosticQuery{})
	}
	for {
		if err = s.op.Context().Err(); err != nil {
			return value, plFlow{}, failure(err)
		}
		present, fetchErr := rows.Next(s.op.Context(), fields)
		if fetchErr != nil {
			return value, plFlow{}, failure(fetchErr)
		}
		if !present {
			if !*iterated {
				if err = s.assignPLRow(f, in.QueryLoop.Target.Slot, &shape, schema, nil); err != nil {
					return value, plFlow{}, failure(err)
				}
			}
			break
		}
		owned := fields
		if f.Program.Variables[in.QueryLoop.Target.Slot].Record {
			owned = slices.Clone(fields)
		}
		if err = s.assignPLRow(f, in.QueryLoop.Target.Slot, &shape, schema, owned); err != nil {
			return value, plFlow{}, failure(err)
		}
		*iterated = true
		value, flow, err = s.executePLBlock(d, f, in.QueryLoop.Body, read)
		if err != nil {
			return value, flow, err
		}
		if flow.kind == plNormal || flow.kind == plContinue && flow.target == owner {
			continue
		}
		if flow.kind == plExit && flow.target == owner {
			return value, plFlow{}, nil
		}
		return value, flow, nil
	}
	return value, plFlow{}, nil
}
