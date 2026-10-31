package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
	"strconv"

	"stuntgres/reference/fixture"
)

func TestReferencePgSeclabelDiagnostics(t *testing.T) {
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ query, name, prefix string }{
			{"SELECT oid FROM pg_catalog.pg_seclabel", "oid", "SELECT "},
			{"SELECT missing FROM pg_catalog.pg_seclabel WHERE FALSE", "missing", "SELECT "},
			{"/* café */ SELECT missing FROM pg_catalog.pg_seclabel", "missing", "/* café */ SELECT "},
		} {
			want := pgSeclabelDiagnostic("42703", "column \""+tc.name+"\" does not exist")
			want.Position = strconv.Itoa(utf8.RuneCountInString(tc.prefix) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, want)
				s.SelectRows, s.Prepared = true, prepared
				if prepared { s.ErrorStage = "prepare" } else { s.ErrorStage = "execute" }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		// token is the independently selected first illegal token; an empty
		// token denotes the end-of-input position, one character past input.
		for _, tc := range []struct{ query, token string }{
			{"SECURITY LABEL", ""},
			{"SECURITY LABEL ON TABLE t", ""},
			{"SECURITY LABEL ON TABLE t IS", ""},
			{"SECURITY LABEL TABLE t IS 'x'", "TABLE"},
			{"SECURITY LABEL FOR ON TABLE t IS 'x'", "ON"},
			{"SECURITY LABEL ON TABLE t IS $1", "$1"},
			{"SECURITY LABEL ON TABLE t IS 42", "42"},
			{"SECURITY LABEL ON TABLE t IS ('x')", "("},
			{"SECURITY LABEL ON TABLE t IS 'x' || 'y'", "||"},
			{"SECURITY LABEL ON TABLE t IS 'x' garbage", "garbage"},
			{"SECURITY LABEL ON FUNCTION f( IS 'x'", "'x'"},
			{"SECURITY LABEL ON FUNCTION f(integer,) IS 'x'", ")"},
			{"SECURITY LABEL ON AGGREGATE a IS 'x'", "IS"},
			{"SECURITY LABEL ON FUNCTION integer() IS 'x'", "("},
			{"SECURITY LABEL ON AGGREGATE integer(*) IS 'x'", "("},
			{"SECURITY LABEL ON FUNCTION integer(", "("},
			{"SECURITY LABEL ON FUNCTION integer( IS 'x'", "("},
			{"SECURITY LABEL ON FUNCTION integer(,) IS 'x'", "("},
			{"SECURITY LABEL ON AGGREGATE integer(", "("},
			{"SECURITY LABEL ON AGGREGATE integer( IS 'x'", "("},
			{"SECURITY LABEL ON AGGREGATE integer(,) IS 'x'", "("},
			{"SECURITY LABEL ON FUNCTION authorization.f() IS 'x'", "."},
			{"SECURITY LABEL ON LARGE OBJECT $1 IS 'x'", "$1"},
			{"SECURITY LABEL ON POLICY p ON t IS 'x'", "POLICY"},
			{"SECURITY LABEL ON RULE r ON t IS 'x'", "RULE"},
			{"SECURITY LABEL ON TRIGGER tr ON t IS 'x'", "TRIGGER"},
			{"PREPARE sl AS SECURITY LABEL ON TABLE t IS 'x'", "SECURITY"},
			{"/* é */ SECURITY LABEL ON TABLE t IS $1", "$1"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := pgSeclabelDiagnostic("42601", "syntax error at end of input")
				want.Position = strconv.Itoa(utf8.RuneCountInString(tc.query) + 1)
				if tc.token != "" {
					want.Message = "syntax error at or near \"" + tc.token + "\""
					want.Position = strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.Prepared = prepared
					if prepared { s.ErrorStage = "prepare" } else { s.ErrorStage = "execute" }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		pgSeclabelEmpty(t, p, b, c)
	})
}

func pgSeclabelSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT a,c FROM public.sl_sentinel", []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
	pgSeclabelEmpty(t, p, b, x)
}

func pgSeclabelSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	pgSeclabelExec(t, p, b, x, "CREATE TABLE public.sl_sentinel(a INT,b INT,c INT)")
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec("INSERT INTO public.sl_sentinel VALUES(7,8,9)", 1))
}

type pgSeclabelInventory struct {
	object [3]sql.NullString
	extensions [][6]string
}

func pgSeclabelInventoryRead(t *testing.T, p *fixture.Pair, x fixture.Executor) pgSeclabelInventory {
	t.Helper()
	var got pgSeclabelInventory
	rows, err := x.QueryContext(p.Ctx, "SELECT oid::text,relowner::text,relacl::text FROM pg_catalog.pg_class WHERE oid='public.sl_sentinel'::regclass")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	if !rows.Next() { t.Fatal("missing sentinel identity", rows.Err()) }
	if err := rows.Scan(&got.object[0], &got.object[1], &got.object[2]); err != nil { t.Fatal(err) }
	if rows.Next() || rows.Err() != nil { t.Fatal("sentinel identity cardinality", rows.Err()) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	rows, err = x.QueryContext(p.Ctx, "SELECT oid,extname,extowner,extnamespace,extrelocatable,extversion FROM pg_catalog.pg_extension ORDER BY extname")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	for rows.Next() {
		var row [6]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5]); err != nil { t.Fatal(err) }
		got.extensions = append(got.extensions, row)
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	return got
}

func pgSeclabelUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, before pgSeclabelInventory) {
	t.Helper()
	pgSeclabelSentinel(t, p, b, x)
	if after := pgSeclabelInventoryRead(t, p, x); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed SECURITY LABEL changed object/ACL/extension inventory: before=%#v after=%#v", before, after)
	}
}

func TestReferencePgSeclabelProviders(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sl_provider_reader"})
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgSeclabelSetup(t, p, b, c)
		pgSeclabelExec(t, p, b, c, "CREATE ROLE sl_provider_reader")
		before := pgSeclabelInventoryRead(t, p, c)
		// Every target alternative in REL_18_6 SecLabelStmt, including kinds
		// a loaded provider would subsequently reject. No target lookup wins.
		targets := []string{
			"TABLE public.sl_sentinel", "TABLE absent.missing", "COLUMN public.sl_sentinel.c", "COLUMN x", "COLUMN a.b.c.d",
			"SEQUENCE absent.s", "VIEW absent.v", "MATERIALIZED VIEW absent.mv", "INDEX absent.i", "FOREIGN TABLE absent.ft",
			"COLLATION absent.c", "CONVERSION absent.c", "STATISTICS absent.s",
			"TEXT SEARCH PARSER absent.p", "TEXT SEARCH DICTIONARY absent.d", "TEXT SEARCH TEMPLATE absent.t", "TEXT SEARCH CONFIGURATION absent.c",
			"ACCESS METHOD absent", "EVENT TRIGGER absent", "EXTENSION absent", "FOREIGN DATA WRAPPER absent", "LANGUAGE absent", "PROCEDURAL LANGUAGE absent",
			"PUBLICATION absent", "SCHEMA absent", "SERVER absent", "DATABASE absent", "ROLE absent", "SUBSCRIPTION absent", "TABLESPACE absent",
			"TYPE absent.t", "TYPE integer[]", "TYPE character varying(12)", "DOMAIN absent.d",
			"AGGREGATE absent.a(*)", "AGGREGATE absent.a(integer)", "AGGREGATE absent.a(integer ORDER BY text)",
			"FUNCTION absent.f()", "FUNCTION absent.f(integer,text)", "FUNCTION absent.f(IN x integer, OUT y text)",
			"PROCEDURE absent.p(INOUT x integer)", "ROUTINE absent.r(integer[])", "LARGE OBJECT 123", "LARGE OBJECT -1", "LARGE OBJECT 1.5",
			"FUNCTION absent.f", "PROCEDURE absent.p", "ROUTINE absent.r",
			"FUNCTION authorization", "FUNCTION authorization()", "FUNCTION absent.authorization",
			"FUNCTION integer", "FUNCTION integer.f()", "FUNCTION absent.integer()", `FUNCTION "integer"()`,
			"AGGREGATE absent.a(ORDER BY text)", "LARGE OBJECT -1.5", "LARGE OBJECT +1",
		}
		for _, target := range targets {
			t.Run(target, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					q := "SECURITY LABEL ON " + target + " IS 'secret'"
					x := fixture.Executor(c)
					if prepared { x = pgSeclabelHeld(t, p, c, q) }
					pgSeclabelError(t, p, b, x, q, pgSeclabelAbsent())
				}
				pgSeclabelUnchanged(t, p, b, c, before)
			})
		}
		for _, tc := range []struct{ sql, decoded string }{
			{"missing", "missing"}, {"MiSsInG", "missing"}, {`"MiSsInG"`, "MiSsInG"},
			{"'MiSsInG'", "MiSsInG"}, {"''", ""}, {`'a"b'`, `a"b`}, {"dummy", "dummy"}, {"selinux", "selinux"},
			{`E'back\\slash'`, `back\slash`}, {"'quo''te'", "quo'te"}, {"$p$café$p$", "café"},
		} {
			for _, label := range []string{"'secret'", "NULL"} {
				q := "SECURITY LABEL FOR " + tc.sql + " ON TABLE public.sl_sentinel IS " + label
				pgSeclabelError(t, p, b, c, q, pgSeclabelProvider(tc.decoded))
				held := pgSeclabelHeld(t, p, c, q)
				pgSeclabelError(t, p, b, held, q, pgSeclabelProvider(tc.decoded))
			}
			pgSeclabelUnchanged(t, p, b, c, before)
		}
		pgSeclabelExec(t, p, b, c, "ALTER TABLE public.sl_sentinel DROP COLUMN b")
		for _, role := range []string{"sl_provider_reader", "NONE"} {
			pgSeclabelExec(t, p, b, c, "SET ROLE "+role)
			for _, target := range []string{"TABLE public.sl_sentinel", "COLUMN public.sl_sentinel.c", "COLUMN public.sl_sentinel.missing", "TYPE absent.t"} {
				q := "SECURITY LABEL FOR missing ON " + target + " IS NULL"
				pgSeclabelError(t, p, b, c, q, pgSeclabelProvider("missing"))
				h := pgSeclabelHeld(t, p, c, q)
				pgSeclabelError(t, p, b, h, q, pgSeclabelProvider("missing"))
			}
		}
		pgSeclabelUnchanged(t, p, b, c, before)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT attnum::int4 FROM pg_catalog.pg_attribute WHERE attrelid='public.sl_sentinel'::regclass AND attname='c'", []string{"attnum"}, []string{"INT4"}, [][]any{{int64(3)}}))
		pgSeclabelExec(t, p, b, c, "DROP ROLE sl_provider_reader")
	})
}
