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

func TestBitStringOperatorsConcatSelection(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		left, right sqltype.ID
		family      sqltype.ID
		textual     [2]bool
	}{
		{sqltype.Bit, sqltype.Bit, sqltype.VarBit, [2]bool{}},
		{sqltype.Bit, sqltype.VarBit, sqltype.VarBit, [2]bool{}},
		{sqltype.VarBit, sqltype.Bit, sqltype.VarBit, [2]bool{}},
		{sqltype.VarBit, sqltype.VarBit, sqltype.VarBit, [2]bool{}},
		{sqltype.Bit, 0, sqltype.VarBit, [2]bool{}},
		{0, sqltype.Bit, sqltype.VarBit, [2]bool{}},
		{sqltype.VarBit, 0, sqltype.VarBit, [2]bool{}},
		{0, sqltype.VarBit, sqltype.VarBit, [2]bool{}},
		{sqltype.Bit, sqltype.Text, 0, [2]bool{false, true}},
		{sqltype.Text, sqltype.Bit, 0, [2]bool{true, false}},
		{0, 0, 0, [2]bool{true, true}},
		{sqltype.Bytea, sqltype.Bytea, sqltype.Bytea, [2]bool{}},
		{sqltype.Bytea, 0, sqltype.Bytea, [2]bool{}},
	} {
		operands := [2]*sqltype.Descriptor{types[tc.left], types[tc.right]}
		textual, family, resolution := SelectStringConcat(true, operands)
		if resolution != Resolved || family != tc.family || textual != tc.textual {
			t.Fatal(tc, textual, family, resolution)
		}
		if _, _, resolution := SelectStringConcat(false, operands); resolution != Missing {
			t.Fatal("invisible concat", tc, resolution)
		}
	}
	supplied := BinaryTypes{VarBit: types[sqltype.VarBit].Usage()}
	if supplied.Usage(sqltype.VarBit) != types[sqltype.VarBit].Usage() ||
		!BinaryResultDemand("||", sqltype.VarBit).Needs(sqltype.VarBit) {
		t.Fatal("VARBIT demand lost")
	}
	args := []Argument[struct{}]{{Usage: types[sqltype.Bit].Usage()}, {Usage: types[sqltype.VarBit].Usage()}}
	if !BinaryArgumentDemand("||", sqltype.VarBit, args, false).Needs(sqltype.VarBit) ||
		!BinaryArgumentsViable("||", sqltype.VarBit, args, &supplied) {
		t.Fatal("VARBIT concat coercion not viable")
	}
}
