package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUpdateRowParser(t *testing.T) {
	for _, source := range []string{"(b,a)", "ROW(b,a)", "(SELECT x,y FROM s)", "ROW(DEFAULT,a)", "ROW(s.*)"} {
		q := "/*é*/ UPDATE t SET id=id+1,(a,b)=" + source + ",(v[1],p.x)=ROW(7,8) RETURNING a,b"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		u := statements[0].(*Update)
		if len(u.Assignments) != 3 || len(u.Assignments[0].Targets) != 0 || u.Assignments[0].Target.Text != "id" {
			t.Fatal(u.Assignments)
		}
		group := u.Assignments[1]
		if len(group.Targets) != 2 || group.Targets[0].Name != "a" || group.Targets[1].Name != "b" || group.Target.Text != "" {
			t.Fatal(group)
		}
		if group.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, "(a,b)")])+1 || group.Value.SourcePosition().Character != utf8.RuneCountInString(q[:strings.Index(q, source)])+1 {
			t.Fatal("lost character origins", group)
		}
		last := u.Assignments[2]
		if len(last.Targets[0].Steps) != 1 || last.Targets[0].Steps[0].Subscript == nil || len(last.Targets[1].Steps) != 1 || last.Targets[1].Steps[0].Field.Text != "x" {
			t.Fatal(last)
		}
	}
	for _, q := range []string{"UPDATE t SET (a,b)=", "UPDATE t SET (a,)=ROW(1,2)", "UPDATE t SET ()=ROW()", "UPDATE t SET (a,b)=(1,2) garbage", "UPDATE t SET (a,b)=(1,2); garbage", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=1,(a,b)=(1,2)", "INSERT INTO t VALUES(1) ON CONFLICT(id) DO UPDATE SET a=1,(a,b)=(1,2)"} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("accepted malformed/excluded input", q, statements, err)
		}
	}
	keywordDiagnostic(t, "UPDATE t SET (a,b)=(SELECT DEFAULT,1)", "42601", "DEFAULT is not allowed in this context", "28")
	keywordDiagnostic(t, "/*é*/ SELECT DEFAULT", "42601", "DEFAULT is not allowed in this context", "14")
}
