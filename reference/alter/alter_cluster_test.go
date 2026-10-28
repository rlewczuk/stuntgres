package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE t(id int,note text)", "CREATE INDEX a ON t(id)", "CREATE INDEX b ON t(note)"} {
		persistenceExec(t, p, b, c, q)
	}
	persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(2,'two'),(1,NULL)", 2))
}
func clusterState(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT c.relname,i.indisclustered FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=$1::regclass ORDER BY c.relname", []string{"relname", "indisclustered"}, []string{"NAME", "BOOL"}, rows, table)
}
func clusterBits(a, b bool) fixture.Step { return clusterState("t", [][]any{{"a", a}, {"b", b}}) }
func clusterRows() fixture.Step {
	return fixture.Select("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "two"}})
}
func clusterCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, a, z bool) {
	t.Helper()
	persistenceRun(t, p, b, x, clusterBits(a, z))
	persistenceRun(t, p, b, x, clusterRows())
}
func TestReferenceAlterClusterPreference(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		clusterCheck(t, p, b, c, false, false)
		for _, v := range []struct {
			q    string
			a, b bool
		}{{"CLUSTER ON a", true, false}, {"CLUSTER ON a", true, false}, {"CLUSTER ON b", false, true}, {"SET WITHOUT CLUSTER", false, false}, {"SET WITHOUT CLUSTER", false, false}} {
			for _, held := range []bool{false, true} {
				step := fixture.Exec("ALTER TABLE t "+v.q, 0)
				if held {
					step = fixture.Prepared(step)
				}
				persistenceRun(t, p, b, c, step)
				clusterCheck(t, p, b, c, v.a, v.b)
			}
		}
	})
}
func TestReferenceAlterClusterPrepared(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		state := clusterBits(false, false)
		data := clusterRows()
		qs, err := c.PrepareContext(p.Ctx, state.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer qs.Close()
		qd, err := c.PrepareContext(p.Ctx, data.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer qd.Close()
		qa, err := c.PrepareContext(p.Ctx, "ALTER TABLE t CLUSTER ON b")
		if err != nil {
			t.Fatal(err)
		}
		defer qa.Close()
		for _, q := range []string{"ALTER TABLE t CLUSTER ON a", "BEGIN", "ALTER TABLE t CLUSTER ON b", "ROLLBACK"} {
			persistenceExec(t, p, b, c, q)
			selected := q == "ALTER TABLE t CLUSTER ON b"
			persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: qs, SQL: state.Query}, clusterBits(!selected, selected))
			persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: qd, SQL: data.Query}, data)
		}
		persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: qa, SQL: "ALTER TABLE t CLUSTER ON b"}, fixture.Exec("ALTER TABLE t CLUSTER ON b", 0))
		clusterCheck(t, p, b, c, false, true)
	})
}
func TestReferenceAlterClusterDiagnostics(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		for _, q := range []string{"ALTER TABLE t CLUSTER ON a", "CREATE TABLE not_an_index(x int)", "CREATE INDEX other_idx ON not_an_index(x)", "CREATE INDEX hash_idx ON t USING hash(id)", "CREATE INDEX partial_idx ON t(id) WHERE id>0", "CREATE TABLE duplicates(id int)"} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO duplicates VALUES(1),(1)", 2))
		persistenceExec(t, p, b, c, "CREATE INDEX dup_a ON duplicates(id)")
		persistenceExec(t, p, b, c, "ALTER TABLE duplicates CLUSTER ON dup_a")
		fixture.ConcurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY invalid_idx ON duplicates(id)", fixture.ConcurrentIndexUnique("duplicates", "invalid_idx", "Key (id)=(1) is duplicated.", true))
		if state, ok := fixture.ConcurrentIndexState(t, p, b, "invalid_idx"); !ok || state.Valid {
			t.Fatal("missing real invalid index", state, ok)
		}
		for _, v := range []struct{ name, code, message string }{{"missing_idx", "42704", `index "missing_idx" for table "t" does not exist`}, {"not_an_index", "42809", `"not_an_index" is not an index`}, {"other_idx", "42809", `"other_idx" is not an index for table "t"`}, {"hash_idx", "0A000", `cannot cluster on index "hash_idx" because access method does not support clustering`}, {"partial_idx", "0A000", `cannot cluster on partial index "partial_idx"`}} {
			for _, held := range []bool{false, true} {
				persistenceFailure(t, p, b, c, "ALTER TABLE t CLUSTER ON "+v.name, held, persistenceDiagnostic(v.code, v.message))
				persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", true}, {"b", false}, {"hash_idx", false}, {"partial_idx", false}}))
				persistenceRun(t, p, b, c, clusterRows())
			}
		}
		for _, held := range []bool{false, true} {
			persistenceFailure(t, p, b, c, "ALTER TABLE duplicates CLUSTER ON invalid_idx", held, persistenceDiagnostic("0A000", `cannot cluster on invalid index "invalid_idx"`))
			persistenceRun(t, p, b, c, clusterState("duplicates", [][]any{{"dup_a", true}, {"invalid_idx", false}}))
			persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM duplicates ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		}
		persistenceExec(t, p, b, c, `CREATE INDEX "odd""\name" ON t USING hash(id)`)
		persistenceFailure(t, p, b, c, `ALTER TABLE t CLUSTER ON "odd""\name"`, false, persistenceDiagnostic("0A000", `cannot cluster on index "odd"\name" because access method does not support clustering`))
	})
}
func TestReferenceAlterClusterCatalogMetadata(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		metadata := fixture.Select("SELECT attnum,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_catalog.pg_index'::regclass AND attname='indisclustered'", []string{"attnum", "atttypid", "attnotnull"}, []string{"INT2", "OID", "BOOL"}, [][]any{{int64(10), int64(16), true}})
		fixture.PGAttributeCheck(t, p, b, c, metadata)
		fixture.PGAttributeCheck(t, p, b, c, fixture.Prepared(metadata))
		for _, q := range []string{"CREATE TABLE t(id int,note text)", "CREATE INDEX a ON t(id DESC) INCLUDE(note)", "ALTER TABLE t CLUSTER ON a"} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", true}}))
		persistenceRun(t, p, b, c, fixture.Select("SELECT indkey::text,indcollation::text,indclass::text,indoption::text FROM pg_index WHERE indexrelid='a'::regclass", []string{"indkey", "indcollation", "indclass", "indoption"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"1 2", "0", "1978", "3"}}))
	})
}
func TestReferenceAlterClusterOIDGrammar(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		for _, q := range []string{"ALTER TABLE t SET WITHOUT OIDS", "ALTER TABLE t SET WITHOUT OIDS, SET WITHOUT OIDS", "CREATE TABLE cluster(oids int)", "CREATE INDEX oids ON cluster(oids)", "ALTER TABLE cluster CLUSTER ON oids"} {
			persistenceExec(t, p, b, c, q)
		}
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, held := range []bool{false, true} {
				want := persistenceDiagnostic("42601", `syntax error at or near "WITH"`)
				want.Position = fixture.Position(19)
				if prefix != "" {
					want.Position = fixture.Position(25)
				}
				step := fixture.ErrorDiagnostic(prefix+"ALTER TABLE t SET WITH OIDS", want)
				if held {
					step = fixture.Prepared(step)
					step.ErrorStage = "prepare"
				}
				persistenceRun(t, p, b, c, step)
				clusterCheck(t, p, b, c, false, false)
			}
		}
	})
}
