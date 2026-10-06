package sqltype

import (
	"context"
	"testing"
)

// Numeric OID expectations assert catalog identity independently of provider names.
func TestCharacterDescriptorsModifiersAndCodecs(t *testing.T) {
	for _, tc := range []struct {
		id                        ID
		name, database, unlimited string
	}{{ID(1042), "bpchar", "BPCHAR", "BPCHAR"}, {ID(1043), "varchar", "VARCHAR", "VARCHAR"}} {
		t.Run(tc.name, func(t *testing.T) {
			var d *Descriptor
			for _, candidate := range Builtins() {
				if candidate.ID() == tc.id {
					d = candidate
					break
				}
			}
			if d == nil {
				t.Fatalf("missing builtin %s OID %d", tc.name, tc.id)
			}
			if d.Name() != tc.name || d.DatabaseName() != tc.database {
				t.Fatal(d)
			}
			u := d.Usage()
			if !u.Valid() || u.Typmod != -1 || u.Collation != (CollationRef{ID: 100, Derivation: Default}) {
				t.Fatal(u)
			}
			declaration, err := u.Declaration()
			if err != nil || declaration != tc.unlimited {
				t.Fatal(declaration, err)
			}
			for _, n := range []int32{1, 2, 10485760} {
				sized, warning, err := d.ResolveModifiers(n)
				if err != nil || warning != nil || !sized.Valid() || sized.Typmod != n+4 {
					t.Fatal(n, sized, warning, err)
				}
			}
			ctx := context.Background()
			for _, text := range []string{"", "é🙂", "a  ", "O'Brien\n\\tail"} {
				v, err := d.Input(ctx, text, -1)
				if err != nil {
					t.Fatal(err)
				}
				got, err := d.Output(ctx, v)
				if err != nil || got != text {
					t.Fatal(got, text, err)
				}
				if _, err = d.SQLLiteral(ctx, v); err != nil {
					t.Fatal(err)
				}
			}
			v, err := d.Input(ctx, "é🙂", 7)
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.Output(ctx, v)
			want := "é🙂"
			if tc.id == ID(1042) {
				want += " "
			}
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		})
	}
}

func TestCharacterSizingContextsConversionsAndComparisons(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, id := range []ID{BPChar, VarChar} {
		d := types[id]
		u, _, err := d.ResolveModifiers(2)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			in, want        string
			explicit, fails bool
		}{
			{"é🙂", "é🙂", false, false},
			{"e\u0301", "e\u0301", false, false},
			{"é🙂   ", "é🙂", false, false},
			{"é🙂x", "", false, true},
			{"é🙂\t", "", false, true},
			{"é🙂\u00a0", "", false, true},
			{"é🙂x", "é🙂", true, false},
			{"a", "a", false, false},
			{"", "", true, false},
		} {
			mode := Assignment
			if tc.explicit {
				mode = Explicit
			}
			plan, err := PlanCoercion(d.Usage(), u, mode)
			if err != nil {
				t.Fatal(err)
			}
			v, err := plan.Apply(ctx, String(d, tc.in))
			if tc.fails {
				numericProviderError(t, err, "22001", "value too long for type "+map[ID]string{BPChar: "character(2)", VarChar: "character varying(2)"}[id])
				continue
			}
			want := tc.want
			if id == BPChar && want == "a" {
				want = "a "
			}
			if id == BPChar && want == "" {
				want = "  "
			}
			got, e := v.Text()
			if err != nil || e != nil || got != want {
				t.Fatal(id, tc, got, err, e)
			}
			null, e := plan.Apply(ctx, Null(d))
			if e != nil || !null.IsNull() || null.Type() != d {
				t.Fatal(null, e)
			}
		}
		n, ok := u.Length()
		if !ok || n != 2 {
			t.Fatal(n, ok)
		}
	}
	for _, target := range []ID{Text, VarChar, BPChar} {
		p, err := PlanCoercion(types[BPChar].Usage(), types[target].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Apply(ctx, String(types[BPChar], "a   "))
		got, e := v.Text()
		want := "a"
		if target == BPChar {
			want = "a   "
		}
		if err != nil || e != nil || got != want {
			t.Fatal(target, got, err, e)
		}
	}
	for _, pair := range []struct {
		left, right ID
		want        int
	}{
		{BPChar, BPChar, 0}, {BPChar, VarChar, 0}, {VarChar, BPChar, 0},
		{BPChar, Text, -1}, {Text, BPChar, 1}, {VarChar, Text, 0},
	} {
		p, err := BindComparison(types[pair.left], types[pair.right])
		if err != nil {
			t.Fatal(err)
		}
		a, b := "a ", "a "
		if pair.left == BPChar {
			a = "a   "
		}
		if pair.right == BPChar {
			b = "a   "
		}
		got, err := p.Compare(ctx, String(types[pair.left], a), String(types[pair.right], b))
		if err != nil || got != pair.want {
			t.Fatal(pair, got, err)
		}
	}
	if _, err := PlanCoercion(types[Int4].Usage(), types[VarChar].Usage(), Implicit); err == nil {
		t.Fatal("assignment IO became implicit")
	}
	for _, character := range []ID{BPChar, VarChar} {
		for _, baseline := range []ID{Int4, Int8, Bool} {
			for _, pair := range [][2]ID{{baseline, character}, {character, baseline}} {
				if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
					t.Fatal("nonstring character cast admitted", pair)
				}
			}
		}
	}
}
