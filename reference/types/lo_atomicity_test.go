package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoAtomicity(t *testing.T) {
	loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loInstall(t, p, b, c)
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_atomic(id integer NOT NULL,v lo)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_atomic VALUES (1,'7')", 1))
		verify := func() {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,v FROM lo_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(1), int64(7)}})
				s.Prepared = prepared
				loRows(t, p, b, c, s)
			}
		}
		for _, tc := range []struct{ input, code string }{{"bad", "22P02"}, {"4294967296", "22003"}} {
			for _, prepared := range []bool{false, true} {
				q := "INSERT INTO lo_atomic VALUES (2,'26'),(3,'" + tc.input + "')"
				d := loInputDiagnostic(tc.input, tc.code)
				d.Position = strconv.Itoa(strings.LastIndex(q, "'") + 1 - len(tc.input) - 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				loRun(t, p, b, c, s)
				verify()
				q = "INSERT INTO lo_atomic VALUES (2,$1),(3,$2)"
				s = loBoundError(q, tc.input, tc.code, "2", "26", tc.input)
				s.Prepared = prepared
				loRun(t, p, b, c, s)
				verify()
			}
		}
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_atomic VALUES (2,'26')", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("UPDATE lo_atomic SET v=(CASE WHEN id=1 THEN '8' ELSE 'bad' END)::lo", loInputDiagnostic("bad", "22P02"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			loRun(t, p, b, c, s)
			loRows(t, p, b, c, fixture.Select("SELECT id,v FROM lo_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(26)}}))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "INSERT INTO lo_atomic VALUES (3,$1)", "4294967295")
			var state interface{ SQLState() string }
			if result != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary pre-canceled lo insert", result, err)
			}
			loRows(t, p, b, c, fixture.Select("SELECT id,v FROM lo_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(26)}}))
			loIdentity(t, p, b, c)
		}
	})
}

func TestReferenceLoNotNullAtomicity(t *testing.T) {
	loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loInstall(t, p, b, c)
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_required_atomic(id integer NOT NULL,v lo NOT NULL)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_required_atomic VALUES (1,'7')", 1))
		d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "lo_required_atomic" violates not-null constraint`)
		d.Detail = "Failing row contains (3, null)."
		d.Schema, d.Table, d.Column = "public", "lo_required_atomic", "v"
		for _, prepared := range []bool{false, true} {
			for _, bound := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO lo_required_atomic VALUES (2,'26'),(3,NULL)", d)
				if bound {
					s = fixture.ErrorDiagnostic("INSERT INTO lo_required_atomic VALUES (2,$1),(3,$2)", d, "26", nil)
				}
				s.Prepared = prepared
				s.ErrorStage = "execute"
				loRun(t, p, b, c, s)
				// The earlier valid tuple is part of the failed statement, not
				// a published prefix. The independently seeded row stays exact.
				for _, readPrepared := range []bool{false, true} {
					s := fixture.Select("SELECT id,v FROM lo_required_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(1), int64(7)}})
					s.Prepared = readPrepared
					loRows(t, p, b, c, s)
					s = fixture.Select("SELECT v FROM lo_required_atomic WHERE id>1 ORDER BY id", []string{"v"}, []string{"OID"}, [][]any{})
					s.Prepared = readPrepared
					loMetadataRows(t, p, b, c, s, []bool{false})
				}
			}
		}
	})
}
