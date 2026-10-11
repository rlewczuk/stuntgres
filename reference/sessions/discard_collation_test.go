package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardTemporaryCollationProjection(t *testing.T) {
	for _, boundary := range []string{"standalone", "rollback", "savepoint", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				discardSeed(t, p, b, a, peer)
				discardExec(t, p, b, a, `CREATE COLLATION pg_temp.discard_projection_coll FROM pg_catalog."C"`)
				discardExec(t, p, b, peer, `CREATE COLLATION pg_temp.discard_peer_coll FROM pg_catalog."C"`)
				discardExec(t, p, b, a, "CREATE TABLE discard_projection(id int,v text COLLATE pg_temp.discard_projection_coll)")
				sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_projection VALUES(7,'a')", 1))
				discardExec(t, p, b, a, "CREATE INDEX discard_projection_v ON discard_projection(v)")
				full := fixture.Select("SELECT * FROM discard_projection", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "a"}})
				projected := fixture.Select("SELECT * FROM discard_projection", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
				provider := "SELECT 'a' COLLATE pg_temp.discard_projection_coll AS v"
				sqlPreparedStep(t, p, b, a, full)
				discardScalar(t, p, b, a, provider, "v", "TEXT", "a")
				if boundary != "standalone" {
					discardExec(t, p, b, a, "BEGIN")
				}
				if boundary == "savepoint" {
					discardExec(t, p, b, a, "SAVEPOINT original")
				}
				discardExec(t, p, b, a, "DISCARD TEMP")
				sqlPreparedStep(t, p, b, a, projected)
				// Missing-provider lookup is an ordinary SQL error: recover its own
				// post-cleanup savepoint without rolling back the cleanup candidate.
				if boundary != "standalone" {
					discardExec(t, p, b, a, "SAVEPOINT missing_provider")
				}
				discardError(t, p, b, a, provider, "42704", `collation "pg_temp.discard_projection_coll" for encoding "UTF8" does not exist`, "12")
				if boundary != "standalone" {
					discardExec(t, p, b, a, "ROLLBACK TO missing_provider")
				}
				discardPeerIntact(t, p, b, peer)
				discardScalar(t, p, b, peer, "SELECT 'b' COLLATE pg_temp.discard_peer_coll AS v", "v", "TEXT", "b")
				discardScalar(t, p, b, a, "SELECT v FROM discard_durable", "v", "INT4", int64(9))
				switch boundary {
				case "rollback":
					discardExec(t, p, b, a, "ROLLBACK")
				case "savepoint":
					discardExec(t, p, b, a, "ROLLBACK TO original")
					discardExec(t, p, b, a, "COMMIT")
				case "commit":
					discardExec(t, p, b, a, "COMMIT")
				}
				if boundary == "rollback" || boundary == "savepoint" {
					sqlPreparedStep(t, p, b, a, full)
					discardScalar(t, p, b, a, provider, "v", "TEXT", "a")
					// Exercise the restored index/provider dependency closure again.
					discardExec(t, p, b, a, "DISCARD TEMP")
				}
				sqlPreparedStep(t, p, b, a, projected)
				discardError(t, p, b, a, provider, "42704", `collation "pg_temp.discard_projection_coll" for encoding "UTF8" does not exist`, "12")
				discardExec(t, p, b, a, `CREATE COLLATION pg_temp.discard_projection_coll FROM pg_catalog."C"`)
				discardScalar(t, p, b, a, provider, "v", "TEXT", "a")
				discardPeerIntact(t, p, b, peer)
			})
		})
	}
}
