package parser

import "testing"

func TestConditionalGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT CASE WHEN b THEN 1 ELSE 2 END+3,CASE v WHEN 1 THEN TRUE ELSE FALSE END FROM t",
		"SELECT COALESCE(v,1),NULLIF(v,0),GREATEST(v,2),LEAST(v,2) FROM t",
		"SELECT CASE WHEN b AND v=1 THEN v>0 ELSE FALSE END FROM t",
		"SELECT num_nulls(COALESCE(v,1)),int4(COALESCE(s,'1')) FROM t",
		"INSERT INTO t VALUES(CASE WHEN TRUE THEN 1 ELSE 2 END,COALESCE(NULL,3))",
		"UPDATE t SET v=CASE WHEN b THEN 1 ELSE 2 END WHERE COALESCE(b,FALSE)",
		"DELETE FROM t WHERE CASE t.v WHEN 1 THEN TRUE ELSE FALSE END",
		"CREATE TABLE t(v INTEGER DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,1) ELSE 2 END)",
		"CREATE TABLE coalesce(nullif INT,greatest INT,least INT)",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	statements, err := Parse("SELECT /*é*/ CASE v WHEN 1 THEN COALESCE(NULL,2) ELSE 3 END FROM t")
	if err != nil {
		t.Fatal(err)
	}
	node, ok := statements[0].(*Select).Targets[0].(CaseExpr)
	if !ok || node.Pos.Character != 14 || node.Arms[0].Pos.Character != 21 || ExpressionStart(node).Character != 14 {
		t.Fatal(node)
	}
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT COALESCE() FROM t", `syntax error at or near ")"`, "17"},
		{"SELECT GREATEST() FROM t", `syntax error at or near ")"`, "17"},
		{"SELECT LEAST() FROM t", `syntax error at or near ")"`, "14"},
		{"SELECT NULLIF(1) FROM t", `syntax error at or near ")"`, "16"},
		{"SELECT NULLIF(1,2,3) FROM t", `syntax error at or near ","`, "18"},
		{"SELECT CASE END FROM t", `syntax error at or near "END"`, "13"},
		{"SELECT CASE WHEN TRUE 1 END FROM t", `syntax error at or near "1"`, "23"},
		{"SELECT CASE WHEN TRUE THEN 1 FROM t", `syntax error at or near "FROM"`, "30"},
		{"SELECT CASE WHEN TRUE THEN 1", `syntax error at end of input`, "29"},
		{"SELECT COALESCE(VARIADIC v) FROM t", `syntax error at or near "VARIADIC"`, "17"},
	} {
		keywordDiagnostic(t, tc.q, "42601", tc.message, tc.pos)
	}
	for _, q := range []string{"INSERT INTO t VALUES(COALESCE(NULL,v))", "INSERT INTO t VALUES(CASE WHEN TRUE THEN 1 ELSE v END)", "SELECT CASE WHEN TRUE THEN 1 END", "SELECT COALESCE(VARIADIC v) FROM t"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("invalid admission", q)
		}
	}
}
