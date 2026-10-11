package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryJoinsProducts(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	product := [][]any{
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "y", int64(8), int64(3)},
		{"a", int64(1), int64(9), "n", int64(9), nil},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "x", int64(9), int64(1)},
		{"a", int64(1), int64(9), "y", int64(8), int64(3)},
		{"a", int64(1), int64(9), "n", int64(9), nil},
		{"n", nil, int64(9), "x", int64(9), int64(1)},
		{"n", nil, int64(9), "x", int64(9), int64(1)},
		{"n", nil, int64(9), "y", int64(8), int64(3)},
		{"n", nil, int64(9), "n", int64(9), nil},
		{"z", int64(2), int64(8), "x", int64(9), int64(1)},
		{"z", int64(2), int64(8), "x", int64(9), int64(1)},
		{"z", int64(2), int64(8), "y", int64(8), int64(3)},
		{"z", int64(2), int64(8), "n", int64(9), nil},
	}
	steps := []fixture.Step{
		fixture.Select("SELECT x.l,y.l FROM a x CROSS JOIN a y", []string{"l", "l"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}, {"a", "a"}, {"a", "n"}, {"a", "z"}, {"a", "a"}, {"a", "a"}, {"a", "n"}, {"a", "z"}, {"n", "a"}, {"n", "a"}, {"n", "n"}, {"n", "z"}, {"z", "a"}, {"z", "a"}, {"z", "n"}, {"z", "z"}}),
		fixture.Select("SELECT a.l FROM a CROSS JOIN b JOIN c ON a.k=c.k AND b.k=c.k", []string{"l"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}, {"a"}}),
		fixture.Select("SELECT * FROM a NATURAL JOIN b AS renamed(rr,qq,kk)", []string{"l", "k", "q", "rr", "qq", "kk"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, product),
	}
	for _, q := range []string{"SELECT * FROM a,b", "SELECT * FROM a CROSS JOIN b", "SELECT * FROM a INNER JOIN b ON TRUE"} {
		steps = append(steps, fixture.Select(q, []string{"l", "k", "q", "r", "q", "k"}, []string{"TEXT", "INT4", "INT4", "TEXT", "INT4", "INT4"}, product))
	}
	queryJoinsReferenceCases(t, p, steps)
}
