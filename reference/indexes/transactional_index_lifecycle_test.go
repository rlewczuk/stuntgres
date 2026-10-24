package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexLifecycle(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("private=%t/commit=%t", private, commit), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tx_index_old")
				fixture.SchemaReferenceCleanup(t, "tx_index_new")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						run := transactionalIndexRun(t, p, b, owner)
						const table = "tx_index_new.items"
						create := func() {
							run(fixture.Exec("CREATE SCHEMA tx_index_old", 0))
							run(fixture.Exec("ALTER SCHEMA tx_index_old RENAME TO tx_index_new", 0))
							run(fixture.Exec("CREATE TABLE "+table+"(n INT,s TEXT,p TEXT)", 0))
							run(fixture.Exec("INSERT INTO "+table+" VALUES(1,'one','a'),(2,'two','b'),(3,'gone','c'),(NULL,NULL,'null')", 4))
						}
						if !private {
							create()
						}
						run(fixture.Exec("BEGIN", 0))
						defer owner.ExecContext(p.Ctx, "ROLLBACK")
						if private {
							create()
						}
						run(fixture.Exec("UPDATE "+table+" SET n=10,s='ten' WHERE n=1", 1))
						run(fixture.Exec("DELETE FROM "+table+" WHERE n=3", 1))
						run(fixture.Exec("INSERT INTO "+table+" VALUES(2,'two','dup'),(4,'four','d'),(NULL,NULL,'null2')", 3))
						for _, q := range []string{
							"CREATE INDEX composite ON " + table + "(n DESC NULLS LAST,s ASC NULLS FIRST) INCLUDE(p)",
							"CREATE INDEX text_key ON " + table + "(s)",
							"CREATE INDEX hash_key ON " + table + " USING hash(n)",
						} {
							run(fixture.Prepared(fixture.Exec(q, 0)))
						}
						run(fixture.Select("SELECT pg_get_indexdef('tx_index_new.composite'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX composite ON tx_index_new.items USING btree (n DESC NULLS LAST, s NULLS FIRST) INCLUDE (p)"}}))
						run(fixture.Exec("UPDATE "+table+" SET n=20 WHERE n=2", 2))
						run(fixture.Exec("DELETE FROM "+table+" WHERE n=4", 1))
						run(fixture.Exec("INSERT INTO "+table+" VALUES(5,'five','e')", 1))
						want := [][]any{{int64(5), "five"}, {int64(10), "ten"}, {int64(20), "two"}, {int64(20), "two"}, {nil, nil}, {nil, nil}}
						rows := fixture.Select("SELECT n,s FROM "+table+" ORDER BY n NULLS LAST,s", []string{"n", "s"}, []string{"INT4", "TEXT"}, want)
						run(rows)
						checkPeer := transactionalIndexRun(t, p, b, peer)
						checkPeer(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='tx_index_new' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, nil))
						if commit {
							run(fixture.Exec("COMMIT", 0))
							checkPeer(rows)
							checkPeer(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='tx_index_new' ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, [][]any{{"composite"}, {"hash_key"}, {"text_key"}}))
							checkPeer(fixture.Exec("INSERT INTO "+table+" VALUES(20,'two','later')", 1))
							checkPeer(fixture.Exec("UPDATE "+table+" SET n=30 WHERE n=5", 1))
							checkPeer(fixture.Exec("DELETE FROM "+table+" WHERE n=10", 1))
							checkPeer(fixture.Select("SELECT n FROM "+table+" WHERE n=20 ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(20)}, {int64(20)}}))
						} else {
							run(fixture.Exec("ROLLBACK", 0))
							checkPeer(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='tx_index_new'", []string{"indexname"}, []string{"NAME"}, nil))
							if private {
								checkPeer(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname IN ('tx_index_old','tx_index_new')", []string{"nspname"}, []string{"NAME"}, nil))
							} else {
								checkPeer(fixture.Select("SELECT n FROM "+table+" ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
							}
						}
					})
				}
			})
		}
	}
}
