package proc

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// returnNext reads immutable frame storage without SQL evaluation or assignment
// conversion. Only the invocation owns the materialized set accumulator.
func (r *PLRun) returnNext(in PLInstruction) error {
	err := r.appendReturnNext(in)
	if err != nil {
		return PLRuntimeQueryError(err, r.Definition, r.Host.Identity, in.Syntax.Pos, "at RETURN NEXT", PLDiagnosticQuery{})
	}
	return nil
}

func (r *PLRun) appendReturnNext(in PLInstruction) error {
	if r.Values == nil {
		return fmt.Errorf("stuntgres: missing PL set result sink")
	}
	slot := in.ReturnNext.Slot
	value := r.Frame.Values[slot]
	source := value.Type()
	if source == nil || source.IsDeferredRecord() {
		err := plError("55000", fmt.Sprintf("record \"%s\" is not assigned yet", r.Frame.Program.Variables[slot].Name))
		err.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
		return err
	}
	source = source.BaseType()
	target := r.Definition.Result.Type.BaseType()
	if !source.IsComposite() || !target.IsComposite() {
		return fmt.Errorf("stuntgres: invalid checked RETURN NEXT row")
	}
	mismatch := func(detail string) error {
		err := plError("42804", "wrong record type supplied in RETURN NEXT")
		err.Detail = detail
		return err
	}
	// PostgreSQL compares overlapping positional usages before width.
	for i := 0; i < source.CompositeLen() && i < target.CompositeLen(); i++ {
		actual, expected := source.CompositeMember(i).Usage, target.CompositeMember(i)
		if actual.Type.ID() != expected.Usage.Type.ID() || expected.Usage.Typmod >= 0 && actual.Typmod != expected.Usage.Typmod {
			return mismatch(fmt.Sprintf("Returned type %s does not match expected type %s in column \"%s\" (position %d).", r.Host.ResultUsageName(actual), r.Host.ResultUsageName(expected.Usage), expected.Name, i+1))
		}
	}
	if source.CompositeLen() != target.CompositeLen() {
		return mismatch(fmt.Sprintf("Number of returned columns (%d) does not match expected column count (%d).", source.CompositeLen(), target.CompositeLen()))
	}
	if !value.IsNull() && value.Type() == r.Definition.Result.Type {
		*r.Values = append(*r.Values, value)
		return nil
	}
	fields := make([]sqltype.Value, target.CompositeLen())
	for i := range fields {
		typ := target.CompositeMember(i).Usage.Type
		if value.IsNull() {
			fields[i] = sqltype.Null(typ)
		} else {
			field, err := value.CompositeField(i).Retag(typ)
			if err != nil {
				return err
			}
			fields[i] = field
		}
	}
	row, err := sqltype.OwnedCompositeValue(r.Definition.Result.Type, fields)
	if err != nil {
		return err
	}
	*r.Values = append(*r.Values, row)
	return nil
}
