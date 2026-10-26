package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func quotedReplayDeclarationDiagnostic(q, code, message, token string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	if token != "" {
		d.Position = fixture.Position(strings.Index(q, token) + 1)
	}
	if code == "42883" {
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	}
	return d
}

func quotedReplayFormalDiagnostic(q, body string) fixture.Diagnostic {
	if body == "SELECT 1 +" {
		d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(strings.Index(q, body) + len(body) + 1)
		return d
	}
	slot := "$1"
	if strings.Contains(body, "$2") {
		slot = "$2"
	}
	return quotedReplayDeclarationDiagnostic(q, "42P02", "there is no parameter "+slot, slot)
}

func TestReferenceRoutineQuotedReplayAdmission(t *testing.T) {
	// Enabled public CREATE independently exercises the syntax/formal boundary.
	// PostgreSQL's disabled validator does NOT parse string bodies; the stricter
	// reconstruction checks below therefore are explicit native-control phases.
	for _, body := range []string{"SELECT $1", "SELECT $2", "SELECT 1 +", "SELECT (SELECT $1), rd_unit_missing(1)"} {
		t.Run(body, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL AS $$" + body + "$$"
				s := fixture.ErrorDiagnostic(q, quotedReplayFormalDiagnostic(q, body))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		})
	}
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE FUNCTION qr_kept() RETURNS integer RETURN 7")
		for _, tc := range []struct{ sql, code, message, token string }{
			{"CREATE FUNCTION qr_bad(qr_missing_type) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$", "42704", "type qr_missing_type does not exist", "qr_missing_type"},
			{"CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE qr_missing_language AS $$SELECT 7$$", "42704", "language \"qr_missing_language\" does not exist", ""},
			{"ALTER FUNCTION qr_kept() OWNER TO qr_missing_owner", "42704", "role \"qr_missing_owner\" does not exist", ""},
			{"CREATE FUNCTION qr_kept() RETURNS integer RETURN 8", "42723", "function \"qr_kept\" already exists with same argument types", ""},
			{"CREATE FUNCTION qr_bad(v integer DEFAULT qr_missing_default()) RETURNS integer RETURN v", "42883", "function qr_missing_default() does not exist", "qr_missing_default"},
			{"CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT x FROM qr_missing_table; END", "42P01", "relation \"qr_missing_table\" does not exist", "qr_missing_table"},
		} {
			d := quotedReplayDeclarationDiagnostic(tc.sql, tc.code, tc.message, tc.token)
			s := fixture.ErrorDiagnostic(tc.sql, d)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		if b.PG {
			return
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		prefix := "SET check_function_bodies = false; CREATE FUNCTION qr_soft() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM qr_absent$$; "
		for _, tc := range []struct{ tail, body, code, message, token string }{
			{"CREATE FUNCTION rd_bad_replay() RETURNS integer LANGUAGE SQL AS $$SELECT rd_missing(1), $1$$;", "SELECT rd_missing(1), $1", "", "", ""},
			{"CREATE FUNCTION rd_bad_replay() RETURNS integer LANGUAGE SQL AS $$SELECT rd_missing(1), $2$$;", "SELECT rd_missing(1), $2", "", "", ""},
			{"CREATE FUNCTION rd_bad_replay() RETURNS integer LANGUAGE SQL AS $$SELECT 1 +$$;", "SELECT 1 +", "", "", ""},
			{"CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL AS $$SELECT (SELECT $1), rd_unit_missing(1)$$;", "SELECT (SELECT $1), rd_unit_missing(1)", "", "", ""},
			{"CREATE FUNCTION qr_bad(qr_missing_type) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$;", "", "42704", "type qr_missing_type does not exist", "qr_missing_type"},
			{"CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE qr_missing_language AS $$SELECT 7$$;", "", "42704", "language \"qr_missing_language\" does not exist", ""},
			{"ALTER FUNCTION qr_soft() OWNER TO qr_missing_owner;", "", "42704", "role \"qr_missing_owner\" does not exist", ""},
			{"CREATE FUNCTION qr_soft() RETURNS integer RETURN 8;", "", "42723", "function \"qr_soft\" already exists with same argument types", ""},
			{"CREATE FUNCTION qr_bad(v integer DEFAULT qr_missing_default()) RETURNS integer RETURN v;", "", "42883", "function qr_missing_default() does not exist", "qr_missing_default"},
			{"CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT x FROM qr_missing_table; END;", "", "42P01", "relation \"qr_missing_table\" does not exist", "qr_missing_table"},
		} {
			stream := prefix + tc.tail
			d := quotedReplayDeclarationDiagnostic(stream, tc.code, tc.message, tc.token)
			if tc.body != "" {
				d = quotedReplayFormalDiagnostic(stream, tc.body)
			}
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)), &d)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("invalid declaration partially published", tc.tail)
			}
			routineValue(t, p, b, c, "SELECT qr_kept() AS v", "v", "INT4", int64(7))
		}
	})
}
