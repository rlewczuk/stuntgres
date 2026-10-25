package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeAuthorization(t *testing.T) {
	for _, kind := range []string{"range_schema", "companion_schema", "subtype", "diff", "constructor"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"range_type_creator"})
			fixture.SchemaReferenceCleanup(t, "range_type_allowed")
			fixture.SchemaReferenceCleanup(t, "range_type_denied")
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := conn.ExecContext(cleanup, `RESET ROLE`); err != nil {
						t.Error(err)
					}
				}()
				for _, q := range []string{`CREATE ROLE range_type_creator`, `CREATE SCHEMA range_type_allowed`, `CREATE SCHEMA range_type_denied`, `GRANT USAGE,CREATE ON SCHEMA range_type_allowed TO range_type_creator`, `GRANT USAGE ON SCHEMA range_type_denied TO range_type_creator`, `GRANT USAGE ON SCHEMA public TO range_type_creator`, `CREATE TYPE public.range_endpoint AS ENUM('z','a')`, `REVOKE USAGE ON TYPE public.range_endpoint FROM PUBLIC`} {
					run(fixture.Exec(q, 0))
				}
				if kind == "constructor" {
					rangeTypeConstructorAuthorization(t, p, b, conn, run)
					return
				}
				q := `CREATE TYPE range_type_denied.guard AS RANGE(SUBTYPE=int4)`
				want := fixture.Diagnostic{Code: "42501", Message: `permission denied for schema range_type_denied`}
				grant := `GRANT CREATE ON SCHEMA range_type_denied TO range_type_creator`
				switch kind {
				case "companion_schema":
					q = `CREATE TYPE range_type_allowed.guard AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_type_denied.guard_many)`
					want.Position = ""
				case "subtype":
					q = `CREATE TYPE range_type_allowed.guard AS RANGE(SUBTYPE=public.range_endpoint)`
					want = fixture.Diagnostic{Code: "42501", Message: `permission denied for type range_endpoint`}
					grant = `GRANT USAGE ON TYPE public.range_endpoint TO range_type_creator`
				case "diff":
					run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION pg_catalog.float8mi(double precision,double precision) FROM PUBLIC`, 0))
					defer func() {
						cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if _, err := conn.ExecContext(cleanup, `RESET ROLE; REVOKE EXECUTE ON FUNCTION pg_catalog.float8mi(double precision,double precision) FROM range_type_creator; GRANT EXECUTE ON FUNCTION pg_catalog.float8mi(double precision,double precision) TO PUBLIC`); err != nil {
							t.Error(err)
						}
					}()
					q = `CREATE TYPE range_type_allowed.guard AS RANGE(SUBTYPE=float8,SUBTYPE_DIFF=pg_catalog.float8mi)`
					want = fixture.Diagnostic{Code: "42501", Message: `permission denied for function float8mi`}
					grant = `GRANT EXECUTE ON FUNCTION pg_catalog.float8mi(double precision,double precision) TO range_type_creator`
				}
				for _, prepared := range []bool{false, true} {
					run(fixture.Exec(`SET ROLE range_type_creator`, 0))
					s := fixture.ImplicitError(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Exec(`RESET ROLE`, 0))
					run(fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace IN ('range_type_allowed'::regnamespace,'range_type_denied'::regnamespace) AND typname IN ('guard','guard_multirange','guard_many','_guard','_guard_multirange','_guard_many') ORDER BY typname`, []string{"typname"}, []string{"NAME"}, nil))
				}
				run(fixture.Exec(grant, 0))
				run(fixture.Exec(`SET ROLE range_type_creator`, 0))
				defer conn.ExecContext(p.Ctx, `RESET ROLE`)
				run(fixture.Exec(q, 0))
				run(fixture.Exec(`RESET ROLE`, 0))
			})
		})
	}
}

func rangeTypeConstructorAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec(`CREATE TYPE guarded_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=guarded_spans)`, 0))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `RESET ROLE; REVOKE EXECUTE ON FUNCTION guarded_span(integer,integer),guarded_spans() FROM range_type_creator; GRANT EXECUTE ON FUNCTION guarded_span(integer,integer),guarded_spans() TO PUBLIC`); err != nil {
			t.Error(err)
		}
	}()
	for _, tc := range []struct {
		name, signature, q, typ, want string
		args                          []any
	}{
		{"guarded_span", "guarded_span(integer,integer)", `SELECT guarded_span($1::int4,$2::int4) AS r`, "GUARDED_SPAN", "[1,3)", []any{int64(1), int64(3)}},
		{"guarded_spans", "guarded_spans()", `SELECT guarded_spans() AS r`, "GUARDED_SPANS", "{}", nil},
	} {
		run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION `+tc.signature+` FROM PUBLIC`, 0))
		stmt, err := conn.PrepareContext(p.Ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec(`SET ROLE range_type_creator`, 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name}, tc.args...)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
		}
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name}, tc.args...)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}, b.Recorder, s)
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+tc.signature+` TO range_type_creator`, 0))
		run(fixture.Exec(`SET ROLE range_type_creator`, 0))
		positive := rangeTypeSelect(b, tc.q, []string{"r"}, []string{tc.typ}, [][]any{{tc.want}}, nil, tc.args...)
		run(positive)
		run(fixture.Prepared(positive))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}, b.Recorder, positive)
		run(fixture.Exec(`RESET ROLE`, 0))
	}
}
