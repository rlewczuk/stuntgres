package reference_test

import (
	"bytes"

	"stuntgres"
	"testing"
)

func TestReferenceQueryOrderViewOverrideRegression(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,b INT)", 0),
		referenceExec("INSERT INTO t VALUES(1,2),(2,1)", 2),
		referenceExec("CREATE SEQUENCE s START WITH 1", 0),
		referenceExec("CREATE VIEW collision(b,id) AS SELECT nextval('s') AS n,id FROM t ORDER BY b,id", 0),
	} {
		p.step(t, step)
	}
	for _, backend := range p.backends() {
		db := backend.db
		var definition string
		if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay AS "+definition); err != nil {
			t.Fatal(definition, err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"collision", "replay"} {
		for _, prepared := range []bool{false, true} {
			p.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			// The hidden physical b key admits id2 before id1 to volatile output
			// evaluation. Outer id ordering fixes the independently expected pairs.
			step := referenceSelect("SELECT b,id FROM "+name+" ORDER BY id", []string{"b", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
			step.prepared = prepared
			p.step(t, step)
			for _, backend := range p.backends() {
				var next int64
				if err := backend.db.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
					t.Fatal(next, err)
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("ALTER SEQUENCE s RESTART WITH 3", 0))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
		}
	}
	restored := newReferenceFixture(t, false, true)
	if err := stuntgres.Restore(restored.ctx, restored.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"collision", "replay"} {
		for _, prepared := range []bool{false, true} {
			if name == "collision" && prepared {
				restored = newReferenceFixture(t, false, true)
				if err := stuntgres.Restore(restored.ctx, restored.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			// The first restored read must witness pristine is_called=false;
			// a corrective restart would conceal a serializer regression.
			if name != "collision" {
				restored.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			}
			step := referenceSelect("SELECT b,id FROM "+name+" ORDER BY id", []string{"b", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
			step.prepared = prepared
			restored.step(t, step)
			var next int64
			if err := restored.memory.QueryRowContext(restored.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
				t.Fatal(next, err)
			}
			restored.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 3", 0))
			restored.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
	}
}
