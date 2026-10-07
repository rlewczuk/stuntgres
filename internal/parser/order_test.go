package parser

import "testing"

func orderParse(t *testing.T, query string) *Select {
	t.Helper()
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(query, statements, err)
	}
	return statements[0].(*Select)
}

func TestOrderKeysAndLabels(t *testing.T) {
	s := orderParse(t, `SELECT id,a+b AS k,a "Mixed",b AS order FROM t ORDER BY k DESC NULLS LAST,t.b+0 ASC NULLS FIRST,1`)
	if len(s.Targets) != 4 || len(s.Labels) != 4 || s.Labels[1].Text != "k" || s.Labels[2].Text != "Mixed" || s.Labels[3].Text != "order" {
		t.Fatal(s)
	}
	keys := s.Order.Keys
	if len(keys) != 3 || !keys[0].Desc || keys[0].Nulls != NullsLast || keys[1].Desc || keys[1].Nulls != NullsFirst || keys[2].Nulls != NullsDefault {
		t.Fatal(keys)
	}
	for _, query := range []string{"SELECT x FROM t ORDER BY x NULLS FIRST", "SELECT x FROM t ORDER BY x DESC NULLS LAST", "SELECT x FROM t ORDER BY x NULLS /*é*/ FIRST", `SELECT x FROM t ORDER BY U&"\0078" NULLS FIRST`, `SELECT x FROM t ORDER BY U&"\0078" DESC NULLS LAST`} {
		orderParse(t, query)
	}
	for _, query := range []string{"SELECT * AS x FROM t", "SELECT * x FROM t"} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal(query, statements, err)
		}
	}
}

func orderLabel(t *testing.T, query, label string) {
	t.Helper()
	s := orderParse(t, query)
	if len(s.Labels) != 1 || s.Labels[0].Text != label {
		t.Fatal(query, s.Labels)
	}
}

func TestOrderScalarLabels(t *testing.T) {
	for _, tc := range []struct{ query, label string }{
		{"SELECT i::int4 AS renamed FROM t ORDER BY renamed", "renamed"},
		{"SELECT CAST(i AS int4) cast_value FROM t ORDER BY cast_value", "cast_value"},
		{"SELECT i<j AS x FROM t ORDER BY x", "x"},
		{"SELECT i<j x FROM t ORDER BY x", "x"},
		{"SELECT v IS NULL AS result FROM t ORDER BY result", "result"},
		{"SELECT v IS NULL result FROM t ORDER BY result", "result"},
		{"SELECT a AND a AS result FROM t ORDER BY a AND a", "result"},
		{"SELECT a AND a result FROM t ORDER BY result", "result"},
		{"SELECT num_nulls(NULL) AS n FROM t ORDER BY n", "n"},
		{"SELECT v BETWEEN 1 AND 3 AS result FROM t ORDER BY v BETWEEN 1 AND 3", "result"},
		{"SELECT timestamp time FROM t ORDER BY time", "time"},
		{"SELECT timestamp zone FROM t ORDER BY zone", "zone"},
	} {
		orderLabel(t, tc.query, tc.label)
	}
}
