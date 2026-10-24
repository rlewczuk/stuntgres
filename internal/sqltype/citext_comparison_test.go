package sqltype

import (
	"context"
	"errors"
	"testing"
)

func citextComparisonBool(t *testing.T) *Descriptor {
	t.Helper()
	for _, d := range Builtins() {
		if d.ID() == Bool {
			return d
		}
	}
	t.Fatal("missing bool")
	return nil
}

func TestCitextComparisonValues(t *testing.T) {
	d, result := citextUnitType(t, 45000), citextComparisonBool(t)
	for _, tc := range []struct {
		a, b string
		sign int
	}{
		{"ABC", "abc", 0}, {"", "", 0}, {"ABC", "abd", -1},
		{"abd", "ABC", 1}, {"", " ", -1}, {"abc", "abcd", -1},
		{"abc", "abc ", -1}, {"É", "é", -1}, {"Σ", "σ", -1},
		{"İ", "i", 1}, {"Straße", "STRASSE", 1}, {"é", "e\u0301", 1},
	} {
		want := [6]bool{tc.sign == 0, tc.sign != 0, tc.sign < 0, tc.sign <= 0, tc.sign > 0, tc.sign >= 0}
		for _, collation := range []CollationRef{{ID: 100, Derivation: Default}, {ID: 950, Derivation: Default}, {ID: 951, Derivation: Default}, {ID: 811, Derivation: Default}, {ID: 6411, Derivation: Default}} {
			for op := CompareEqual; op <= CompareGreaterEqual; op++ {
				p, err := BindCitextComparison(op, d, d, result, collation, false)
				if err != nil {
					t.Fatal(err)
				}
				a, b := String(d, tc.a), String(d, tc.b)
				v, err := p.Evaluate(context.Background(), a, b)
				if err != nil || v != Boolean(result, want[op]) {
					t.Fatal(tc, collation, op, v, err)
				}
				if a.text != tc.a || b.text != tc.b {
					t.Fatal("mutated spelling")
				}
			}
		}
	}
}

func TestCitextComparisonNullAndCollation(t *testing.T) {
	d, result := citextUnitType(t, 45000), citextComparisonBool(t)
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		p, err := BindCitextComparison(op, d, d, result, CollationRef{}, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range [][2]Value{{Null(d), String(d, "ABC")}, {String(d, "abc"), Null(d)}, {Null(d), Null(d)}} {
			v, err := p.Evaluate(context.Background(), args[0], args[1])
			if err != nil || v != Null(result) {
				t.Fatal(op, v, err)
			}
		}
		v, err := p.Evaluate(context.Background(), String(d, "ABC"), String(d, "abc"))
		if op < CompareLess {
			if err != nil || v != Boolean(result, op == CompareEqual) {
				t.Fatal(v, err)
			}
		} else if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42P22" {
			t.Fatal(err)
		}
	}
}

func TestCitextComparisonIdentity(t *testing.T) {
	d, other, result := citextUnitType(t, 45000), citextUnitType(t, 45000), citextComparisonBool(t)
	if _, err := BindCitextComparison(CompareEqual, d, other, result, CollationRef{ID: 100, Derivation: Default}, false); err == nil {
		t.Fatal("foreign generation bound")
	}
	p, err := BindCitextComparison(CompareEqual, d, d, result, CollationRef{ID: 100, Derivation: Default}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []Value{String(other, "ABC"), Null(other)} {
		if _, err := p.Evaluate(context.Background(), String(d, "abc"), v); err == nil {
			t.Fatal("foreign value admitted")
		}
	}
	if _, err := BindComparison(d, d, CollationRef{ID: 100, Derivation: Default}, false); err == nil {
		t.Fatal("generic comparison invented")
	}
}

func TestCitextComparisonCancellation(t *testing.T) {
	d, result := citextUnitType(t, 45000), citextComparisonBool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		p, err := BindCitextComparison(op, d, d, result, CollationRef{ID: 100, Derivation: Default}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []Value{Null(d), String(d, "ABC")} {
			if _, err := p.Evaluate(ctx, v, v); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
