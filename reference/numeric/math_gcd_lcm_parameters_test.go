package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathGcdLcmParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, name := range []string{"gcd", "lcm"} {
			for _, w := range []struct{ peer, cast, typ, word string }{{"18", "integer", "INT4", "integer"}, {"18::bigint", "bigint", "INT8", "bigint"}, {"18.00", "numeric", "NUMERIC", "numeric"}} {
				t.Run(name+w.cast, func(t *testing.T) {
					q := "SELECT " + name + "($1," + w.peer + ") AS result"
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{conn, stmt, q}
					for _, frame := range []struct {
						input any
						g, l  int64
					}{{int64(-12), 6, 36}, {"9", 9, 18}, {nil, 0, 0}, {int32(12), 6, 36}} {
						var want any = frame.g
						if name == "lcm" {
							want = frame.l
						}
						if w.typ == "NUMERIC" {
							want = fmt.Sprintf("%d.00", want)
						}
						if frame.input == nil {
							want = nil
						}
						s := fixture.Select(q, []string{"result"}, []string{w.typ}, [][]any{{want}}, frame.input)
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG) }
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					bad := referenceMathGcdLcmFailure(q, "22P02", fmt.Sprintf("invalid input syntax for type %s: %q", w.word, "oops"))
					bad.Args = []any{"oops"}
					bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
					var want any = int64(6)
					if name == "lcm" {
						want = int64(36)
					}
					if w.typ == "NUMERIC" {
						want = fmt.Sprintf("%d.00", want)
					}
					s := fixture.Select(q, []string{"result"}, []string{w.typ}, [][]any{{want}}, int64(12))
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG) }
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					if name == "lcm" && w.typ != "NUMERIC" {
						max := "2147483647"
						message := "integer out of range"
						if w.typ == "INT8" {
							max, message = "9223372036854775807", "bigint out of range"
						}
						failure := referenceMathGcdLcmFailure(q, "22003", message)
						failure.Args = []any{max}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					run(fixture.Exec("PREPARE gl("+w.cast+") AS SELECT "+name+"($1,"+w.peer+") AS result", 0))
					for _, input := range []string{"12", "NULL", "-12"} {
						value := want
						if input == "NULL" {
							value = nil
						}
						s := fixture.Select("EXECUTE gl("+input+")", []string{"result"}, []string{w.typ}, [][]any{{value}})
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG) }
						run(s)
					}
					failure := fixture.ImplicitError("EXECUTE gl('oops')", fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for type %s: %q", w.word, "oops"), Position: "12"})
					failure.SelectRows, failure.ErrorStage = true, "execute"
					run(failure)
					run(fixture.Select("EXECUTE gl(12)", []string{"result"}, []string{w.typ}, [][]any{{want}}))
					run(fixture.Exec("DEALLOCATE gl", 0))
				})
			}
		}
	})
}
