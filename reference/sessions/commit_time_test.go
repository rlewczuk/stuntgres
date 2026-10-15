package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func commitTimeOffScenario(t *testing.T, run func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			run(t, p, b, c)
		})
	}
}

func TestReferenceCommitTimeDisabled(t *testing.T) {
	commitTimeOffScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"SELECT pg_xact_commit_timestamp('3'::xid)",
			"SELECT pg_xact_commit_timestamp_origin('3'::xid)",
			"SELECT pg_last_committed_xact()",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferenceCommitTimeSpecialIDs(t *testing.T) {
	commitTimeOffScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, input := range []string{"NULL", "'1'::xid", "'2'::xid"} {
				var record any = "(,)"
				if input == "NULL" {
					record = nil
				}
				s := fixture.Select("SELECT pg_xact_commit_timestamp("+input+"), pg_xact_commit_timestamp_origin("+input+")::text", []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{nil, record}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			for _, name := range []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"} {
				s := fixture.ImplicitError("SELECT "+name+"('0'::xid)", fixture.Diagnostic{Code: "22023", Message: "cannot retrieve commit timestamp for transaction 0"})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_catalog.pg_xact_commit_timestamp_origin(xid => '1')::text", []string{"pg_xact_commit_timestamp_origin"}, []string{"TEXT"}, [][]any{{"(,)"}}))
	})
}

func TestReferenceCommitTimeResolution(t *testing.T) {
	commitTimeOffScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		cases := []struct{ call, signature string }{
			{"pg_xact_commit_timestamp()", "pg_xact_commit_timestamp()"},
			{"pg_xact_commit_timestamp('1'::xid,'2'::xid)", "pg_xact_commit_timestamp(xid, xid)"},
			{"pg_xact_commit_timestamp_origin()", "pg_xact_commit_timestamp_origin()"},
			{"pg_xact_commit_timestamp_origin('1'::xid,'2'::xid)", "pg_xact_commit_timestamp_origin(xid, xid)"},
			{"pg_last_committed_xact('1'::xid)", "pg_last_committed_xact(xid)"},
			{"pg_xact_commit_timestamp(xid => '1')", "pg_xact_commit_timestamp(xid => unknown)"},
			{"pg_xact_commit_timestamp_origin(other => '1')", "pg_xact_commit_timestamp_origin(other => unknown)"},
			{`"PG_LAST_COMMITTED_XACT"()`, "PG_LAST_COMMITTED_XACT()"},
		}
		for _, name := range []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"} {
			for _, typ := range []struct{ expr, printed string }{{"1", "integer"}, {"'1'::text", "text"}, {"'1'::oid", "oid"}, {"'1'::xid8", "xid8"}} {
				cases = append(cases, struct{ call, signature string }{name + "(" + typ.expr + ")", name + "(" + typ.printed + ")"})
			}
		}
		for i, tc := range cases {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Position: "8", Hint: hint})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, tc := range []struct{ q, code, message, pos string }{
			{"SELECT missing_commit_time.pg_last_committed_xact()", "3F000", `schema "missing_commit_time" does not exist`, "8"},
			{"SELECT pg_xact_commit_timestamp('bad')", "22P02", `invalid input syntax for type xid: "bad"`, "33"},
			{"SELECT pg_xact_commit_timestamp('4294967296')", "22003", `value "4294967296" is out of range for type xid`, "33"},
			{"/* é */ SELECT pg_last_committed_xact(1)", "42883", "function pg_last_committed_xact(integer) does not exist", "16"},
		} {
			for _, prepared := range []bool{false, true} {
				d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos}
				if tc.code == "42883" {
					d.Hint = hint
				}
				s := fixture.ImplicitError(tc.q, d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		stmt, err := c.PrepareContext(p.Ctx, "SELECT pg_xact_commit_timestamp($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var value any
		if err := stmt.QueryRowContext(p.Ctx, nil).Scan(&value); err != nil || value != nil {
			t.Fatalf("strict bound NULL: %v %v", value, err)
		}
		err = stmt.QueryRowContext(p.Ctx, "bad").Scan(&value)
		// Typed parameter import uses the shared, redacted parameter Context on
		// both adapters; it is not an engine-created portal or a runtime error.
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type xid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
		fixture.AssertDiagnostic(t, err, &want)
	})
}

func TestReferenceCommitTimeDisabledSmoke(t *testing.T) {
	commitTimeOffScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW track_commit_timestamp", []string{"track_commit_timestamp"}, []string{"TEXT"}, [][]any{{"off"}}))
		s := fixture.ImplicitError("SET track_commit_timestamp=on", fixture.Diagnostic{Code: "55P02", Message: `parameter "track_commit_timestamp" cannot be changed without restarting the server`})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		// Disabled runtime calls must not be demanded just to describe an empty result.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT * FROM pg_last_committed_xact() LIMIT 0", []string{"xid", "timestamp", "roident"}, []string{"XID", "TIMESTAMPTZ", "OID"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_xact_commit_timestamp(NULL), pg_xact_commit_timestamp_origin('2'::xid)::text", []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{nil, "(,)"}}))
		for _, q := range []string{"SELECT pg_last_committed_xact()", "SELECT pg_xact_commit_timestamp('3'::xid)"} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
