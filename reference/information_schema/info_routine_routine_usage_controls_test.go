package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineRoutineUsageControls(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruExec(t, p, b, c, "CREATE FUNCTION rru_control_leaf() RETURNS integer RETURN 7",
			"CREATE FUNCTION rru_control_use(a integer DEFAULT rru_control_leaf()) RETURNS integer RETURN rru_control_leaf()+a",
			"CREATE FUNCTION rru_control_quote(a integer DEFAULT rru_control_leaf()) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION rru_control_pl(a integer DEFAULT rru_control_leaf()) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$",
			"CREATE FUNCTION rru_control_text() RETURNS integer LANGUAGE SQL AS 'SELECT rru_control_leaf()'",
			"CREATE FUNCTION rru_control_pl_text() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN rru_control_leaf(); END$$")
		expected := func(conn *sql.Conn) [][]any {
			leaf := rruID(t, p, conn, "public", "rru_control_leaf", "")
			var want [][]any
			for _, name := range []string{"rru_control_use", "rru_control_quote", "rru_control_pl"} {
				want = append(want, rruRow(database, "public", rruID(t, p, conn, "public", name, "integer"), "public", leaf))
			}
			return want
		}
		want := expected(c)
		rruCheck(t, p, b, c, "specific_schema='public'", want)
		if b.PG {
			return
		}
		retained, err := c.QueryContext(p.Ctx, rruProjection(false, "specific_schema='public'", nil).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		rruMetadata(t, columns, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		rruCheck(t, p, b, c, "specific_schema='public'", nil)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rruCheck(t, p, b, c, "specific_schema='public'", expected(c))
		got := map[string]int{}
		for retained.Next() {
			var a, b, c, d, e, f string
			if err := retained.Scan(&a, &b, &c, &d, &e, &f); err != nil {
				t.Fatal(err)
			}
			got[c+"/"+f]++
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		retained.Close()
		rruMetadata(t, columns, false)
		if len(got) != 3 {
			t.Fatal("retained pair bag", got)
		}
		for _, row := range want {
			if got[row[2].(string)+"/"+row[5].(string)] != 1 {
				t.Fatal("old cursor changed", got, want)
			}
		}
		unchanged := func() {
			rruCheck(t, p, b, c, "specific_schema='public'", expected(c))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after.Bytes(), dump.Bytes()) {
				t.Fatal("failed restore changed catalog")
			}
		}
		prefix := dump.String() + "\nCREATE TABLE rru_control_bad(v integer); INSERT INTO rru_control_bad VALUES ("
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "not-an-integer"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"'not-an-integer');"))
		fixture.AssertDiagnostic(t, err, &diagnostic)
		unchanged()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		unchanged()
		destination, err := stuntgres.NewDriver().OpenConnector("rru-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(destination)
		defer db.Close()
		recorded, recorder := fixture.RecordedNativePool(t, destination)
		destinationBackend := b
		destinationBackend.DB = recorded
		destinationBackend.Recorder = recorder
		destinationBackend.PG = false
		if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		dest, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := dest.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		var destDatabase string
		if err := dest.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&destDatabase); err != nil {
			t.Fatal(err)
		}
		destinationWant := expected(dest)
		for _, row := range destinationWant {
			row[0] = destDatabase
			row[3] = destDatabase
		}
		rruCheck(t, p, destinationBackend, dest, "specific_schema='public'", destinationWant)
		rruExec(t, p, destinationBackend, dest, "CREATE ROLE rru_other", "ALTER FUNCTION rru_control_leaf() OWNER TO rru_other", "CREATE ROLE rru_reader", "SET SESSION AUTHORIZATION rru_reader")
		rruCheck(t, p, destinationBackend, dest, "specific_schema='public'", nil)
		rruExec(t, p, destinationBackend, dest, "RESET SESSION AUTHORIZATION")
	})
}
