package functions

import (
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBitStringFunctionsSelection(t *testing.T) {
	types := formatUnitTypes()
	supplied := BinaryTypes{Bytea: types[sqltype.Bytea].Usage(), Bit: types[sqltype.Bit].Usage(), Integer: types[sqltype.Int4].Usage(), Bigint: types[sqltype.Int8].Usage(), Text: types[sqltype.Text].Usage()}
	for _, tc := range []struct {
		name       string
		ids        []sqltype.ID
		resolution Resolution
		family     sqltype.ID
		result     sqltype.ID
	}{
		{"bit_count", []sqltype.ID{sqltype.Bit}, Resolved, sqltype.Bit, sqltype.Int8},
		{"bit_count", []sqltype.ID{sqltype.VarBit}, Resolved, sqltype.Bit, sqltype.Int8},
		{"length", []sqltype.ID{sqltype.Bit}, Resolved, sqltype.Bit, sqltype.Int4},
		{"bit_length", []sqltype.ID{sqltype.VarBit}, Resolved, sqltype.Bit, sqltype.Int4},
		{"octet_length", []sqltype.ID{sqltype.Bit}, Resolved, sqltype.Bit, sqltype.Int4},
		{"substring", []sqltype.ID{sqltype.VarBit, sqltype.Int2}, Resolved, sqltype.Bit, sqltype.Bit},
		{"substring", []sqltype.ID{sqltype.Bit, 0, 0}, Resolved, sqltype.Bit, sqltype.Bit},
		{"substring", []sqltype.ID{sqltype.Bit, sqltype.Int8}, Missing, sqltype.Bit, 0},
		{"overlay", []sqltype.ID{sqltype.VarBit, sqltype.Bit, sqltype.Int4}, Resolved, sqltype.Bit, sqltype.Bit},
		{"overlay", []sqltype.ID{sqltype.Bit, 0, 0, 0}, Resolved, sqltype.Bit, sqltype.Bit},
		{"position", []sqltype.ID{sqltype.Bit, sqltype.VarBit}, Resolved, sqltype.Bit, sqltype.Int4},
		{"position", []sqltype.ID{0, sqltype.Bit}, Resolved, sqltype.Bit, sqltype.Int4},
	} {
		for _, defaults := range []bool{false, true} {
			args := make([]Argument[struct{}], len(tc.ids))
			for i, id := range tc.ids {
				if id != 0 {
					args[i].Usage = types[id].Usage()
					if id == sqltype.Bit || id == sqltype.VarBit {
						args[i].Usage.Typmod = 5
					}
				}
			}
			prepared := PrepareBinary(tc.name, true, false, defaults, args)
			selected := ResolveBinary(prepared, tc.name, args, &supplied)
			if selected.Resolution != tc.resolution || selected.Route != BinaryCall || selected.Family != tc.family {
				t.Fatal(tc, defaults, selected)
			}
			if tc.result != 0 && BinaryResultID(tc.name, selected.Family) != tc.result {
				t.Fatal(tc, selected)
			}
			demand := BinaryArgumentDemand(tc.name, selected.Family, args, false)
			if !demand.Needs(sqltype.Bit) || demand.Needs(sqltype.Bytea) {
				t.Fatal("wrong family demand", tc, demand)
			}
			if selected.Resolution == Resolved && !BinaryArgumentsViable(tc.name, selected.Family, args, &supplied) {
				t.Fatal("resolved but not viable", tc)
			}
		}
	}
	args := []Argument[struct{}]{{Usage: types[sqltype.Bit].Usage()}}
	for _, name := range []string{"reverse", "md5", "get_bit", "set_bit", "substr", "btrim", "||"} {
		if BinaryBitCandidate(name, false, args) {
			t.Fatal("adjacent candidate", name)
		}
	}
	if BinaryBitCandidate("length", true, args) {
		t.Fatal("variadic candidate")
	}
	unknown := []Argument[struct{}]{{Kind: DeferredNullArgument}}
	selection := ResolveBinary(PrepareBinary("bit_count", true, false, false, unknown), "bit_count", unknown, &supplied)
	if selection.Resolution != Ambiguous {
		t.Fatal(selection)
	}
}
