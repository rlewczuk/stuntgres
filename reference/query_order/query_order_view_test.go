package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderViewRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,a INT)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,2),(2,1),(3,NULL)", 3),
		fixture.Exec("CREATE VIEW alias_order(k,id) AS SELECT a+0 AS x,id FROM t ORDER BY x DESC NULLS LAST,id", 0),
		fixture.Exec("CREATE VIEW hidden_order AS SELECT id FROM t ORDER BY COALESCE(a,0)+1 DESC NULLS LAST,id", 0),
		fixture.Exec("CREATE VIEW ordinal_order AS SELECT id,a FROM t ORDER BY 2 NULLS FIRST,1 DESC", 0),
		fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0),
	} {
		p.Step(t, step)
	}
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, name := range []string{"alias_order", "hidden_order", "ordinal_order"} {
			var definition string
			if err := db.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(p.Ctx, "CREATE VIEW replay_"+name+" AS "+definition); err != nil {
				t.Fatal(name, definition, err)
			}
		}
	}
	for _, step := range queryOrderViewReadSteps() {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE SEQUENCE alias_sequence START WITH 1", 0),
		fixture.Exec("CREATE SEQUENCE hidden_sequence START WITH 1", 0),
		fixture.Exec("CREATE VIEW volatile_alias AS SELECT nextval('alias_sequence') AS n FROM t ORDER BY n DESC", 0),
		fixture.Exec("CREATE VIEW volatile_hidden AS SELECT id FROM t ORDER BY nextval('hidden_sequence') DESC", 0),
		fixture.Select("SELECT n FROM volatile_alias ORDER BY n DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}),
		fixture.Select("SELECT id FROM volatile_hidden ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}),
	} {
		p.Step(t, step)
	}
	for _, b := range p.Backends() {
		for _, sequence := range []string{"alias_sequence", "hidden_sequence"} {
			var next int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT nextval('"+sequence+"')").Scan(&next); err != nil || next != 4 {
				t.Fatal(sequence, next, err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER SEQUENCE "+sequence+" RESTART WITH 4", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('"+sequence+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	restored := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(restored.Ctx, restored.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, step := range queryOrderViewReadSteps() {
		restored.Step(t, step)
		restored.Step(t, fixture.Prepared(step))
	}
	for _, b := range p.Backends() {
		db := b.DB
		if !b.PG {
			db = restored.Memory
		}
		stmt, err := db.PrepareContext(p.Ctx, "SELECT n FROM volatile_alias ORDER BY n DESC")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.Ctx)
			fixture.DeleteReferenceConsume(t, b.PG, rows, err, fixture.Select("", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}}))
		}()
		var next int64
		if err := db.QueryRowContext(p.Ctx, "SELECT nextval('alias_sequence')").Scan(&next); err != nil || next != 8 {
			t.Fatal(next, err)
		}
	}
}

func queryOrderViewReadSteps() []fixture.Step {
	var steps []fixture.Step
	for _, prefix := range []string{"", "replay_"} {
		steps = append(steps,
			fixture.Select("SELECT k,id FROM "+prefix+"alias_order ORDER BY id", []string{"k", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {nil, int64(3)}}),
			fixture.Select("SELECT id FROM "+prefix+"hidden_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}),
			fixture.Select("SELECT id,a FROM "+prefix+"ordinal_order ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}),
		)
	}
	return steps
}
