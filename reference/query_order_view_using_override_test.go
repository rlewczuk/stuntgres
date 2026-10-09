package reference_test

import (
	"bytes"

	"math"
	"stuntgres"
	"testing"
)

func TestReferenceQueryOrderViewUsingOverrideRegression(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE a(id INT,k VARCHAR(2))", 0),
		referenceExec("CREATE TABLE b(k VARCHAR(3))", 0),
		referenceExec("CREATE SEQUENCE s START WITH 1", 0),
		referenceExec("CREATE VIEW collision(k,id) AS SELECT nextval('s') AS n,__view_using_0.id FROM a AS __view_using_0 JOIN b USING(k) ORDER BY k,__view_using_0.id", 0),
		referenceExec("CREATE VIEW merged_usage AS SELECT k,__view_using_0.id FROM a AS __view_using_0 JOIN b USING(k) ORDER BY k,__view_using_0.id", 0),
	} {
		p.step(t, step)
	}
	for _, backend := range p.backends() {
		db := backend.db
		for _, name := range []string{"collision", "merged_usage"} {
			var definition string
			if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+name+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(p.ctx, "CREATE VIEW replay_"+name+" AS "+definition); err != nil {
				t.Fatal(definition, err)
			}
		}
	}
	for stage := 0; stage < 3; stage++ {
		populated := stage == 2
		if stage == 1 {
			p.step(t, referenceExec("INSERT INTO a VALUES(1,'b'),(2,'a')", 2))
		}
		if populated {
			p.step(t, referenceExec("INSERT INTO b VALUES('a'),('b')", 2))
		}
		if stage > 0 {
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &snapshot); err != nil {
				t.Fatal(err)
			}
			queryOrderUsingRestored(t, snapshot.Bytes(), populated)
		}
		for _, name := range []string{"collision", "replay_collision"} {
			for _, prepared := range []bool{false, true} {
				p.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
				want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
				next := int64(3)
				if !populated {
					want = nil
					next = 1
				}
				step := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, want)
				step.prepared = prepared
				p.step(t, step)
				for _, backend := range p.backends() {
					// Preserve the original QueryRowContext sequence-effect route
					// as well as the existing independently recorded stream.
					var got int64
					if err := backend.db.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&got); err != nil || got != next {
						t.Fatal(got, next, err)
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("ALTER SEQUENCE s RESTART WITH "+referencePosition(int(next)), 0))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
				}
			}
		}
		for _, backend := range p.backends() {
			queryOrderUsingMetadata(t, p, backend, populated)
		}
	}
}

func queryOrderUsingMetadata(t *testing.T, p *referencePair, backend referenceBackend, populated bool) {
	t.Helper()
	for _, prefix := range []string{"", "replay_"} {
		want := [][]any{{"b", int64(1)}, {"a", int64(2)}}
		if !populated {
			want = nil
		}
		step := referenceSelect("SELECT k,id FROM "+prefix+"merged_usage ORDER BY id", []string{"k", "id"}, []string{"VARCHAR", "INT4"}, want)
		for _, prepared := range []bool{false, true} {
			rows, closeQuery, err := transactionQuery(p.ctx, backend.db, step.query, prepared)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer closeQuery()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 || types[0].Name() != "k" || types[1].Name() != "id" || types[0].DatabaseTypeName() != "VARCHAR" || types[1].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				if !backend.pg {
					if length, known := types[0].Length(); !known || length != math.MaxInt64 {
						t.Fatal("lost merged usage", length, known)
					}
				}
				got := make([][]any, 0)
				for rows.Next() {
					var k, id any
					if err := rows.Scan(&k, &id); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{k, id})
				}
				if rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				if _, ok := referenceCompareRows(got, want, false); !ok {
					t.Fatal(got, want)
				}
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
			}()
			step.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
		}
		if backend.pg {
			var typmod int64
			q := "SELECT att.atttypmod FROM pg_catalog.pg_attribute att JOIN pg_catalog.pg_class cls ON att.attrelid=cls.oid WHERE cls.relname='" + prefix + "merged_usage' AND att.attname='k'"
			if err := backend.db.QueryRowContext(p.ctx, q).Scan(&typmod); err != nil || typmod != -1 {
				t.Fatal("PostgreSQL merged typmod", typmod, err)
			}
		}
	}
}

func queryOrderUsingRestored(t *testing.T, snapshot []byte, populated bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		restored := newReferenceFixture(t, false, true)
		if err := stuntgres.Restore(restored.ctx, restored.native, bytes.NewReader(snapshot)); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"collision", "replay_collision"} {
			if name != "collision" {
				restored.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
			}
			want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
			next := int64(3)
			if !populated {
				want, next = nil, 1
			}
			step := referenceSelect("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, want)
			step.prepared = prepared
			restored.step(t, step)
			var got int64
			if err := restored.memory.QueryRowContext(restored.ctx, "SELECT nextval('s')").Scan(&got); err != nil || got != next {
				t.Fatal(got, next, err)
			}
			restored.step(t, referenceExec("ALTER SEQUENCE s RESTART WITH "+referencePosition(int(next)), 0))
			restored.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{next}}))
		}
		queryOrderUsingMetadata(t, restored, restored.backends()[0], populated)
	}
}
