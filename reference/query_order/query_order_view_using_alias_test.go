package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderViewUsingAliasNamespaces(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"reserved", "SELECT nextval('s') AS n,__view_using_0.id FROM a AS __view_using_0 JOIN b AS __view_using_0_1 USING(k,j) ORDER BY k,j,__view_using_0.id"},
		{"explicit", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) AS merged ORDER BY merged.k,merged.j,a.id"},
		{"whole_join", "SELECT nextval('s') AS n,q.id FROM (a JOIN b USING(k,j)) AS q(k,j,id) ORDER BY q.k,q.j,q.id"},
		{"nested", "SELECT nextval('s') AS n,a.id FROM (a JOIN b USING(k,j)) JOIN c USING(k,j) ORDER BY k,j,a.id"},
		{"natural", "SELECT nextval('s') AS n,a.id FROM a NATURAL JOIN b ORDER BY k,j,a.id"},
		{"mixed_slots", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) ORDER BY k,j,a.id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			jWidth := "3"
			if tc.name == "mixed_slots" {
				jWidth = "2"
			}
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE a(id INT,k VARCHAR(2),j VARCHAR(2))", 0), fixture.Exec("CREATE TABLE b(k VARCHAR(3),j VARCHAR("+jWidth+"))", 0), fixture.Exec("CREATE TABLE c(k VARCHAR(4),j VARCHAR(4))", 0),
				fixture.Exec("INSERT INTO a VALUES(1,'b','z'),(2,'a','y')", 2), fixture.Exec("INSERT INTO b VALUES('a','y'),('b','z')", 2), fixture.Exec("INSERT INTO c VALUES('a','y'),('b','z')", 2),
				fixture.Exec("CREATE SEQUENCE s START WITH 1", 0), fixture.Exec("CREATE VIEW collision(k,id) AS "+tc.body, 0),
			} {
				p.Step(t, step)
			}
			for _, backend := range p.Backends() {
				db := backend.DB
				var definition string
				definitionSource := db
				if tc.name == "nested" {
					// Pinned PostgreSQL18.0's own pg_views definition emits
					// bare ORDER k here, retargeting the hidden merged key to
					// the overridden volatile output. Its original view has
					// the independently asserted pairs below; its own replay
					// does not. Test the selected native serializer instead:
					// execute the same unmodified native definition on both
					// engines, preserving exact rows/types/effects assertions.
					definitionSource = p.Memory
				}
				if err := definitionSource.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(p.Ctx, "CREATE VIEW replay AS "+definition); err != nil {
					t.Fatal(definition, err)
				}
				if tc.name == "nested" {
					var own string
					if err := db.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&own); err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(p.Ctx, "CREATE VIEW own_export AS "+own); err != nil {
						t.Fatal(own, err)
					}
					for _, prepared := range []bool{false, true} {
						fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
						want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
						if backend.PG {
							// PG18's own export retargets hidden ORDER k to
							// nextval output; retain its independently specified bug.
							want = [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}
						}
						step := fixture.Select("SELECT k,id FROM own_export ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, want)
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, db, backend.Recorder, step)
						var next int64
						if err := db.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
							t.Fatal(next, err)
						}
					}
					fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
				}
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"collision", "replay"} {
				for _, prepared := range []bool{false, true} {
					p.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
					step := fixture.Select("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
					step.Prepared = prepared
					p.Step(t, step)
					p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				}
			}
			for _, prepared := range []bool{false, true} {
				restored := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(restored.Ctx, restored.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"collision", "replay"} {
					if name == "replay" {
						restored.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
					}
					step := fixture.Select("SELECT k,id FROM "+name+" ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
					step.Prepared = prepared
					restored.Step(t, step)
					restored.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				}
			}
		})
	}
}
