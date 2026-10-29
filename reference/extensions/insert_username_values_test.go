package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func insertUsernameValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	insertUsernameSetup(t, p, b, c)
	run := insertUsernameRun(t, p, b, c)
	q := "INSERT INTO iu_data(id,actor,note) VALUES($1,$2,$3) RETURNING actor"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run(fixture.Exec("SET ROLE iu_writer", 0))
	run(fixture.Exec("INSERT INTO iu_data(id) VALUES(1)", 1))
	for i, v := range []any{nil, "forged", "", " ", "\t"} {
		note := []string{"two", "three", "four", "five", "six"}[i]
		s := fixture.Select(q, []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_writer"}}, int64(i+2), v, note)
		if i%2 == 0 { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) } else { run(fixture.Prepared(s)) }
	}
	want := [][]any{{int64(1), "iu_writer", nil}, {int64(2), "iu_writer", "two"}, {int64(3), "iu_writer", "three"}, {int64(4), "iu_writer", "four"}, {int64(5), "iu_writer", "five"}, {int64(6), "iu_writer", "six"}}
	s := insertUsernameRows("SELECT id,actor,note FROM iu_data ORDER BY id", want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 3 { t.Fatal(cols, err) }
		for i, col := range cols {
			wantScan := reflect.TypeFor[string]()
			if i == 0 { wantScan = reflect.TypeFor[int64](); if b.PG { wantScan = reflect.TypeFor[int32]() } }
			if col.ScanType() != wantScan { t.Fatalf("column %d ScanType %v, want %v", i, col.ScanType(), wantScan) }
			if !b.PG { nullable, known := col.Nullable(); if !known || nullable != (i != 1) { t.Fatalf("column %d nullability %v/%v", i, nullable, known) } }
		}
	}
	insertUsernameBoth(t, p, b, c, s)
	update := "UPDATE iu_data SET note=$2 WHERE id=$1 RETURNING actor"
	u, err := c.PrepareContext(p.Ctx, update)
	if err != nil { t.Fatal(err) }
	defer u.Close()
	uh := fixture.HeldStatement{Executor: c, Statement: u, SQL: update}
	run(fixture.Exec("SET ROLE iu_other", 0))
	fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Select(update, []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_other"}}, int64(2), "changed"))
	run(fixture.Exec("UPDATE iu_data SET actor=NULL WHERE id=3", 1))
	run(fixture.Prepared(fixture.Exec("UPDATE iu_data SET note=note WHERE id=4", 1)))
	fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Select(update, []string{"actor"}, []string{"TEXT"}, nil, int64(999), nil))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_other"}}, int64(7), nil, nil))
	want[1] = []any{int64(2), "iu_other", "changed"}; want[2][1] = "iu_other"; want[3][1] = "iu_other"
	want = append(want, []any{int64(7), "iu_other", nil})
	insertUsernameBoth(t, p, b, c, insertUsernameRows("SELECT id,actor,note FROM iu_data ORDER BY id", want))
}

func TestReferenceInsertUsernameValues(t *testing.T) { insertUsernameCase(t, insertUsernameValues) }
