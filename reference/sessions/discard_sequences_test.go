package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardSequenceMemoryAndCache(t *testing.T) {
	for _, boundary := range []string{"standalone", "rollback", "savepoint"} {
		t.Run(boundary, func(t *testing.T) {
			discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				discardExec(t, p, b, a, "CREATE SEQUENCE discard_s CACHE 10")
				discardExec(t, p, b, a, "CREATE TEMP SEQUENCE discard_ts")
				discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(1))
				discardScalar(t, p, b, peer, "SELECT nextval('discard_s')", "nextval", "INT8", int64(11))
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT last_value,is_called FROM discard_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), true}}))
				if boundary != "standalone" {
					discardExec(t, p, b, a, "BEGIN")
				}
				if boundary == "savepoint" {
					discardExec(t, p, b, a, "SAVEPOINT s")
				}
				discardExec(t, p, b, a, "DISCARD SEQUENCES")
				if boundary == "savepoint" {
					discardExec(t, p, b, a, "ROLLBACK TO s")
				}
				if boundary != "standalone" {
					discardExec(t, p, b, a, "ROLLBACK")
				}
				discardMissingMemory(t, p, b, a, "discard_s")
				discardScalar(t, p, b, peer, "SELECT currval('discard_s')", "currval", "INT8", int64(11))
				discardScalar(t, p, b, peer, "SELECT nextval('discard_s')", "nextval", "INT8", int64(12))
				discardScalar(t, p, b, a, "SELECT last_value FROM discard_s", "last_value", "INT8", int64(20))
				discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(21))
				discardScalar(t, p, b, a, "SELECT nextval('discard_ts')", "nextval", "INT8", int64(1))
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT seqstart,seqincrement,seqmin,seqmax,seqcache,seqcycle FROM pg_sequence WHERE seqrelid='discard_s'::regclass::oid", []string{"seqstart", "seqincrement", "seqmin", "seqmax", "seqcache", "seqcycle"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{int64(1), int64(1), int64(1), int64(9223372036854775807), int64(10), false}}))
			})
		})
	}
}
