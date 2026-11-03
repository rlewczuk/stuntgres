package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationOriginSessionDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_diagnostics"
			originStateCleanup(t, b.DB, name)
			c := originSessionConn(t, p, b.DB)
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT pg_replication_origin_session_reset()", "SELECT pg_replication_origin_session_progress(false)", "SELECT pg_replication_origin_session_progress(true)", "SELECT pg_replication_origin_xact_setup('0/1','2022-03-04+00')"} {
					s := originStateError(q, "55000", "no replication origin is configured", "")
					s.Prepared = prepared
					originStateRun(t, p, b, c, s)
				}
				s := originStateScalar("SELECT pg_replication_origin_xact_reset() AS v", "VOID", "")
				s.Prepared = prepared
				originStateRun(t, p, b, c, s)
			}
			originStateCreate(t, p, b, b.DB, name)
			originSessionSetup(t, p, b, c, name)
			for _, prepared := range []bool{false, true} {
				s := originStateError("SELECT pg_replication_origin_session_setup($1::text)", "55000", "cannot setup replication origin when one is already setup", "", name)
				s.Prepared = prepared
				originStateRun(t, p, b, c, s)
				s = originStateError("SELECT pg_replication_origin_session_setup($1::text)", "42704", `replication origin "session_missing" does not exist`, "", "session_missing")
				s.Prepared = prepared
				originStateRun(t, p, b, c, s)
			}
			originSessionIsSetup(t, p, b, c, true)
			originSessionProgress(t, p, b, c, nil)
		})
	}
}
func TestReferenceReplicationOriginSessionBinding(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct{ call, signature string }{
		{"pg_replication_origin_session_setup()", "pg_replication_origin_session_setup()"},
		{"pg_replication_origin_session_setup(1)", "pg_replication_origin_session_setup(integer)"},
		{"pg_replication_origin_session_setup(node_name=>'x')", "pg_replication_origin_session_setup(node_name => unknown)"},
		{"pg_replication_origin_session_setup('x',1)", "pg_replication_origin_session_setup(unknown, integer)"},
		{"pg_replication_origin_session_reset(1)", "pg_replication_origin_session_reset(integer)"},
		{"pg_replication_origin_session_is_setup(1)", "pg_replication_origin_session_is_setup(integer)"},
		{"pg_replication_origin_session_progress()", "pg_replication_origin_session_progress()"},
		{"pg_replication_origin_session_progress(1)", "pg_replication_origin_session_progress(integer)"},
		{"pg_replication_origin_session_progress(flush=>false)", "pg_replication_origin_session_progress(flush => boolean)"},
		{"pg_replication_origin_xact_setup('0/1')", "pg_replication_origin_xact_setup(unknown)"},
		{"pg_replication_origin_xact_setup('0/1'::text,now())", "pg_replication_origin_xact_setup(text, timestamp with time zone)"},
		{"pg_replication_origin_xact_reset(1)", "pg_replication_origin_xact_reset(integer)"},
		{"public.pg_replication_origin_session_is_setup()", "public.pg_replication_origin_session_is_setup()"},
		{"pg_replication_origin_xact_setup(origin_lsn=>'0/1',origin_timestamp=>'2022-03-04+00')", "pg_replication_origin_xact_setup(origin_lsn => unknown, origin_timestamp => unknown)"},
		{"pg_replication_origin_session_setup(VARIADIC ARRAY['x'])", "pg_replication_origin_session_setup(text[])"},
		{"pg_replication_origin_xact_setup('0/1','2022-03-04+00',NULL)", "pg_replication_origin_xact_setup(unknown, unknown, unknown)"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				for _, prepared := range []bool{false, true} {
					for _, prefix := range []string{"SELECT ", "/*é*/ SELECT ", "SELECT * FROM "} {
						s := fixture.ImplicitError(prefix+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: fixture.Position(len([]rune(prefix)) + 1)})
						s.Prepared = prepared
						originStateRun(t, p, b, b.DB, s)
					}
				}
			}
		})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		originStateRun(t, p, b, b.DB, fixture.ImplicitError("SELECT missing_origin_schema.pg_replication_origin_session_is_setup()", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_origin_schema" does not exist`, Position: fixture.Position(8)}))
		originStateRun(t, p, b, b.DB, fixture.ImplicitError("SELECT pg_replication_origin_xact_setup(NULL::pg_lsn,'bad'::timestamptz)", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "bad"`, Position: fixture.Position(len("SELECT pg_replication_origin_xact_setup(NULL::pg_lsn,") + 1)}))
		for _, prepared := range []bool{false, true} {
			star := fixture.ImplicitError("SELECT pg_replication_origin_session_is_setup(*)", fixture.Diagnostic{Code: "42809", Message: "pg_replication_origin_session_is_setup(*) specified, but pg_replication_origin_session_is_setup is not an aggregate function", Position: fixture.Position(8)})
			star.Prepared = prepared
			originStateRun(t, p, b, b.DB, star)
			q := "SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz)"
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "bad"`, Context: "unnamed portal parameter $2 = '...'"}, nil, "bad")
			bad.Prepared, bad.SelectRows, bad.ErrorStage = prepared, true, "execute"
			originStateRun(t, p, b, b.DB, bad)
			valid := originStateScalar(q+" AS v", "VOID", nil, nil, "2022-03-04+00")
			valid.Prepared = prepared
			originStateRun(t, p, b, b.DB, valid)
		}
	}
}
func TestReferenceReplicationOriginSessionNulls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_nulls"
			originStateCleanup(t, b.DB, name)
			c := originSessionConn(t, p, b.DB)
			calls := []struct {
				q, typ string
				args   []any
			}{
				{"SELECT pg_replication_origin_session_setup($1::text) AS v", "VOID", []any{nil}},
				{"SELECT pg_replication_origin_session_progress($1::boolean) AS v", "PG_LSN", []any{nil}},
				{"SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz) AS v", "VOID", []any{nil, "2022-03-04+00"}},
				{"SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz) AS v", "VOID", []any{"0/9", nil}},
				{"SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz) AS v", "VOID", []any{nil, nil}},
			}
			for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
				originStateRun(t, p, b, c, fixture.Exec("SET plan_cache_mode="+mode, 0))
				for _, tc := range calls {
					originStateRun(t, p, b, c, originStateScalar(tc.q, tc.typ, nil, tc.args...))
					stmt, err := c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
					originStateRun(t, p, b, held, originStateScalar(tc.q, tc.typ, nil, tc.args...))
					stmt.Close()
				}
			}
			originStateRun(t, p, b, c, fixture.Exec("RESET plan_cache_mode", 0))
			originSessionIsSetup(t, p, b, c, false)
			originStateCreate(t, p, b, b.DB, name)
			originSessionSetup(t, p, b, c, name)
			originSessionXact(t, p, b, c, "0/5")
			for _, tc := range calls {
				originStateRun(t, p, b, c, originStateScalar(tc.q, tc.typ, nil, tc.args...))
			}
			originStateRun(t, p, b, c, fixture.Exec("CREATE TABLE session_null_rows(lsn pg_lsn,stamp timestamptz,flush boolean,name text)", 0))
			originStateRun(t, p, b, c, fixture.Exec("INSERT INTO session_null_rows VALUES(NULL,NULL,NULL,NULL)", 1))
			for i, call := range []string{"pg_replication_origin_session_setup(name)", "pg_replication_origin_session_progress(flush)", "pg_replication_origin_xact_setup(lsn,'2022-03-04+00')", "pg_replication_origin_xact_setup('0/9',stamp)", "pg_replication_origin_xact_setup(lsn,stamp)"} {
				typ := "VOID"
				if i == 1 {
					typ = "PG_LSN"
				}
				for _, prepared := range []bool{false, true} {
					s := originStateScalar("SELECT "+call+" AS v FROM session_null_rows", typ, nil)
					s.Prepared = prepared
					originStateRun(t, p, b, c, s)
				}
			}
			originSessionProgress(t, p, b, c, "0/5")
		})
	}
}
func TestReferenceReplicationOriginSessionDurability(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_durability"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			c := originSessionConn(t, p, b.DB)
			originSessionSetup(t, p, b, c, name)
			for _, want := range []any{nil, "80000000/9"} {
				if want != nil {
					originSessionXact(t, p, b, c, want.(string))
					originStateRun(t, p, b, c, fixture.Exec("SELECT pg_current_xact_id()", 1))
				}
				for _, prepared := range []bool{false, true} {
					s := originStateScalar("SELECT pg_replication_origin_session_progress(true) AS v", "PG_LSN", want)
					if !b.PG {
						s = originStateError(s.Query, "0A000", "pg_replication_origin_session_progress with flush=true requires durable WAL flushing, which is unsupported in embedded mode", "")
					}
					s.Prepared = prepared
					originStateRun(t, p, b, c, s)
				}
				originSessionProgress(t, p, b, c, want)
			}
		})
	}
}
