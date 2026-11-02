package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
)

func TestPgStatStatementsFingerprintSQLCallSyntax(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"SELECT substring('abc' FROM 1)", "SELECT pg_catalog.substring('abc',1)", false},
		{"SELECT trim('abc')", "SELECT pg_catalog.btrim('abc')", false},
		{"SELECT substring('abc' FROM 'b')", "SELECT pg_catalog.substring('abc','b')", false},
		{"SELECT substring('abc' FROM 'a%' FOR '#')", "SELECT pg_catalog.substring('abc','a%','#')", false},
		{"SELECT EXTRACT(year FROM DATE '2001-01-01')", "SELECT pg_catalog.extract('year',DATE '2001-01-01')", false},
		{"SELECT overlay('abc'::bytea PLACING 'd'::bytea FROM 1)", "SELECT pg_catalog.overlay('abc'::bytea,'d'::bytea,1)", false},
		{"SELECT (DATE '2001-01-01',DATE '2001-01-02') OVERLAPS (DATE '2001-01-01',DATE '2001-01-03')", "SELECT pg_catalog.overlaps(DATE '2001-01-01',DATE '2001-01-02',DATE '2001-01-01',DATE '2001-01-03')", false},
		{"SELECT substring('abc' FROM 1)", "SELECT substring('def' FROM 2)", true},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, right := pgssAnalyzedJumble(t, e, tc.left), pgssAnalyzedJumble(t, e, tc.right)
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected FuncExpr.funcformat: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintXML(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"SELECT XMLELEMENT(NAME first,1)", "SELECT XMLELEMENT(NAME second,2)", true},
		{"SELECT XMLFOREST(1 AS first)", "SELECT XMLFOREST(2 AS second)", true},
		{"SELECT XMLPARSE(DOCUMENT '<a/>')", "SELECT XMLPARSE(CONTENT '<a/>')", true},
		{"SELECT XMLPARSE(CONTENT '<a/>')", "SELECT XMLPARSE(CONTENT '<a/>' PRESERVE WHITESPACE)", true},
		{"SELECT XMLROOT('<a/>'::xml,VERSION NO VALUE)", "SELECT XMLROOT('<a/>'::xml,VERSION NULL)", true},
		{"SELECT XMLROOT('<a/>'::xml,VERSION NO VALUE)", "SELECT XMLROOT('<a/>'::xml,VERSION NO VALUE,STANDALONE YES)", true},
		{"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text)", "SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text NO INDENT)", true},
		{"SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text)", "SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS text INDENT)", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, right := pgssAnalyzedJumble(t, e, tc.left), pgssAnalyzedJumble(t, e, tc.right)
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected analyzed XmlExpr: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"XMLELEMENT(NAME first,1)", "XMLELEMENT(NAME second,1)", true},
		{"XMLFOREST(1 AS first)", "XMLFOREST(1 AS second)", true},
		{"XMLPARSE(DOCUMENT '<a/>')", "XMLPARSE(CONTENT '<a/>')", true},
		{"XMLPARSE(CONTENT '<a/>')", "XMLPARSE(CONTENT '<a/>' STRIP WHITESPACE)", true},
		{"XMLPARSE(CONTENT '<a/>')", "XMLPARSE(CONTENT '<a/>' PRESERVE WHITESPACE)", false},
		{"XMLROOT('<a/>'::xml,VERSION NO VALUE)", "XMLROOT('<a/>'::xml,VERSION NULL)", true},
		{"XMLROOT('<a/>'::xml,VERSION NO VALUE)", "XMLROOT('<a/>'::xml,VERSION NO VALUE,STANDALONE NO VALUE)", false},
		{"XMLSERIALIZE(CONTENT '<a/>'::xml AS text)", "XMLSERIALIZE(CONTENT '<a/>'::xml AS text NO INDENT)", true},
		{"substring('abc' FROM 1)", "pg_catalog.substring('abc',1)", false},
	} {
		t.Run("raw / "+tc.left+" / "+tc.right, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_xml_raw AS SELECT "+tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_xml_raw AS SELECT "+tc.right))
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected raw syntax: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}
