package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// TestReferenceBtreeArrayControls covers the dual-mode maintenance witness for
// schema-qualified text[] btree keys, then the native Dump/Restore controls.
// ORDER BY and equality results prove the comparator and maintained entries;
// they are not evidence that the planner scanned the index.
func TestReferenceBtreeArrayControls(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE SCHEMA portable", 0))
		run(fixture.Exec("CREATE TABLE portable.mail(id integer, labels text[])", 0))
		run(fixture.Exec("CREATE INDEX mail_labels ON portable.mail(labels)", 0))
		run(fixture.Exec("INSERT INTO portable.mail VALUES(1,'{x}'),(2,'{x,y}'),(3,'{a}'),(4,NULL)", 4))

		order := func(rows ...int64) fixture.Step {
			want := make([][]any, len(rows))
			for i, id := range rows {
				want[i] = []any{id}
			}
			return fixture.Select("SELECT id FROM portable.mail ORDER BY labels", []string{"id"}, []string{"INT4"}, want)
		}
		const equalX = "SELECT id FROM portable.mail WHERE labels = '{x}'::text[]"
		heldOrder := classHeld(t, p, conn, "SELECT id FROM portable.mail ORDER BY labels")
		heldEqual := classHeld(t, p, conn, equalX)
		check := func(rows ...int64) {
			t.Helper()
			s := order(rows...)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, heldOrder, b.Recorder, s)
		}
		checkEqual := func(want [][]any) {
			t.Helper()
			s := fixture.Select(equalX, []string{"id"}, []string{"INT4"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, heldEqual, b.Recorder, s)
		}
		check(3, 1, 2, 4)
		checkEqual([][]any{{int64(1)}})

		run(fixture.Exec("UPDATE portable.mail SET labels='{b}' WHERE id=2", 1))
		check(3, 2, 1, 4)
		run(fixture.Exec("DELETE FROM portable.mail WHERE id=1", 1))
		check(3, 2, 4)
		checkEqual(nil)

		if b.PG {
			return
		}
		dump := fixture.CheckDump(t, p)
		const indexDDL = `CREATE INDEX "mail_labels" ON "portable"."mail" USING btree ("labels" ASC NULLS LAST);`
		if !strings.Contains(string(dump), indexDDL) {
			t.Fatal("Dump omitted implicit btree array index", string(dump))
		}
		for _, unwanted := range []string{"array_ops", "CREATE TEMP", " DESC"} {
			if strings.Contains(string(dump), unwanted) {
				t.Fatalf("Dump emitted explicit %q: %s", unwanted, dump)
			}
		}

		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/btree-array-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		const destinationOrder = "SELECT id FROM portable.mail ORDER BY labels"
		other(fixture.Select(destinationOrder, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(4)}}))
		other(fixture.Exec("INSERT INTO portable.mail VALUES(5,'{x,z}')", 1))
		other(fixture.Select("SELECT id FROM portable.mail WHERE labels = ARRAY['x','z']", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}))
		other(fixture.Select(destinationOrder, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(5)}, {int64(4)}}))
		other(fixture.Exec("UPDATE portable.mail SET labels='{c}' WHERE id=3", 1))
		other(fixture.Exec("DELETE FROM portable.mail WHERE id=4", 1))
		other(fixture.Select(destinationOrder, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}}))

		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id FROM portable.mail ORDER BY labels", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(4)}}))
		run(fixture.Select("SELECT id FROM portable.mail WHERE labels = '{b}'::text[]", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		before := fixture.CheckDump(t, p)
		broken := string(before) + "\nCREATE INDEX broken ON portable.mail USING btree(missing);\n"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(broken))
		got := fixture.ExtractDiagnostic(t, err)
		if got.Code != "42703" || got.Message != `column "missing" does not exist` {
			t.Fatalf("failed Restore diagnostic got %#v", got)
		}
		if after := fixture.CheckDump(t, p); !bytes.Equal(before, after) {
			t.Fatal("failed Restore changed the incumbent Dump")
		}
	})
}