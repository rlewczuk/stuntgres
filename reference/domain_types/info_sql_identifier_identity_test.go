package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func infoSQLIdentifierInvalid(query, suffix string, input any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{
		Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + suffix,
		Context: "unnamed portal parameter $1",
	}, input)
	s.ErrorStage = "execute"
	return s
}

func TestReferenceInfoSQLIdentifierDiagnostics(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_sql_identifier_witness VALUES (1,'sentinel'),(2,NULL)", 2))
		unchanged := infoSQLIdentifierStored(backend.PG, [][]any{{"sentinel"}, {nil}})
		q := "INSERT INTO public.info_sql_identifier_witness VALUES (90,'ok'),(91,$1)"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, prefix := range []string{"", strings.Repeat("x", 63)} {
			for _, tc := range []struct{ raw, suffix string }{
				{"\x00", "0x00"}, {"\xff", "0xff"}, {"\xc3", "0xc3"}, {"\xc3(", "0xc3 0x28"},
			} {
				for mode := range 2 {
					var input any = prefix + tc.raw
					if mode == 1 {
						input = []byte(prefix + tc.raw)
					}
					s := infoSQLIdentifierInvalid(q, tc.suffix, input)
					for _, prepared := range []bool{false, true} {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						infoSQLIdentifierBoth(t, p, conn, recorder, unchanged)
					}
					s.Prepared = false
					fixture.RunStep(t, p.Ctx, held, recorder, s)
					infoSQLIdentifierBoth(t, p, conn, recorder, unchanged)
				}
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		result, err := backend.DB.ExecContext(ctx, q, "valid")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled INSERT result=%v error=%v", result, err)
		}
		infoSQLIdentifierBoth(t, p, conn, recorder, unchanged)
	})
}

func infoSQLIdentifierIdentity(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	// Restrict join inputs with existing WHERE predicates; keep every identity
	// relationship and expected row while avoiding unrelated catalog candidates.
	infoSQLIdentifierBoth(t, p, conn, recorder, fixture.Select(
		"SELECT 'information_schema.sql_identifier'::regtype::text AS domain,'pg_catalog.name'::regtype::text AS base",
		[]string{"domain", "base"}, []string{"TEXT", "TEXT"}, [][]any{{"information_schema.sql_identifier", "name"}}))
	infoSQLIdentifierBoth(t, p, conn, recorder, fixture.Select(
		"SELECT t.typtype AS kind,n.nspname AS domain_schema,t.typname AS domain_name,b.oid::integer AS base_oid,bn.nspname AS base_schema,b.typname AS base_name,t.typtypmod AS modifier,t.typnotnull AS required,t.typndims AS dimensions,t.typcollation::integer AS collation,t.typdefault AS default_value,(t.oid>0 AND t.oid<>b.oid AND t.typarray>0 AND t.typarray<>t.oid AND a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS identity FROM (SELECT * FROM pg_type WHERE typname='sql_identifier') t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace JOIN pg_type a ON a.oid=t.typarray WHERE n.nspname='information_schema' AND t.typname='sql_identifier'",
		[]string{"kind", "domain_schema", "domain_name", "base_oid", "base_schema", "base_name", "modifier", "required", "dimensions", "collation", "default_value", "identity"},
		[]string{"CHAR", "NAME", "NAME", "INT4", "NAME", "NAME", "INT4", "BOOL", "INT4", "INT4", "TEXT", "BOOL"},
		[][]any{{"d", "information_schema", "sql_identifier", int64(19), "pg_catalog", "name", int64(-1), false, int64(0), int64(950), nil, true}}))
	infoSQLIdentifierBoth(t, p, conn, recorder, fixture.Select(
		"SELECT a.atttypid=t.oid AS domain_identity,a.atttypmod AS modifier,a.attcollation::integer AS collation FROM (SELECT * FROM pg_attribute WHERE attname='v') a JOIN (SELECT * FROM pg_class WHERE relname='info_sql_identifier_witness') c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_type t ON t.oid=a.atttypid WHERE n.nspname='public' AND c.relname='info_sql_identifier_witness' AND a.attname='v' AND t.typname='sql_identifier'",
		[]string{"domain_identity", "modifier", "collation"}, []string{"BOOL", "INT4", "INT4"}, [][]any{{true, int64(-1), int64(950)}}))
	if !backend.PG {
		infoSQLIdentifierBoth(t, p, conn, recorder, fixture.Select(
			"SELECT 'information_schema.sql_identifier'::regtype::bigint AS domain_oid,'information_schema.sql_identifier[]'::regtype::bigint AS array_oid",
			[]string{"domain_oid", "array_oid"}, []string{"INT8", "INT8"}, [][]any{{int64(13248), int64(13247)}}))
	}
}

func TestReferenceInfoSQLIdentifierIdentity(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoSQLIdentifierIdentity(t, p, backend, conn, recorder)
	})
}
