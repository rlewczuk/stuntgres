package proc

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// TriggerReturn validates positional row compatibility without assignment
// conversion, NULL filling, truncation or member coercion.
func TriggerReturn(shape *sqltype.Descriptor, value sqltype.Value) (sqltype.Value, error) {
	if shape == nil || !shape.IsComposite() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid trigger result row type")
	}
	if value.IsNull() {
		return sqltype.Null(shape), nil
	}
	failure := func(message, detail string) (sqltype.Value, error) {
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: message, Detail: detail}
	}
	if value.Type() == nil || !value.Type().BaseType().IsComposite() {
		return failure("cannot return non-composite value from function returning composite type", "")
	}
	const mismatch = "returned row structure does not match the structure of the triggering table"
	if value.CompositeLen() != shape.CompositeLen() {
		return failure(mismatch, fmt.Sprintf("Number of returned columns (%d) does not match expected column count (%d).", value.CompositeLen(), shape.CompositeLen()))
	}
	for i := range shape.CompositeLen() {
		got, want := value.Type().BaseType().CompositeMember(i).Usage, shape.CompositeMember(i).Usage
		if got.Type.ID() != want.Type.ID() || want.Typmod >= 0 && got.Typmod != want.Typmod {
			return failure(mismatch, fmt.Sprintf("Returned type %s does not match expected type %s in column %q (position %d).", got.Type.DiagnosticName(), want.Type.DiagnosticName(), shape.CompositeMember(i).Name, i+1))
		}
	}
	if value.Type() == shape {
		return value, nil
	}
	fields := make([]sqltype.Value, shape.CompositeLen())
	for i := range fields {
		fields[i] = value.CompositeField(i)
	}
	return sqltype.OwnedCompositeValue(shape, fields)
}
