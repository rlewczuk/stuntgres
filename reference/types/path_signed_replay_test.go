package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePathSignedSpecialReplay(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Display follows PostgreSQL canonicalization, not representation identity.
		for _, prepared := range []bool{false, true} {
			for _, tc := range [][2]string{
				{"[(-NaN,1)]", "[(NaN,1)]"},
				{"((1,-NaN),(+NaN,-Infinity))", "((1,NaN),(NaN,-Infinity))"},
				{"[(+Infinity,-Inf)]", "[(Infinity,-Infinity)]"},
			} {
				for _, q := range []string{"SELECT path " + boxQuote(tc[0]) + " AS p", "SELECT $1::path AS p"} {
					var args []any
					if strings.Contains(q, "$1") {
						args = []any{tc[0]}
					}
					s := pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{tc[1]}}, args...)
					s.Prepared = prepared
					run(s)
				}
			}
		}
		run(fixture.Exec("CREATE TABLE path_signed(id integer,p path DEFAULT path '[(-NaN,22)]')", 0))
		run(fixture.Exec("CREATE VIEW path_signed_view AS SELECT path '((33,-NaN))' AS p", 0))
		run(fixture.Exec("INSERT INTO path_signed VALUES(1,'[(-NaN,11)]')", 1))
		run(fixture.Exec("INSERT INTO path_signed(id) VALUES(2)", 1))
		checks := []fixture.Step{
			pathSelect(b, "SELECT id,p FROM path_signed ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, [][]any{{int64(1), "[(NaN,11)]"}, {int64(2), "[(NaN,22)]"}}),
			pathSelect(b, "SELECT p FROM path_signed_view", []string{"p"}, []string{"PATH"}, [][]any{{"((33,NaN))"}}),
		}
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		if b.PG {
			return
		} // PostgreSQL has no native host replacement API.

		// SQL display intentionally hides NaN signs. Public Dump serialization is
		// therefore the consumer-visible exact-representation witness, not source
		// text inspection or a PostgreSQL physical-datum comparison.
		assertDump := func(data []byte) {
			t.Helper()
			for _, exact := range []string{"[(-NaN,11)]", "[(-NaN,22)]", "((33,-NaN))"} {
				if !bytes.Contains(data, []byte(exact)) {
					t.Fatalf("logical dump lost signed PATH coordinate %q: %s", exact, data)
				}
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		assertDump(dump.Bytes())
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			run(s)
		}
		var same bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil {
			t.Fatal(err)
		}
		assertDump(same.Bytes())
		if !bytes.Equal(dump.Bytes(), same.Bytes()) {
			t.Fatalf("same-driver signed PATH replay changed serialization\nbefore:\n%s\nafter:\n%s", dump.Bytes(), same.Bytes())
		}

		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
		}
		// Exercise the restored default, not merely its printed definition.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO path_signed(id) VALUES(3)", 1))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, pathSelect(b, "SELECT p FROM path_signed WHERE id=3", []string{"p"}, []string{"PATH"}, [][]any{{"[(NaN,22)]"}}))
		var fresh bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &fresh); err != nil {
			t.Fatal(err)
		}
		assertDump(fresh.Bytes())
		if bytes.Count(fresh.Bytes(), []byte("[(-NaN,22)]")) <= bytes.Count(dump.Bytes(), []byte("[(-NaN,22)]")) {
			t.Fatal("restored default lost signed NaN when evaluated")
		}
	})
}
