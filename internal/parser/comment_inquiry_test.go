package parser

import "testing"

func TestCommentInquiryRawCalls(t *testing.T) {
	for _, name := range []string{"col_description", "obj_description", "shobj_description"} {
		for _, args := range []string{"", "NULL", "$1,$2", "1::oid,'pg_class'::text", "VARIADIC NULL"} {
			q := "SELECT /*é*/ pg_catalog." + name + "(" + args + ")"
			ss, err := Parse(q)
			if err != nil || len(ss) != 1 {
				t.Fatal(q, ss, err)
			}
			s := ss[0].(*Select)
			f, ok := s.Targets[0].(FunctionExpr)
			if !ok || f.Name.Parts[1].Text != name || f.Pos.Character != 14 || s.From != nil || s.Values != nil {
				t.Fatal(q, f, s)
			}
		}
		ss, err := Parse("SELECT " + name + " FROM t")
		if err != nil {
			t.Fatal(err)
		}
		if ss[0].(*Select).Targets[0].(ColumnExpr).Name.Parts[0].Text != name {
			t.Fatal(ss)
		}
	}
}

func TestCommentInquiryGroupedCalls(t *testing.T) {
	ss, err := Parse("SELECT obj_description('t'::regclass,'pg_class'),count(*) FROM t GROUP BY obj_description('t'::regclass,'pg_class')")
	if err != nil || len(ss) != 1 {
		t.Fatal(ss, err)
	}
	s := ss[0].(*Select)
	if s.GroupBy == nil || len(s.GroupBy.Items) != 1 {
		t.Fatal(s.GroupBy)
	}
	call, ok := s.GroupBy.Items[0].Expr.(FunctionExpr)
	if !ok || call.Name.Parts[0].Text != "obj_description" || len(call.Args) != 2 {
		t.Fatal(s.GroupBy)
	}
}

func TestCommentInquiryCaseResults(t *testing.T) {
	ss, err := Parse("SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END FROM t")
	if err != nil || len(ss) != 1 {
		t.Fatal(ss, err)
	}
	e, ok := ss[0].(*Select).Targets[0].(CaseExpr)
	if !ok || len(e.Arms) != 1 {
		t.Fatal(ss)
	}
	if _, ok := e.Arms[0].Result.(FunctionExpr); !ok {
		t.Fatal(e.Arms)
	}
	if _, ok := e.Else.(FunctionExpr); !ok {
		t.Fatal(e.Else)
	}
}
