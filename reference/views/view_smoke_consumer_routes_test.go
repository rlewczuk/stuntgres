package reference_test

import (
	"bytes"
	"strconv"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Completes the original smoke contract after the shared row/held-parameter
// witnesses: identity changes, actual catalog export, and native import atomicity.
func TestReferenceViewSmokeCatalogReplayControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := b.DB
			recorder := b.Recorder
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
			providerQuery := "CREATE VIEW providers AS SELECT viewname FROM pg_catalog.pg_views WHERE schemaname='public'"
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0),
				fixture.Exec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4),
				fixture.Exec("CREATE VIEW v(k,label) WITH(security_barrier=true) AS SELECT id,txt FROM t WHERE visible", 0),
				fixture.Exec("CREATE VIEW w AS SELECT k,label FROM v WHERE k>1", 0),
				fixture.Exec("CREATE VIEW z AS SELECT FROM t", 0),
				fixture.Exec(providerQuery, 0),
				fixture.Exec("CREATE TABLE b(txt TEXT,visible BOOL)", 0),
				fixture.Exec("INSERT INTO b VALUES('1',true),('oops',false)", 2),
				fixture.Exec("CREATE VIEW safe(txt,d,clock,stamp) WITH(security_barrier=true) AS SELECT txt,DATE '2000-01-02',TIME(3) '04:05:06.1235',TIMESTAMP(3) '2000-01-02 04:05:06.1235' FROM b WHERE visible", 0),
				fixture.Select("SELECT CAST(txt AS INT) FROM safe WHERE CAST(txt AS INT)=1", []string{"txt"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Exec("ALTER TABLE t RENAME TO renamed", 0),
				fixture.Exec("ALTER TABLE renamed RENAME COLUMN id TO new_id", 0),
				fixture.Exec("CREATE TABLE t(id INT)", 0),
				fixture.Exec("INSERT INTO t VALUES(99)", 1),
				fixture.Select("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}),
			} {
				run(s)
			}
			dependency := func() {
				_, err := db.ExecContext(p.Ctx, "DROP TABLE renamed RESTRICT")
				got := fixture.ExtractDiagnostic(t, err)
				if got.Code != "2BP01" || got.Message != "cannot drop table renamed because other objects depend on it" {
					t.Fatal(got)
				}
			}
			dependency()
			var oid int64
			var kind string
			var rules bool
			if err := db.QueryRowContext(p.Ctx, "SELECT oid,relkind,relhasrules FROM pg_catalog.pg_class WHERE relname='v'").Scan(&oid, &kind, &rules); err != nil || oid <= 0 || kind != "v" || !rules {
				t.Fatal(oid, kind, rules, err)
			}
			ruleOID := any(oid)
			if b.PG {
				ruleOID = strconv.FormatInt(oid, 10)
			}
			run(fixture.Select("SELECT rulename,ev_class,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename", "ev_class", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "OID", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", ruleOID, "1", "O", true}}, oid))
			var definition string
			if err := db.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='v'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE VIEW reconstructed AS "+definition, 0))
			run(fixture.Select("SELECT k FROM reconstructed ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(3)}}))
			run(fixture.Exec("INSERT INTO renamed VALUES(4,'d',true)", 1))
			run(fixture.Select("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			check := func() {
				run(fixture.Select("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(4)}}))
				stamp := time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)
				want := [][]any{{"2000-01-02", "04:05:06.124", stamp}}
				if b.PG {
					want = [][]any{{time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC), stamp}}
				}
				run(fixture.Select("SELECT d,clock,stamp FROM safe", []string{"d", "clock", "stamp"}, []string{"DATE", "TIME", "TIMESTAMP"}, want))
				run(fixture.Select("SELECT viewname FROM providers ORDER BY viewname", []string{"viewname"}, []string{"NAME"}, [][]any{{"providers"}, {"reconstructed"}, {"safe"}, {"v"}, {"w"}, {"z"}}))
			}
			check()
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				restored := fixture.NewFixture(t, false, true)
				db, recorder = restored.Memory, restored.MemoryRecord
				if err := stuntgres.Restore(p.Ctx, restored.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
				if err := stuntgres.Restore(p.Ctx, restored.Native, bytes.NewBufferString("CREATE TABLE lost(i INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
					t.Fatal("invalid restore accepted")
				}
				run(fixture.Select("SELECT count(*) FROM v", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
				dependency()
			}
		})
	}
}
