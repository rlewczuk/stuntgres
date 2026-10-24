package proc

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

const PLForeachAction = "at FOREACH over array"

func (r *PLRun) foreach(in PLInstruction) (value sqltype.Value, flow PLFlow, err error) {
	loop, raw := in.Foreach, in.Syntax.Foreach
	at := PLOrigin{Position: in.Syntax.Pos, Action: PLForeachAction}
	source, _, err := r.Host.Evaluate(r.Frame, loop.Query, at)
	if err != nil {
		return value, flow, PLRuntimeError(err, r.Definition, r.Host.Identity, raw.Source.Start, raw.Source.End, in.Syntax.Pos, PLForeachAction, true)
	}
	failure := func(cause error) (sqltype.Value, PLFlow, error) {
		return sqltype.Value{}, PLFlow{}, PLRuntimeError(cause, r.Definition, r.Host.Identity, raw.Source.Start, raw.Source.End, in.Syntax.Pos, PLForeachAction, false)
	}
	if source.IsNull() {
		return failure(plError("22004", "FOREACH expression must not be null"))
	}
	if !source.Type().IsArray() {
		return failure(plError("42804", "FOREACH expression must yield an array, not type "+r.Host.TypeName(source.Type())))
	}
	rank := source.ArrayRank()
	if loop.Slice > rank {
		return failure(plError("2202E", fmt.Sprintf("slice dimension (%d) is out of the valid range 0..%d", loop.Slice, rank)))
	}
	isArray := false
	if len(loop.Targets) == 1 {
		target := loop.Targets[0]
		usage := r.Frame.Usage(target.Slot)
		if len(target.Steps) != 0 {
			usage, err = r.Host.TargetUsage(r.Frame, target)
			if err != nil {
				return failure(err)
			}
		}
		isArray = usage.Type.IsArray()
	}
	if loop.Slice > 0 && !isArray {
		return failure(plError("42804", "FOREACH ... SLICE loop variable must be of an array type"))
	}
	if loop.Slice == 0 && isArray {
		return failure(plError("42804", "FOREACH loop variable must not be of an array type"))
	}
	size := 1
	for i := rank - loop.Slice; i < rank; i++ {
		size *= int(source.ArrayDimension(i).Length)
	}
	iterated := false
	for offset := 0; offset < source.ArrayLen(); offset += size {
		if err := r.Host.Context().Err(); err != nil {
			return failure(err)
		}
		item := source.ArrayElement(offset)
		if loop.Slice > 0 {
			item = source.ArrayForeachSlice(loop.Slice, offset, size)
		}
		if err := r.assignForeach(loop.Targets, item, at); err != nil {
			return failure(err)
		}
		iterated = true
		value, flow, err = r.Block(loop.Body)
		if err != nil {
			// Body errors already carry their own statement action.
			return value, flow, err
		}
		if flow.Kind == PLNormal || flow.Kind == PLContinue && flow.Target == loop.Body.Control {
			continue
		}
		if flow.Kind == PLExit && flow.Target == loop.Body.Control {
			flow = PLFlow{}
		}
		r.Frame.SetFound(iterated)
		return value, flow, nil
	}
	r.Frame.SetFound(iterated)
	return value, PLFlow{}, nil
}

func (r *PLRun) assignForeach(targets []PLTarget, item sqltype.Value, at PLOrigin) error {
	if len(targets) == 1 {
		return r.assignForeachTarget(targets[0], item, at)
	}
	shape := item.Type().BaseType()
	if !shape.IsComposite() {
		return plError("42804", "cannot assign non-composite value to a row variable")
	}
	for i, target := range targets {
		// Absent columns and NULL composite elements still deliver NULLs.
		field := sqltype.Null(item.Type())
		if i < shape.CompositeLen() {
			field = sqltype.Null(shape.CompositeMember(i).Usage.Type)
			if !item.IsNull() {
				field = item.CompositeField(i)
			}
		}
		if err := r.assignForeachTarget(target, field, at); err != nil {
			return err
		}
	}
	return nil
}

func (r *PLRun) assignForeachTarget(target PLTarget, value sqltype.Value, at PLOrigin) error {
	if len(target.Steps) != 0 {
		return r.Host.AssignValue(r.Frame, target, value, at)
	}
	_, err := r.AssignVariable(target.Slot, value, at)
	return err
}
