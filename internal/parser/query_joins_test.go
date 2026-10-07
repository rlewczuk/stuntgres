package parser

import "testing"

func TestQueryJoinsTreeAndPrecedence(t *testing.T) {
	parse := func(q string) *Select {
		t.Helper()
		ss, err := Parse(q)
		if err != nil || len(ss) != 1 {
			t.Fatal(q, ss, err)
		}
		return ss[0].(*Select)
	}
	s := parse("SELECT a.*,b.k FROM a,b JOIN c ON b.k=c.k")
	comma := s.From.(*JoinReference)
	if comma.On != nil || comma.Left.(*TableReference).Name.Parts[0].Text != "a" || comma.Right.(*JoinReference).On == nil {
		t.Fatal(s.From)
	}
	if len(s.Targets) != 2 || s.Targets[0].(StarExpr).Qualifier.Parts[0].Text != "a" || len(s.Targets[1].(ColumnExpr).Name.Parts) != 2 {
		t.Fatal(s.Targets)
	}
	s = parse("SELECT * FROM a JOIN b USING(k) JOIN c USING(k)")
	chain := s.From.(*JoinReference)
	if _, ok := chain.Left.(*JoinReference); !ok {
		t.Fatal(chain)
	}
	s = parse("SELECT j.* FROM (a JOIN (b JOIN c USING(k)) USING(k) AS u) AS j(kk,ll)")
	alias := s.From.(*AliasReference)
	join := alias.Input.(*JoinReference)
	if alias.Name.Text != "j" || len(alias.Columns) != 2 || join.UsingAlias.Text != "u" {
		t.Fatal(alias, join)
	}
	if _, ok := join.Right.(*JoinReference); !ok {
		t.Fatal(join.Right)
	}
	for _, q := range []string{
		"SELECT * FROM a NATURAL JOIN b", "SELECT * FROM a NATURAL INNER JOIN b", "SELECT * FROM a CROSS JOIN b", "SELECT * FROM a INNER JOIN b ON NULL",
		"SELECT public.a.k FROM public.a", `SELECT "A".* FROM a AS "A"`, "INSERT INTO out SELECT a.k FROM a JOIN b USING(k)",
	} {
		if ss, err := Parse(q); err != nil || len(ss) != 1 {
			t.Fatal(q, ss, err)
		}
	}
	for _, tc := range []struct{ q, msg, pos string }{
		{"SELECT * FROM a JOIN b ON", "syntax error at end of input", "26"},
		{"SELECT * FROM a CROSS JOIN b ON TRUE", `syntax error at or near "ON"`, "30"},
		{"SELECT * FROM a NATURAL JOIN b ON TRUE", `syntax error at or near "ON"`, "32"},
		{"SELECT * FROM a JOIN b ON TRUE ON TRUE", `syntax error at or near "ON"`, "32"},
		{"SELECT * FROM a NATURAL JOIN b USING(k)", `syntax error at or near "USING"`, "32"},
		{"SELECT * FROM a JOIN b USING()", `syntax error at or near ")"`, "30"},
		{"SELECT * FROM a JOIN b USING(k) AS u(z)", `syntax error at or near "("`, "37"},
	} {
		keywordDiagnostic(t, tc.q, "42601", tc.msg, tc.pos)
	}
	for _, q := range []string{"SELECT * FROM a LEFT JOIN b ON TRUE", "SELECT * FROM a RIGHT JOIN b ON TRUE", "SELECT * FROM a FULL JOIN b ON TRUE", "SELECT * FROM a CROSS JOIN LATERAL b", "SELECT * FROM (SELECT * FROM a) x", "SELECT * FROM a AS x AS y"} {
		if ss, err := Parse(q); err == nil || len(ss) != 0 {
			t.Fatal("excluded FROM form escaped", q, ss, err)
		}
	}
}

func TestQueryJoinsKeywordTokens(t *testing.T) {
	for _, tc := range []struct {
		word     string
		token    int
		category keywordCategory
	}{
		{"cross", CROSS, TypeFunctionName}, {"inner", INNER, TypeFunctionName}, {"join", JOIN, TypeFunctionName}, {"natural", NATURAL, TypeFunctionName}, {"using", USING, Reserved},
	} {
		k := lookupKeyword(tc.word)
		if k.token != tc.token || k.category != tc.category {
			t.Fatal(tc.word, k)
		}
	}
}

func TestQueryJoinsRetainsMutationGrammar(t *testing.T) {
	for _, tc := range []struct{ q, code, message, pos string }{
		{"DELETE FROM t WHERE t.i+1=8", "42601", `syntax error at or near "+"`, "24"},
		{"UPDATE t SET a=t.b", "0A000", "qualified column references are unsupported", "17"},
		{"UPDATE t SET a=t.b::INT", "0A000", "qualified column references are unsupported", "17"},
		{"UPDATE t SET a=1 WHERE t.b=1", "42601", `syntax error at or near "."`, "25"},
		{"UPDATE t SET a=CAST(t.b AS INT)", "42601", `syntax error at or near "."`, "22"},
		{"UPDATE t SET a=*", "42601", `syntax error at or near "*"`, "16"},
	} {
		keywordDiagnostic(t, tc.q, tc.code, tc.message, tc.pos)
	}
	for _, q := range []string{"UPDATE t SET a=COALESCE(t.b,1)", "UPDATE t SET a=CAST(COALESCE(t.b,1) AS INT)"} {
		// Existing conditional qualification reaches the nonquery binder's
		// semantic guard; query admission must not blanket-reject it earlier.
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}

func TestQueryJoinsRetainsScalarInputGrammar(t *testing.T) {
	for _, tc := range []struct{ q, code, message, pos string }{
		{"CREATE TABLE bad(v INT DEFAULT a.k)", "42601", `syntax error at or near "."`, "33"},
		{"INSERT INTO a(k) VALUES(a.k)", "42601", `syntax error at or near "."`, "26"},
		{"SELECT current_setting(a.k)", "42601", `syntax error at or near "."`, "25"},
		{"INSERT INTO a(k) VALUES(COALESCE(a.k,1))", "0A000", "column references are unsupported in scalar inputs", "34"},
	} {
		keywordDiagnostic(t, tc.q, tc.code, tc.message, tc.pos)
	}
	// Conditional DEFAULT arguments keep their established parse admission;
	// utility Prepare describes them, while execution applies DEFAULT policy.
	q := "CREATE TABLE bad(v INT DEFAULT COALESCE(a.k,1))"
	if statements, err := Parse(q); err != nil || len(statements) != 1 {
		t.Fatal(q, statements, err)
	}
}

func TestQueryJoinsRetainsNoFromGrammar(t *testing.T) {
	for _, tc := range []struct{ q, code, message, pos string }{
		{"SELECT a.k", "0A000", "qualified column references are unsupported", "9"},
		{"SELECT a.*", "0A000", "qualified column references are unsupported", "9"},
		{"INSERT INTO a(k) SELECT a.k", "0A000", "qualified column references are unsupported", "26"},
		{"INSERT INTO a(k) SELECT a.*", "0A000", "qualified column references are unsupported", "26"},
		{"SELECT (a.k)", "42601", `syntax error at or near "."`, "10"},
		{"INSERT INTO a(k) SELECT (a.k)", "42601", `syntax error at or near "."`, "27"},
		{"SELECT num_nulls(a.k)", "42601", `syntax error at or near "."`, "19"},
		{"SELECT COALESCE(a.k,1)", "0A000", "SELECT without FROM is unsupported", "1"},
	} {
		keywordDiagnostic(t, tc.q, tc.code, tc.message, tc.pos)
	}
	for _, q := range []string{"SELECT num_nulls(NULL,1)", "INSERT INTO a(k) SELECT COALESCE($1,7)", "INSERT INTO zero SELECT", "INSERT INTO a(k) SELECT COALESCE(a.k,1)"} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}

func TestQueryJoinsAliasAndStarBoundaries(t *testing.T) {
	keywordDiagnostic(t, "SELECT * t", "42601", `syntax error at or near "t"`, "10")
	for _, q := range []string{"SELECT values.v FROM t values", "SELECT values.k FROM t values(k)", "SELECT values.v FROM t AS values", `SELECT "values".v FROM t "values"`} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}
