package parser

import "testing"

func TestUpdateCurrentOfGrammar(t *testing.T) {
	for _, tc := range []struct{ q, name string }{
		{"UPDATE t SET a=1 WHERE CURRENT OF C", "c"},
		{`UPDATE t SET a=$1 FROM s WHERE CURRENT OF "MiX" RETURNING a`, "MiX"},
		{`UPDATE t SET a=1 WHERE CURRENT OF "é"`, "é"},
	} {
		statements, err := Parse(tc.q)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.q, statements, err)
		}
		u := statements[0].(*Update)
		if u.CurrentOf == nil || u.CurrentOf.Text != tc.name || u.CurrentOf.Pos.Character == 0 || u.Where != nil || len(u.Assignments) != 1 {
			t.Fatal("lost mutually exclusive positioned qualifier", u)
		}
	}
	statements, err := Parse("UPDATE t SET a=1 WHERE a=2")
	if err != nil {
		t.Fatal(err)
	}
	if u := statements[0].(*Update); u.CurrentOf != nil || u.Where == nil {
		t.Fatal("ordinary predicate changed", u)
	}
	for _, q := range []string{"UPDATE t SET a=1 WHERE CURRENT OF $1", "UPDATE t SET a=1 WHERE CURRENT OF c AND TRUE", "UPDATE t SET a=1 WHERE CURRENT OF c OR TRUE", "UPDATE t SET a=1 WHERE CURRENT OF public.c", "UPDATE t SET a=1 WHERE CURRENT OF c junk", "UPDATE t SET a=1 WHERE CURRENT OF c; junk"} {
		statements, err := Parse(q)
		if err == nil || len(statements) != 0 {
			t.Fatal("accepted malformed full input", q, statements, err)
		}
	}
}
