package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGINMaintenanceInstalledScalar(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		compoundCleanup(t, p, b, conn)
		phase, query := "setup", ""
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("installed scalar maintenance phase=%s query=%s", phase, query)
			}
		})
		run := func(step fixture.Step) {
			t.Helper()
			query = step.Query
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
		}
		both := func(step fixture.Step) {
			t.Helper()
			query = step.Query
			heapBoth(t, p, b, conn, step)
		}
		for _, source := range []string{
			"SET lc_monetary='C'",
			"CREATE EXTENSION btree_gin VERSION '1.3'",
			"CREATE EXTENSION amcheck",
			"CREATE TYPE gin_maint_scalar_enum AS ENUM('z','a','m')",
			"ALTER TYPE gin_maint_scalar_enum ADD VALUE 'q' BEFORE 'a'",
			"CREATE TABLE gin_maint_scalar(id integer,b bit(3),v varbit,e gin_maint_scalar_enum,m money,a integer[],t tsvector)",
		} {
			run(fixture.Exec(source, 0))
		}
		run(fixture.Exec("INSERT INTO gin_maint_scalar VALUES(1,'100','0','a','0.01','{2}','cat:1')", 1))
		run(fixture.Exec("CREATE INDEX gin_maint_scalar_i ON gin_maint_scalar USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)", 0))
		run(fixture.Exec("INSERT INTO gin_maint_scalar VALUES(2,'011','','q','-0.01','{}','dog:1'),(3,'100','00','a','0.01','{2,2}','cat:1'),(4,NULL,NULL,NULL,NULL,NULL,NULL),(5,'111','10','m','0.02','{3}','rat:1')", 4))
		check := func(extra bool) {
			t.Helper()
			for _, tc := range []struct {
				column, sqltype, boundary string
				want                      [][]int64
			}{
				{"b", "bit(3)", "100", [][]int64{{2}, {1, 2, 3}, {1, 3}, {1, 3, 5}, {5}}},
				{"v", "varbit", "0", [][]int64{{2}, {1, 2}, {1}, {1, 3, 5}, {3, 5}}},
				{"e", "gin_maint_scalar_enum", "a", [][]int64{{2}, {1, 2, 3}, {1, 3}, {1, 3, 5}, {5}}},
				{"m", "money", "0.01", [][]int64{{2}, {1, 2, 3}, {1, 3}, {1, 3, 5}, {5}}},
			} {
				for strategy, operator := range []string{"<", "<=", "=", ">=", ">"} {
					want := append([]int64(nil), tc.want[strategy]...)
					if extra && strategy >= 1 && strategy <= 3 {
						want = append(want, 6)
					}
					query := "SELECT id FROM gin_maint_scalar WHERE " + tc.column + operator + "$1::" + tc.sqltype + " ORDER BY id"
					both(ginIDs(query, want, tc.boundary))
					both(ginIDs(query, nil, nil))
				}
				both(ginIDs("SELECT id FROM gin_maint_scalar WHERE "+tc.column+" IS NULL ORDER BY id", []int64{4}))
			}
			matches := []int64{1, 3}
			if extra {
				matches = append(matches, 6)
			}
			both(ginIDs("SELECT id FROM gin_maint_scalar WHERE a @> '{2}'::integer[] AND t @@ 'cat'::tsquery ORDER BY id", matches))
			both(amcheckSelect("SELECT gin_index_check('gin_maint_scalar_i') AS ok", false))
		}
		check(false)
		run(ginMaintResult("SELECT gin_clean_pending_list(NULL::regclass)", nil))
		wrong := fixture.ErrorDiagnostic("SELECT gin_clean_pending_list('gin_maint_scalar')", fixture.ControlsReferenceDiagnostic("42809", `"gin_maint_scalar" is not an index`))
		wrong.SelectRows = true
		run(wrong)
		var dump bytes.Buffer
		phase = "native dump of mixed installed root"
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
		}
		// Switching off preserves prior pending work, including scalar NULLs.
		phase = "cleanup of residual pending after fastupdate off"
		run(fixture.Exec("ALTER INDEX gin_maint_scalar_i SET(fastupdate=off)", 0))
		run(ginMaintCleanIndex("gin_maint_scalar_i", true))
		run(ginMaintCleanIndex("gin_maint_scalar_i", false))
		check(false)
		phase = "new off-mode scalar insertion"
		run(fixture.Prepared(fixture.Exec("INSERT INTO gin_maint_scalar VALUES(6,'100','0','a','0.01','{2}','cat:1')", 1)))
		run(ginMaintCleanIndex("gin_maint_scalar_i", false))
		check(true)
		if !b.PG {
			phase = "native restore of mixed installed root"
			// Native comments preserve the earlier mixed split and remint actual
			// installed class/helper/enum owners. PostgreSQL has no such format.
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			phase = "check and cleanup restored installed root"
			check(false)
			run(ginMaintCleanIndex("gin_maint_scalar_i", true))
			run(ginMaintCleanIndex("gin_maint_scalar_i", false))
			check(false)
		}
	})
}
