package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineQuotedReplayAtomicity(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "missing relation"
		if mismatch {
			name = "empty source result mismatch"
		}
		t.Run(name, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Public oracle witness starts valid; only ordinary lawful DDL
				// makes the soft body unusable. Result checking cannot depend on
				// whether the newly created text source produces any rows.
				routineExec(t, p, b, c, "CREATE TABLE qr_source(x integer)", "CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM qr_source$$", "DROP TABLE qr_source")
				code, message, detail := "42P01", "relation \"qr_source\" does not exist", ""
				if mismatch {
					routineExec(t, p, b, c, "CREATE TABLE qr_source(x text)")
					code, message, detail = "42P13", "return type mismatch in function declared to return integer", "Actual return type is text."
				}
				q := "SELECT qr_bad() AS v"
				position := "15"
				if mismatch {
					position = ""
				}
				quotedReplayFailure(t, p, b, c, nil, q, "qr_bad", "SELECT x FROM qr_source", position, code, message, detail, "")
				want := quotedReplayDiagnostic(b.PG, "qr_bad", "SELECT x FROM qr_source", position, code, message, detail, "", false)
				routineExec(t, p, b, c, "CREATE TABLE qr_destination(x integer DEFAULT qr_bad())")
				s := fixture.ErrorDiagnostic("INSERT INTO qr_destination DEFAULT VALUES", want)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT x FROM qr_destination", []string{"x"}, []string{"INT4"}, nil))
				if b.PG {
					return
				}
				routineExec(t, p, b, c, "CREATE TABLE qr_sentinel(v integer)", "SET application_name='quoted-target'")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qr_sentinel VALUES(77)", 1))
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				held, err := c.PrepareContext(p.Ctx, "SELECT v FROM qr_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				old, err := c.QueryContext(p.Ctx, "SELECT v FROM qr_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				prefix := "SET check_function_bodies = false; CREATE TABLE must_not_publish(v integer); "
				if mismatch {
					prefix += "CREATE TABLE qr_source(x text); "
				}
				prefix += "CREATE FUNCTION qr_bad() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM qr_source$$; CREATE TABLE qr_destination(x integer DEFAULT qr_bad()); "
				assertUnchanged := func() {
					t.Helper()
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("failed Restore changed target catalog/data")
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"quoted-target"}}))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: "SELECT v FROM qr_sentinel"}, b.Recorder, fixture.Select("SELECT v FROM qr_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='must_not_publish'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"INSERT INTO qr_destination DEFAULT VALUES;"))
				fixture.AssertDiagnostic(t, err, &want)
				assertUnchanged()
				// Written routine calls are outside replay scalar admission;
				// this is not a routine execution diagnostic.
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"INSERT INTO qr_destination VALUES(qr_bad());"))
				var state interface{ SQLState() string }
				if err == nil || err.Error() != "stuntgres: invalid restore scalar" || errors.As(err, &state) {
					t.Fatal("written scalar admission", err)
				}
				assertUnchanged()
				for _, tc := range []struct{ tail, code, message, token string }{
					{"ALTER FUNCTION qr_bad() OWNER TO qr_missing_owner;", "42704", "role \"qr_missing_owner\" does not exist", ""},
					{"CREATE FUNCTION qr_hard() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT v FROM qr_missing; END;", "42P01", "relation \"qr_missing\" does not exist", "qr_missing;"},
				} {
					stream := prefix + tc.tail
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.token != "" {
						d.Position = fixture.Position(strings.Index(stream, tc.token) + 1)
					}
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)), &d)
					assertUnchanged()
				}
				readFailure := errors.New("quoted replay reader failure")
				err = stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(prefix), fixture.CatalogControlFailingReader{Err: readFailure}))
				if !errors.Is(err, readFailure) || errors.As(err, &state) {
					t.Fatal("reader failure", err)
				}
				assertUnchanged()
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(prefix)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				assertUnchanged()
				fixture.CatalogControlNativeValues(t, old, 77)
			})
		})
	}
}
