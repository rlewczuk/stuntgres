package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreCompositionPredicates(t *testing.T) {
	cases := []struct {
		predicate string
		ids       []int64
	}{{"d IS NULL", []int64{2}}, {"d ISNULL", []int64{2}}, {"d IS NOT NULL", []int64{1, 3}}, {"d NOTNULL", []int64{1, 3}}, {"f IS TRUE", []int64{1}}, {"f IS FALSE", []int64{2}}, {"f IS UNKNOWN", []int64{3}}, {"f IS NOT TRUE", []int64{2, 3}}, {"b IS DISTINCT FROM sb", []int64{1}}, {"b IS NOT DISTINCT FROM sb", []int64{2, 3}}, {"b IS NOT DISTINCT FROM NULL", []int64{2}}, {"d IS DISTINCT FROM NULL", []int64{1, 3}}}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.predicate, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,f BOOL,d DATE,sd DATE,b BYTEA,sb BYTEA)", 0))
				p.Step(t, fixture.Exec(`INSERT INTO t VALUES(1,10,TRUE,'2000-01-01','2000-02-29','\x41','\x4200'),(2,20,FALSE,NULL,NULL,NULL,NULL),(3,30,NULL,'2001-01-01','2004-02-29','\x41','\x41')`, 3))
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						q := "UPDATE t SET a=7 WHERE " + tc.predicate
						if prepared {
							stmt, err := backend.DB.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							for range 2 {
								r, err := stmt.ExecContext(p.Ctx)
								if err != nil {
									t.Fatal(err)
								}
								fixture.LockAffected(t, r, int64(len(tc.ids)))
							}
						} else {
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, int64(len(tc.ids))))
						}
						values := []int64{10, 20, 30}
						for _, id := range tc.ids {
							values[id-1] = 7
						}
						var d1, d2, d3, d4 any = "2000-01-01", "2000-02-29", "2001-01-01", "2004-02-29"
						if backend.PG {
							d1 = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
							d2 = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)
							d3 = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
							d4 = time.Date(2004, 2, 29, 0, 0, 0, 0, time.UTC)
						}
						fixture.UpdateCoreByteRows(t, p.Ctx, backend.DB, "SELECT b,sb FROM t ORDER BY id", [][][]byte{{{65}, {66, 0}}, {nil, nil}, {{65}, {65}}})
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id,a,f,d,sd,b,sb FROM t ORDER BY id", []string{"id", "a", "f", "d", "sd", "b", "sb"}, []string{"INT4", "INT4", "BOOL", "DATE", "DATE", "BYTEA", "BYTEA"}, [][]any{{int64(1), values[0], true, d1, d2, "A", "B\x00"}, {int64(2), values[1], false, nil, nil, nil, nil}, {int64(3), values[2], nil, d3, d4, "A", "A"}}))
					})
				}
			})
		}
	}
}

func TestReferenceUpdateCoreCompositionOldRowSwap(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,a INT,d DATE,sd DATE,b BYTEA,sb BYTEA)", 0))
	p.Step(t, fixture.Exec(`INSERT INTO t VALUES(1,7,'2000-01-01','2000-02-29','\x01','\x005cff'),(2,NULL,NULL,NULL,NULL,NULL)`, 2))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var d, sd any = "2000-01-01", "2000-02-29"
			if backend.PG {
				d = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				sd = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)
			}
			check := func(swapped bool) {
				date, src := d, sd
				b, sb := []byte{1}, []byte{0, 92, 255}
				if swapped {
					date, src = src, date
					b, sb = sb, b
				}
				fixture.UpdateCoreByteRows(t, p.Ctx, backend.DB, "SELECT b,sb FROM t ORDER BY id", [][][]byte{{b, sb}, {nil, nil}})
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id,d,sd,b,sb FROM t ORDER BY id", []string{"id", "d", "sd", "b", "sb"}, []string{"INT4", "DATE", "DATE", "BYTEA", "BYTEA"}, [][]any{{int64(1), date, src, string(b), string(sb)}, {int64(2), nil, nil, nil, nil}}))
			}
			q := "UPDATE t SET d=sd,sd=d,b=sb,sb=b"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 2))
			check(true)
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, swapped := range []bool{false, true} {
				r, err := held.ExecContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LockAffected(t, r, 2)
				check(swapped)
			}
			for _, prepared := range []bool{false, true} {
				step := fixture.Exec("UPDATE t SET a=CAST(NULL AS INTEGER),d=DATE '2000-02-29',b=NULL WHERE id=1", 1)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				fixture.UpdateCoreByteRows(t, p.Ctx, backend.DB, "SELECT b,sb FROM t ORDER BY id", [][][]byte{{nil, {1}}, {nil, nil}})
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a,d,b FROM t ORDER BY id", []string{"a", "d", "b"}, []string{"INT4", "DATE", "BYTEA"}, [][]any{{nil, sd, nil}, {nil, nil, nil}}))
			}
		})
	}
}
