package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const securityLabelNoProvider = "no security label providers have been loaded"

func securityLabelScenarios(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer c.Close()
					body(t, p, b, c, prepared)
				})
			}
		})
	}
}

func securityLabelError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, q, message string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RoleReferenceError(q, "22023", message, "", prepared, "execute"))
}

func securityLabelSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE sl_t(v INT)", "COMMENT ON TABLE sl_t IS 'old'", "COMMENT ON COLUMN sl_t.v IS 'old column'"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sl_t VALUES(7)", 1))
}

func securityLabelUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM "+table+" ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT obj_description('"+table+"'::regclass,'pg_class') AS table_text,col_description('"+table+"'::regclass,1) AS column_text", []string{"table_text", "column_text"}, []string{"TEXT", "TEXT"}, [][]any{{"old", "old column"}}))
}

func TestReferenceSecurityLabelProviderBoundary(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		securityLabelSeed(t, p, b, c)
		for _, label := range []string{"'classified'", "''", "NULL"} {
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_t IS "+label, securityLabelNoProvider)
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL FOR missing ON TABLE sl_t IS "+label, `security label provider "missing" is not loaded`)
		}
		for _, tc := range []struct { sql, name string }{
			{"MissingProvider", "missingprovider"}, {`"MissingProvider"`, "MissingProvider"}, {"'MissingProvider'", "MissingProvider"},
			{"''", ""}, {`'a"b'`, `a"b`}, {"'a''b'", "a'b"}, {`E'a\nb'`, "a\nb"}, {"'雪'", "雪"},
			{"between", "between"}, {"authorization", "authorization"}, {"is", "is"}, {"label", "label"},
			{"setof", "setof"},
			{"nchar", "nchar"}, {"national", "national"},
			{"'" + strings.Repeat("X", 80) + "'", strings.Repeat("X", 80)},
		} {
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL FOR "+tc.sql+" ON TABLE sl_t IS NULL", "security label provider \""+tc.name+"\" is not loaded")
		}
		securityLabelUnchanged(t, p, b, c, "sl_t")
	})
}

func TestReferenceSecurityLabelObjectSyntax(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, target := range []string{
			"TABLE public.sl_missing", "COLUMN public.sl_missing.v", "COLUMN v",
			"COLUMN db.s.t.v", "TABLE db.s.t", "FUNCTION db.s.f(integer)", "AGGREGATE db.s.a(*)",
			"AGGREGATE public.sl_missing(*)", "AGGREGATE public.sl_missing(IN n integer)",
			"AGGREGATE public.sl_missing(ORDER BY integer)", "AGGREGATE public.sl_missing(integer ORDER BY integer)",
			"DATABASE sl_missing", "DOMAIN public.sl_missing", "DOMAIN numeric(4,2)[]", "EVENT TRIGGER sl_missing",
			"FOREIGN TABLE public.sl_missing", "FUNCTION public.sl_missing", "FUNCTION public.sl_missing()",
			"FUNCTION public.sl_missing(IN n integer, OUT z text, INOUT a integer, VARIADIC rest text[])",
			"FUNCTION public.sl_missing(sl_missing.sl_type)", "LARGE OBJECT 12345", "LARGE OBJECT -1.2e3", "LARGE OBJECT +12345",
			"MATERIALIZED VIEW public.sl_missing", "LANGUAGE sl_missing", "PROCEDURAL LANGUAGE sl_missing",
			"PROCEDURE public.sl_missing", "PROCEDURE public.sl_missing(integer)", "PUBLICATION sl_missing", "ROLE sl_missing",
			"ROUTINE public.sl_missing", "ROUTINE public.sl_missing(integer)", "SCHEMA sl_missing", "SEQUENCE public.sl_missing",
			"SUBSCRIPTION sl_missing", "TABLESPACE sl_missing", "TYPE public.sl_missing", "TYPE integer[]", "TYPE numeric(4,2)[]", "VIEW public.sl_missing",
			"TYPE db.s.ty[]", "DOMAIN db.s.ty(1)", "TYPE int4(1)", "DOMAIN bool(1)",
			"FUNCTION f(db.s.ty[],int4(1))", "AGGREGATE a(db.s.ty ORDER BY bool(1))",
			"TYPE int4(abs(1))", "TYPE int4(1=1)", "TYPE int4((SELECT 1))", "TYPE int4($1)",
			"TYPE db.s.ty(abs(1),1=1)", "TYPE bit(abs(1))", "TYPE int4(1::int4(abs(2)))",
			"TYPE SETOF integer", "DOMAIN SETOF integer[]", "FUNCTION f(SETOF integer,IN n SETOF text)",
			"FUNCTION f(t.v%TYPE,SETOF db.t.v%TYPE)", "AGGREGATE a(t.v%TYPE)", "AGGREGATE a(SETOF t.v%TYPE)",
			"TYPE int4(setof)", "TABLE setof",
			"TYPE json", `TYPE "json"(1)`, "TYPE public.json(1)", "TYPE pg_catalog.json(1)", `TYPE "pg_catalog"."json"(1)`,
			"TYPE nchar", "TYPE national char", "TYPE national character",
			"TYPE nchar(3)", "TYPE national char(3)", "TYPE national character(3)",
			"TYPE nchar varying", "TYPE national char varying", "TYPE national character varying",
			"TYPE nchar varying(3)", "TYPE national char varying(3)", "TYPE national character varying(3)",
			"TYPE nchar(0)", "TYPE nchar varying(0)", "DOMAIN national character varying(3)[]",
			"FUNCTION f(nchar,national char varying(3))", "PROCEDURE p(national character)", "AGGREGATE a(nchar)",
			"TYPE SETOF national character[]", `TYPE "nchar"(abs(1))`, "TYPE s.nchar(abs(1))",
			"TYPE pg_catalog.nchar(1)", `TYPE "national"(1)`, `TYPE "national".ty(1)`, "TYPE s.national(1)",
			`TYPE "nchar".ty(1)`, `TYPE "nchar".ty`, "TYPE s.nchar(1)", `TYPE s."nchar"(1)`,
			"TYPE s.nchar", `TYPE s."nchar"`, "TABLE nchar", "TABLE national",
			`COLUMN "s.dot"."t.dot"."v.dot"`, "INDEX public.sl_missing", "COLLATION public.sl_missing", "CONVERSION public.sl_missing", "STATISTICS public.sl_missing",
			"TEXT SEARCH PARSER public.sl_missing", "TEXT SEARCH DICTIONARY public.sl_missing", "TEXT SEARCH TEMPLATE public.sl_missing", "TEXT SEARCH CONFIGURATION public.sl_missing",
			"ACCESS METHOD sl_missing", "EXTENSION sl_missing", "FOREIGN DATA WRAPPER sl_missing", "SERVER sl_missing",
		} {
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON "+target+" IS NULL", securityLabelNoProvider)
		}
		for _, target := range []string{"TYPE sl_missing.sl_type[]", "FUNCTION sl_missing.f(sl_missing.ty)", "LARGE OBJECT 1e20"} {
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL FOR 'MissingProvider' ON "+target+" IS ''", `security label provider "MissingProvider" is not loaded`)
		}
	})
}

func TestReferenceSecurityLabelSyntaxDiagnostics(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, tc := range []struct { q, message, position string }{
			{`SECURITY LABEL ON TABLE t IS $1`, `syntax error at or near "$1"`, "30"},
			{`SECURITY LABEL ON TABLE t IS 1`, `syntax error at or near "1"`, "30"},
			{`SECURITY LABEL ON TABLE t IS x`, `syntax error at or near "x"`, "30"},
			{`SECURITY LABEL ON TABLE t IS 'x' || 'y'`, `syntax error at or near "||"`, "34"},
			{`SECURITY LABEL ON TABLE t IS`, `syntax error at end of input`, "29"},
			{`SECURITY LABEL ON TABLE t IS NULL junk`, `syntax error at or near "junk"`, "35"},
			{`SECURITY LABEL FOR ON TABLE t IS NULL`, `syntax error at or near "ON"`, "20"},
			{`SECURITY LABEL ON AGGREGATE a(integer IS NULL`, `syntax error at or near "IS"`, "39"},
			{`SECURITY LABEL ON COLUMN t. IS NULL`, `syntax error at or near "NULL"`, "32"},
			{`SECURITY LABEL ON TABLE "雪" IS $1`, `syntax error at or near "$1"`, "32"},
			{`SECURITY LABEL ON TYPE integer(1) IS NULL`, `syntax error at or near "("`, "31"},
			{`SECURITY LABEL ON DOMAIN boolean(1) IS NULL`, `syntax error at or near "("`, "33"},
			{`SECURITY LABEL ON TYPE int4(1+) IS NULL`, `syntax error at or near ")"`, "31"},
			{`SECURITY LABEL ON FUNCTION f(t.v%) IS NULL`, `syntax error at or near ")"`, "34"},
			{`SECURITY LABEL ON TABLE t IS NULL; SELECT )`, `syntax error at or near ")"`, "43"},
			{`SECURITY LABEL ON TYPE json(1) IS NULL`, `syntax error at or near "("`, "28"},
			{`SECURITY LABEL ON TYPE JSON(abs(1)) IS NULL`, `syntax error at or near "("`, "28"},
			{`SECURITY LABEL ON TYPE int4(CAST(1 AS json(1))) IS NULL`, `syntax error at or near "("`, "43"},
			{`SECURITY LABEL ON TYPE nchar(1+1) IS NULL`, `syntax error at or near "+"`, "31"},
			{`SECURITY LABEL ON TYPE nchar(abs(1)) IS NULL`, `syntax error at or near "abs"`, "30"},
			{`SECURITY LABEL ON TYPE nchar varying(1,2) IS NULL`, `syntax error at or near ","`, "39"},
			{`SECURITY LABEL ON TYPE national IS NULL`, `syntax error at or near "IS"`, "33"},
			{`SECURITY LABEL ON TYPE national character(1+1) IS NULL`, `syntax error at or near "+"`, "44"},
			{`SECURITY LABEL ON TYPE national.ty(1) IS NULL`, `syntax error at or near "."`, "32"},
			{`SECURITY LABEL ON TYPE nchar.ty(1) IS NULL`, `syntax error at or near "."`, "29"},
			{`SECURITY LABEL ON TYPE nchar.ty IS NULL`, `syntax error at or near "."`, "29"},
			{`SECURITY LABEL ON TYPE int4(abs(1) WITHIN GROUP (ORDER BY 1) WITHIN GROUP (ORDER BY 1)) IS NULL`, `syntax error at or near "WITHIN"`, "62"},
			{`SECURITY LABEL ON TYPE int4(abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) FILTER (WHERE false)) IS NULL`, `syntax error at or near "FILTER"`, "82"},
			{`SECURITY LABEL ON TYPE int4(abs(1) OVER () OVER ()) IS NULL`, `syntax error at or near "OVER"`, "44"},
			{`SECURITY LABEL ON TYPE int4(abs(1) FILTER (WHERE true) WITHIN GROUP (ORDER BY 1)) IS NULL`, `syntax error at or near "WITHIN"`, "56"},
			{`SECURITY LABEL ON TYPE int4(abs(1) OVER () FILTER (WHERE true)) IS NULL`, `syntax error at or near "FILTER"`, "44"},
			{`SECURITY LABEL ON TYPE int4(abs(1) OVER () WITHIN GROUP (ORDER BY 1)) IS NULL`, `syntax error at or near "WITHIN"`, "44"},
			{`SECURITY LABEL ON TYPE int4(abs(1) WITHIN GROUP (ORDER BY 1) OVER () FILTER (WHERE true)) IS NULL`, `syntax error at or near "FILTER"`, "70"},
			{`SECURITY LABEL ON TYPE int4(abs(DISTINCT 1) WITHIN GROUP (ORDER BY 1)) IS NULL`, `cannot use DISTINCT with WITHIN GROUP`, "45"},
			{`SECURITY LABEL ON TYPE int4(f(VARIADIC x) WITHIN GROUP (ORDER BY 1)) IS NULL`, `cannot use VARIADIC with WITHIN GROUP`, "43"},
			{`SECURITY LABEL ON TYPE int4(abs(1 ORDER BY 1) WITHIN GROUP (ORDER BY 1)) IS NULL`, `cannot use multiple ORDER BY clauses with WITHIN GROUP`, "47"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: tc.position})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared { s.ErrorStage = "prepare" }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, mode := range []string{"OUT", "INOUT"} {
			s := fixture.ImplicitError("SECURITY LABEL ON AGGREGATE a("+mode+" n integer) IS NULL", fixture.Diagnostic{Code: "0A000", Message: "aggregates cannot have output arguments", Position: "31"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared { s.ErrorStage = "prepare" }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
