package proc

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLRunReturnNextShape(t *testing.T) {
	d, err := returnNextUnit(t, "DECLARE r record; BEGIN RETURN NEXT r; END", true)
	if err != nil {
		t.Fatal(err)
	}
	in := d.PL.Root.Statements[0].Block.Statements[0]
	host := newWalkHost(t)
	frame := NewPLFrame(d.PL, d, &Frame{}, sqltype.CollationRef{})
	var values []sqltype.Value
	run := &PLRun{Host: host, Definition: d, Frame: frame, Values: &values}
	integer := plUnitInteger(t)
	bigint := plUnitBuiltin(t, sqltype.Int8)
	for _, tc := range []struct {
		name    string
		members []sqltype.CompositeMember
		detail  string
	}{
		{"aliases", []sqltype.CompositeMember{{Attribute: 1, Name: "other", Usage: integer.Usage()}}, ""},
		{"width", []sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: integer.Usage()}, {Attribute: 2, Name: "b", Usage: integer.Usage()}}, "Number of returned columns (2) does not match expected column count (1)."},
		{"type_before_width", []sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: bigint.Usage()}, {Attribute: 2, Name: "b", Usage: integer.Usage()}}, "Returned type bigint does not match expected type integer in column \"id\" (position 1)."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape, err := sqltype.RecordShape(tc.members)
			if err != nil {
				t.Fatal(err)
			}
			fields := make([]sqltype.Value, len(tc.members))
			for i, m := range tc.members {
				fields[i], err = sqltype.Integer(m.Usage.Type, 9)
				if err != nil {
					t.Fatal(err)
				}
			}
			frame.Values[in.ReturnNext.Slot], err = sqltype.OwnedCompositeValue(shape, fields)
			if err != nil {
				t.Fatal(err)
			}
			before := len(values)
			_, _, err = run.Statements([]PLInstruction{in})
			if tc.detail == "" {
				if err != nil || len(values) != before+1 || values[before].Type() != d.Result.Type {
					t.Fatal("compatible RECORD not projected", err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Message != "wrong record type supplied in RETURN NEXT" || diagnostic.Detail != tc.detail || len(values) != before {
					t.Fatal("wrong shape diagnostic/publication", err)
				}
			}
		})
	}
	frame.Values[in.ReturnNext.Slot] = sqltype.Value{}
	_, _, err = run.Statements([]PLInstruction{in})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != "record \"r\" is not assigned yet" || diagnostic.Detail != "The tuple structure of a not-yet-assigned record is indeterminate." {
		t.Fatal(err)
	}
}
