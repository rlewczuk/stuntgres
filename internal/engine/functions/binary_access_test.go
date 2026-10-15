package functions

import (
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryAccessSignatures(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		name       string
		ids        []sqltype.ID
		resolution Resolution
		route      BinaryRoute
		result     sqltype.ID
	}{
		{"bit_count", []sqltype.ID{sqltype.Bytea}, Resolved, BinaryCall, sqltype.Int8},
		{"bit_count", []sqltype.ID{sqltype.Bit}, Resolved, BinaryCall, sqltype.Int8},
		{"bit_count", []sqltype.ID{sqltype.VarBit}, Resolved, BinaryCall, sqltype.Int8},
		{"bit_count", []sqltype.ID{0}, Ambiguous, BinaryCall, 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int4}, Resolved, BinaryCall, sqltype.Int4},
		{"get_bit", []sqltype.ID{0, sqltype.Int2}, Ambiguous, BinaryCall, 0},
		{"get_bit", []sqltype.ID{0, sqltype.Int8}, Resolved, BinaryCall, sqltype.Int4},
		{"get_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int8}, Missing, BinaryCall, 0},
		{"set_bit", []sqltype.ID{sqltype.Bytea, sqltype.Int4, sqltype.Int8}, Missing, BinaryCall, 0},
		{"set_byte", []sqltype.ID{sqltype.Bytea, sqltype.Int2, sqltype.Int2}, Resolved, BinaryCall, sqltype.Bytea},
		{"get_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4}, Resolved, BinaryCall, sqltype.Int4},
		{"get_bit", []sqltype.ID{sqltype.VarBit, sqltype.Int2}, Resolved, BinaryCall, sqltype.Int4},
		{"get_bit", []sqltype.ID{sqltype.Bit, sqltype.Int8}, Missing, BinaryCall, 0},
		{"set_bit", []sqltype.ID{0, sqltype.Int4, sqltype.Int4}, Resolved, BinaryCall, sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4, sqltype.Int4}, Resolved, BinaryCall, sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.VarBit, sqltype.Int2, sqltype.Int2}, Resolved, BinaryCall, sqltype.Bit},
		{"set_bit", []sqltype.ID{sqltype.Bit, sqltype.Int8, sqltype.Int4}, Missing, BinaryCall, 0},
		{"set_bit", []sqltype.ID{sqltype.Bit, sqltype.Int4, sqltype.Int8}, Missing, BinaryCall, 0},
	} {
		arguments := make([]Argument[struct{}], len(tc.ids))
		for i, id := range tc.ids {
			if id == 0 {
				arguments[i].Kind = DeferredNullArgument
			} else {
				arguments[i].Usage = types[id].Usage()
			}
		}
		prepared := PrepareBinary(tc.name, true, false, false, arguments)
		var supplied BinaryTypes
		if prepared.Demand.Needs(sqltype.Bytea) {
			supplied.Bytea = types[sqltype.Bytea].Usage()
		}
		if prepared.Demand.Needs(sqltype.Bit) {
			supplied.Bit = types[sqltype.Bit].Usage()
		}
		if prepared.Demand.Needs(sqltype.Int4) {
			supplied.Integer = types[sqltype.Int4].Usage()
		}
		if prepared.Demand.Needs(sqltype.Int8) {
			supplied.Bigint = types[sqltype.Int8].Usage()
		}
		if prepared.Demand.Needs(sqltype.Name) {
			supplied.Name = types[sqltype.Name].Usage()
		}
		if prepared.Demand.Needs(sqltype.Text) {
			supplied.Text = types[sqltype.Text].Usage()
		}
		selected := ResolveBinary(prepared, tc.name, arguments, &supplied)
		if selected.Resolution != tc.resolution || selected.Route != tc.route {
			t.Fatal(tc, selected)
		}
		if tc.result != 0 && BinaryResultID(tc.name, selected.Family) != tc.result {
			t.Fatal(tc, BinaryResultID(tc.name, selected.Family))
		}
		if selected.Resolution == Resolved && selected.Route == BinaryCall && (tc.name == "get_bit" || tc.name == "set_bit") {
			want := sqltype.Int8
			if selected.Family == sqltype.Bit {
				want = sqltype.Int4
			}
			if BinaryArgumentID(tc.name, 1, selected.Family) != want {
				t.Fatal(tc, "index formal", want)
			}
		}
	}
}
