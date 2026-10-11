package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderViewOverrideRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,b INT)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,2),(2,1)", 2),
		fixture.Exec("CREATE SEQUENCE s START WITH 1", 0),
		fixture.Exec("CREATE VIEW collision(b,id) AS SELECT nextval('s') AS n,id FROM t ORDER BY b,id", 0),
	} {
		p.Step(t, step)
	}
	for _, backend := range p.Backends() {
		db := backend.DB
		var definition string
		if err := db.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(p.Ctx, "CREATE VIEW replay AS "+definition); err != nil {
			t.Fatal(definition, err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"collision", "replay"} {
		for _, prepared := range []bool{false, true} {
			p.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
			// The hidden physical b key admits id2 before id1 to volatile output
			// evaluation. Outer id ordering fixes the independently expected pairs.
			step := fixture.Select("SELECT b,id FROM "+name+" ORDER BY id", []string{"b", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
			step.Prepared = prepared
			p.Step(t, step)
			for _, backend := range p.Backends() {
				var next int64
				if err := backend.DB.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
					t.Fatal(next, err)
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("ALTER SEQUENCE s RESTART WITH 3", 0))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
		}
	}
	restored := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(restored.Ctx, restored.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"collision", "replay"} {
		for _, prepared := range []bool{false, true} {
			if name == "collision" && prepared {
				restored = fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(restored.Ctx, restored.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			// The first restored read must witness pristine is_called=false;
			// a corrective restart would conceal a serializer regression.
			if name != "collision" {
				restored.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
			}
			step := fixture.Select("SELECT b,id FROM "+name+" ORDER BY id", []string{"b", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
			step.Prepared = prepared
			restored.Step(t, step)
			var next int64
			if err := restored.Memory.QueryRowContext(restored.Ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
				t.Fatal(next, err)
			}
			restored.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 3", 0))
			restored.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
	}
}
