package parser

import "testing"

func TestUpdateCoreSyntax(t *testing.T) {
	for _, q := range []string{"UPDATE t SET a=b,b=a", "UPDATE public.t SET a=$1 WHERE NOT f OR a=b", "UPDATE \"t\" SET f=a=b", "UPDATE t SET a=num_nulls(a,b)", "UPDATE t SET update=1", "UPDATE t SET a=b+1", "UPDATE t SET a=a+b,b=a-b WHERE a+$1=$2", "UPDATE t SET a=(SELECT b FROM u)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if len(statements) != 1 {
			t.Fatal(statements)
		}
		u, ok := statements[0].(*Update)
		if !ok || len(u.Assignments) == 0 || u.Pos.Character != 1 || u.Assignments[0].Target.Pos.Character == 0 || u.Assignments[0].Value.SourcePosition().Character == 0 {
			t.Fatal(q, statements)
		}
	}
	statements, err := Parse("UPDATE t SET a=b,b=a")
	if err != nil {
		t.Fatal(err)
	}
	u := statements[0].(*Update)
	if u.Assignments[0].Target.Text != "a" || u.Assignments[1].Target.Text != "b" {
		t.Fatal(u)
	}
}

func TestUpdateCoreExcludedAndMalformed(t *testing.T) {
	for _, q := range []string{"UPDATE t SET (a,b)=(1,2)", "UPDATE t SET a=1 WHERE CURRENT OF c", "UPDATE t SET a=1 WHERE", "UPDATE t SET a=1 garbage", "UPDATE t SET a=1; garbage"} {
		statements, err := Parse(q)
		if err == nil || len(statements) != 0 {
			t.Fatalf("executed excluded/malformed input %s: %v %v", q, statements, err)
		}
	}
}
