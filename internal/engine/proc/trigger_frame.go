package proc

import (
	"fmt"
	"strings"

	"stuntgres/internal/sqltype"
)

// PLTriggerRelation is the firing relation, resolved by the caller.
type PLTriggerRelation struct {
	Shape     *sqltype.Descriptor // admitted table or logical view composite row type
	OID       uint32
	Name      string
	Namespace string
}

// NewPLTriggerFrame owns one invocation. OLD and NEW are both typed records even
// when their tuple is absent; no table shape is written into the shared program.
func NewPLTriggerFrame(d *Definition, rel PLTriggerRelation, name, event, timing, level string, args []string, old, next []sqltype.Value) (*PLFrame, error) {
	if d == nil || d.Invocation != DMLTriggerInvocation || d.PL == nil || d.PL.Invocation != DMLTriggerInvocation {
		return nil, fmt.Errorf("stuntgres: missing checked trigger program")
	}
	shape := rel.Shape
	if shape == nil || !shape.IsComposite() {
		return nil, fmt.Errorf("stuntgres: missing trigger relation row type")
	}
	f := NewPLFrame(d.PL, d, &Frame{}, sqltype.CollationRef{})
	fail := func(err error) (*PLFrame, error) { f.Close(); return nil, err }
	for i, row := range [2][]sqltype.Value{old, next} {
		if row == nil {
			f.Values[i] = sqltype.Null(shape)
		} else {
			// CompositeValue detaches the producer's scratch row.
			value, err := sqltype.CompositeValue(shape, row)
			if err != nil {
				return fail(err)
			}
			f.Values[i] = value
		}
	}
	for _, entry := range [...]struct {
		slot int
		text string
	}{{2, name}, {3, timing}, {4, level}, {5, strings.ToUpper(event)}, {7, rel.Name}, {8, rel.Namespace}, {9, rel.Name}} {
		f.Values[entry.slot] = sqltype.String(f.Program.Variables[entry.slot].Usage.Type, entry.text)
	}
	var err error
	f.Values[6], err = sqltype.OIDValue(f.Program.Variables[6].Usage.Type, rel.OID)
	if err != nil {
		return fail(err)
	}
	f.Values[10], err = sqltype.Integer(f.Program.Variables[10].Usage.Type, int64(len(args)))
	if err != nil {
		return fail(err)
	}
	array := f.Program.Variables[11].Usage.Type
	f.Values[11] = sqltype.Null(array)
	if len(args) != 0 {
		values := make([]sqltype.Value, len(args))
		for i, arg := range args {
			values[i] = sqltype.String(array.Element(), arg)
		}
		f.Values[11], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(args)), Lower: 0}}, values)
		if err != nil {
			return fail(err)
		}
	}
	return f, nil
}
