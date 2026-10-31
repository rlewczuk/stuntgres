package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSecurityLabelGrammar(t *testing.T) {
	for _, target := range []string{
		"TABLE a.b.c.d", "COLUMN x", "COLUMN a.b.c.d", "SEQUENCE s", "VIEW v", "MATERIALIZED VIEW v", "INDEX i", "FOREIGN TABLE t",
		"COLLATION c", "CONVERSION c", "STATISTICS s", "TEXT SEARCH PARSER p", "TEXT SEARCH DICTIONARY d", "TEXT SEARCH TEMPLATE t", "TEXT SEARCH CONFIGURATION c",
		"ACCESS METHOD a", "EVENT TRIGGER e", "EXTENSION e", "FOREIGN DATA WRAPPER f", "LANGUAGE l", "PROCEDURAL LANGUAGE l", "PUBLICATION p", "SCHEMA s", "SERVER s", "DATABASE d", "ROLE r", "SUBSCRIPTION s", "TABLESPACE t",
		"TYPE missing.t", "TYPE integer[]", "TYPE character varying(12)", "DOMAIN missing.d",
		"FUNCTION f()", "FUNCTION s.f(IN x integer, OUT y text)", "PROCEDURE p(INOUT x integer)", "ROUTINE r(integer[])",
		"FUNCTION f", "PROCEDURE p", "ROUTINE s.r", "FUNCTION authorization", "FUNCTION authorization()", "FUNCTION s.authorization",
		"FUNCTION integer", "FUNCTION integer.f()", "FUNCTION s.integer()", `FUNCTION "integer"()`,
		"AGGREGATE a(*)", "AGGREGATE a(integer)", "AGGREGATE a(ORDER BY text)", "AGGREGATE a(integer ORDER BY text)",
		"LARGE OBJECT 123", "LARGE OBJECT -1", "LARGE OBJECT +1", "LARGE OBJECT 1.5",
	} {
		t.Run(target, func(t *testing.T) {
			for _, label := range []string{"'secret'", "NULL"} {
				statements, err := Parse("SECURITY LABEL ON " + target + " IS " + label)
				if err != nil || len(statements) != 1 { t.Fatal(statements, err) }
				s, ok := statements[0].(*SecurityLabel)
				if !ok || s.Kind == "" || s.Provider != nil || s.Pos.Character != 1 { t.Fatalf("target AST %#v", statements[0]) }
				if label == "NULL" && s.Label.Kind != NullValue || label != "NULL" && (s.Label.Kind != StringValue || s.Label.Text != "secret") { t.Fatal("label", s.Label) }
			}
		})
	}
	for _, tc := range []struct{ provider, decoded string }{
		{"MiSsInG", "missing"}, {`"MiSsInG"`, "MiSsInG"}, {"'MiSsInG'", "MiSsInG"}, {"''", ""}, {`'a"b'`, `a"b`}, {"authorization", "authorization"},
		{`E'back\\slash'`, `back\slash`}, {"'quo''te'", "quo'te"}, {"$p$café$p$", "café"},
	} {
		statements, err := Parse("/*é*/ SECURITY LABEL FOR " + tc.provider + " ON TABLE \"Case\".t IS NULL")
		if err != nil || len(statements) != 1 { t.Fatal(statements, err) }
		s := statements[0].(*SecurityLabel)
		if s.Provider == nil || s.Provider.Text != tc.decoded || s.Pos.Character != 7 || s.Pos.Offset != 7 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "Case" { t.Fatalf("decoded AST %#v", s) }
	}
	statements, err := Parse("SECURITY LABEL ON FUNCTION s.f(IN x integer, OUT y text) IS NULL")
	if err != nil || len(statements) != 1 { t.Fatal(statements, err) }
	routine := statements[0].(*SecurityLabel).Routine
	if !routine.SignatureSpecified || len(routine.Name.Parts) != 2 || len(routine.Arguments) != 2 || routine.Arguments[0].Mode != "in" || routine.Arguments[0].Name.Text != "x" || routine.Arguments[1].Mode != "out" { t.Fatal("routine signature", routine) }
	for _, q := range []string{"CREATE TABLE security(label INT); SELECT label FROM security", "SELECT 1 AS label; SELECT 1 security"} {
		if _, err := Parse(q); err != nil { t.Fatal("unreserved security/label", q, err) }
	}
}

func TestSecurityLabelSyntax(t *testing.T) {
	for _, q := range []string{
		"SECURITY LABEL", "SECURITY LABEL ON TABLE t", "SECURITY LABEL ON TABLE t IS", "SECURITY LABEL TABLE t IS 'x'", "SECURITY LABEL FOR ON TABLE t IS 'x'",
		"SECURITY LABEL ON TABLE t IS $1", "SECURITY LABEL ON TABLE t IS 42", "SECURITY LABEL ON TABLE t IS ('x')", "SECURITY LABEL ON TABLE t IS 'x' || 'y'",
		"SECURITY LABEL ON TABLE t IS 'x' garbage", "SECURITY LABEL ON FUNCTION f( IS 'x'", "SECURITY LABEL ON FUNCTION f(integer,) IS 'x'", "SECURITY LABEL ON LARGE OBJECT $1 IS 'x'",
		"SECURITY LABEL ON POLICY p ON t IS 'x'", "SECURITY LABEL ON RULE r ON t IS 'x'", "SECURITY LABEL ON TRIGGER tr ON t IS 'x'", "PREPARE sl AS SECURITY LABEL ON TABLE t IS 'x'",
		"SELECT 1; SECURITY LABEL ON TABLE t IS $1", "SECURITY LABEL ON TABLE t IS NULL; SECURITY LABEL ON TABLE t IS 7",
		"SECURITY LABEL ON AGGREGATE a IS 'x'",
		"SECURITY LABEL ON FUNCTION integer() IS 'x'", "SECURITY LABEL ON FUNCTION authorization.f() IS 'x'",
	} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" { t.Fatalf("whole-input syntax %s: %#v %v", q, statements, err) }
	}
	// Pinned PostgreSQL 18.6 reports the callable opener for both families;
	// the bare keyword identity and qualified signatures remain legal.
	for _, tc := range []struct{ query, position string }{
		{"SECURITY LABEL ON FUNCTION integer() IS 'x'", "35"},
		{"SECURITY LABEL ON AGGREGATE integer(*) IS 'x'", "36"},
		{"SECURITY LABEL ON FUNCTION integer(", "35"},
		{"SECURITY LABEL ON FUNCTION integer( IS 'x'", "35"},
		{"SECURITY LABEL ON FUNCTION integer(,) IS 'x'", "35"},
		{"SECURITY LABEL ON AGGREGATE integer(", "36"},
		{"SECURITY LABEL ON AGGREGATE integer( IS 'x'", "36"},
		{"SECURITY LABEL ON AGGREGATE integer(,) IS 'x'", "36"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at or near \"(\"" || diagnostic.Position != tc.position {
			t.Fatalf("callable opener diagnostic: %s: %#v %v", tc.query, statements, err)
		}
	}
}
