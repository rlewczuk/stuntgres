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
	for _, q := range []string{"SELECT * FROM a CROSS JOIN LATERAL b", "SELECT * FROM a AS x AS y"} {
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
	} {
		keywordDiagnostic(t, tc.q, tc.code, tc.message, tc.pos)
	}
	for _, q := range []string{"UPDATE t SET a=*", "UPDATE t SET a = *"} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("invalid mutation assignment admitted", q, statements, err)
		}
	}
	for _, q := range []string{"UPDATE t SET a=t.b", "UPDATE t SET a=t.b::INT", "UPDATE t SET a=1 WHERE t.b=1", "UPDATE t SET a=CAST(t.b AS INT)", "UPDATE t SET a=COALESCE(t.b,1)", "UPDATE t SET a=CAST(COALESCE(t.b,1) AS INT)"} {
		// UPDATE qualifications now reach the shared target/source namespace.
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
	// Names are analyzed by the shared query namespace, not grammar capability gates.
	for _, q := range []string{"SELECT a.k", "SELECT a.*", "INSERT INTO a(k) SELECT a.k", "INSERT INTO a(k) SELECT a.*", "SELECT (a.k)", "INSERT INTO a(k) SELECT (a.k)", "SELECT num_nulls(a.k)", "SELECT COALESCE(a.k,1)", "SELECT num_nulls(NULL,1)", "INSERT INTO a(k) SELECT COALESCE($1,7)", "INSERT INTO zero SELECT", "INSERT INTO a(k) SELECT COALESCE(a.k,1)"} {
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

func TestQueryDerivedSubquerySyntax(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM (SELECT * FROM a) x",
		"SELECT x.v FROM (SELECT 1) x(v)",
		"SELECT * FROM (SELECT 1 AS v)",
		"SELECT EXISTS(SELECT 1),1 IN (SELECT 1),1 NOT IN (SELECT 1)",
		"SELECT 1 = ANY(SELECT 1),1 < SOME(SELECT 2),1 <= ALL(SELECT 2)",
		"SELECT ROW(1,2) = (SELECT 1,2),(1,2) IN (SELECT 1,2)",
		"SELECT (SELECT 1) WHERE EXISTS(SELECT 1)",
		"SELECT v FROM r WHERE v IN (SELECT v FROM r)",
		"SELECT v FROM r WHERE v NOT IN (SELECT v FROM r)",
		"SELECT v FROM r WHERE NOT v IN (SELECT v FROM r)",
		"SELECT ROW(1) FROM r OFFSET 1 ROW FETCH FIRST ROW ONLY",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}

func TestQuerySubqueryAST(t *testing.T) {
	parse := func(sql string) *Select {
		t.Helper()
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
		return statements[0].(*Select)
	}
	for _, tc := range []struct {
		sql      string
		kind     SubqueryKind
		negate   bool
		position int
	}{
		{"SELECT (SELECT 1)", SubqueryScalar, false, 8},
		{"SELECT EXISTS(SELECT)", SubqueryExists, false, 8},
		{"SELECT 1 IN (SELECT 1)", SubqueryAny, false, 10},
		{"SELECT 1 NOT IN (SELECT 1)", SubqueryAny, true, 10},
		{"SELECT /*é*/ 1 NOT IN (SELECT 1)", SubqueryAny, true, 16},
		{"SELECT 1 = SOME(SELECT 1)", SubqueryAny, false, 10},
		{"SELECT 1 <= ALL(SELECT 1)", SubqueryAll, false, 10},
	} {
		s := parse(tc.sql)
		expr, ok := s.Targets[0].(SubqueryExpr)
		if !ok || expr.Kind != tc.kind || expr.Negate != tc.negate || expr.Pos.Character != tc.position || expr.Query == nil || expr.Query.From != nil {
			t.Fatal(tc.sql, expr)
		}
	}
	s := parse("SELECT ROW(1,2) = (SELECT 1,2),(1,2) IN (SELECT 1,2)")
	operator := s.Targets[0].(OperatorExpr)
	if operator.Name.Parts[0].Text != "=" || len(operator.Args) != 2 || !operator.Args[0].(RowExpr).Explicit || operator.Args[1].(SubqueryExpr).Kind != SubqueryScalar {
		t.Fatal(operator)
	}
	quantified := s.Targets[1].(SubqueryExpr)
	row := quantified.Left.(RowExpr)
	if row.Explicit || len(row.Fields) != 2 {
		t.Fatal(row)
	}
	s = parse("SELECT d.x FROM (SELECT (SELECT 1) AS x) d(x)")
	alias := s.From.(*AliasReference)
	derived := alias.Input.(*DerivedQuery)
	if alias.Name.Text != "d" || len(alias.Columns) != 1 || derived.Query.From != nil || derived.Pos.Character != 17 {
		t.Fatal(alias, derived)
	}
}

func TestQuerySubquerySourceWalk(t *testing.T) {
	statements, err := Parse("SELECT (SELECT 1 FROM target_source) FROM outer_source,(SELECT 1 FROM derived_source) d WHERE EXISTS(SELECT 1 FROM predicate_source FOR UPDATE)")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var modes []string
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		mode := ""
		if len(query.Locking) != 0 {
			mode = query.Locking[0].Mode
		}
		modes = append(modes, mode)
		return nil
	})
	if err != nil || len(names) != 4 {
		t.Fatal(names, err)
	}
	for i, want := range []string{"outer_source", "derived_source", "target_source", "predicate_source"} {
		if names[i] != want {
			t.Fatal(names)
		}
		if (i == 3) != (modes[i] == "FOR UPDATE") {
			t.Fatal("cross-query row mark leakage", modes)
		}
	}
}
