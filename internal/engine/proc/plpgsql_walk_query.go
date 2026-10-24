package proc

import (
	"slices"

	"stuntgres/internal/sqltype"
)

// PLQueryLoopAction is the query-FOR statement action in diagnostics and notices.
const PLQueryLoopAction = "at FOR over SELECT rows"

// forQuery publishes FOUND only after the producer closed successfully.
func (r *PLRun) forQuery(in PLInstruction) (value sqltype.Value, flow PLFlow, err error) {
	rows, err := r.Host.OpenQuery(r.Frame, in)
	if err != nil {
		return value, flow, err
	}
	iterated := false
	value, flow, err = r.consumeQuery(in, rows, &iterated)
	if err = rows.Close(err); err == nil {
		r.Frame.SetFound(iterated)
	}
	return value, flow, err
}

// consumeQuery uses one width-sized demand buffer. Only published anonymous
// record values need detached backing; no result history or second result buffer.
func (r *PLRun) consumeQuery(in PLInstruction, rows PLRows, iterated *bool) (value sqltype.Value, flow PLFlow, err error) {
	f := r.Frame
	fields := make([]sqltype.Value, rows.Width())
	var shape *sqltype.Descriptor
	owner := in.QueryLoop.Body.Control
	slot := in.QueryLoop.Target.Slot
	at := PLOrigin{Position: in.Syntax.Pos, Action: PLQueryLoopAction}
	failure := func(cause error) error {
		return PLRuntimeQueryError(cause, r.Definition, r.Host.Identity, in.Syntax.Pos, PLQueryLoopAction, PLDiagnosticQuery{})
	}
	for {
		ctx := r.Host.Context()
		if err = ctx.Err(); err != nil {
			return value, PLFlow{}, failure(err)
		}
		present, fetchErr := rows.Next(ctx, fields)
		if fetchErr != nil {
			return value, PLFlow{}, failure(fetchErr)
		}
		if !present {
			if !*iterated {
				if err = r.AssignRow(slot, &shape, rows, nil, at); err != nil {
					return value, PLFlow{}, failure(err)
				}
			}
			break
		}
		owned := fields
		if f.Program.Variables[slot].Record {
			owned = slices.Clone(fields)
		}
		if err = r.AssignRow(slot, &shape, rows, owned, at); err != nil {
			return value, PLFlow{}, failure(err)
		}
		*iterated = true
		value, flow, err = r.Block(in.QueryLoop.Body)
		if err != nil {
			return value, flow, err
		}
		if flow.Kind == PLNormal || flow.Kind == PLContinue && flow.Target == owner {
			continue
		}
		if flow.Kind == PLExit && flow.Target == owner {
			return value, PLFlow{}, nil
		}
		return value, flow, nil
	}
	return value, PLFlow{}, nil
}

// AssignRow takes ownership of fields when publishing an anonymous record.
// Named rows use the ordinal assignment conversion and keep their declared
// descriptor. *shape caches the producer's record descriptor across rows; nil
// fields assign a row of typed NULLs.
func (r *PLRun) AssignRow(slot int, shape **sqltype.Descriptor, columns PLColumns, fields []sqltype.Value, at PLOrigin) error {
	width := columns.Width()
	if *shape == nil {
		members := make([]sqltype.CompositeMember, width)
		for i := range members {
			name, usage := columns.Column(i)
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: usage}
		}
		var err error
		*shape, err = sqltype.RecordShape(members)
		if err != nil {
			return err
		}
	}
	if len(fields) == 0 && width != 0 {
		fields = make([]sqltype.Value, width)
		for i := range fields {
			_, usage := columns.Column(i)
			fields[i] = sqltype.Null(usage.Type)
		}
	}
	row, err := sqltype.OwnedCompositeValue(*shape, fields)
	if err != nil {
		return err
	}
	_, err = r.AssignVariable(slot, row, at)
	return err
}
