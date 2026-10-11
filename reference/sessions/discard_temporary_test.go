package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardTemporaryClosureAndTransactions(t *testing.T) {
	for _, boundary := range []string{"standalone", "rollback", "savepoint", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				discardSeed(t, p, b, a, peer)
				discardExec(t, p, b, a, "CREATE TEMP TABLE discard_owned(id serial, ident int GENERATED ALWAYS AS IDENTITY, v int UNIQUE)")
				sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_owned(v) VALUES(7)", 1))
				discardExec(t, p, b, a, "CREATE INDEX discard_plain ON discard_owned(v)")
				discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(2))
				discardExec(t, p, b, a, "CREATE INDEX discard_expression ON discard_owned((v+1))")
				discardExec(t, p, b, a, "PREPARE temp_p AS SELECT v FROM discard_t")
				if boundary != "standalone" {
					discardExec(t, p, b, a, "BEGIN")
				}
				if boundary == "savepoint" {
					discardExec(t, p, b, a, "SAVEPOINT s")
					discardExec(t, p, b, a, "CREATE TEMP TABLE discard_new(v int)")
				}
				discardExec(t, p, b, a, "DISCARD TEMPORARY")
				if boundary == "rollback" || boundary == "savepoint" {
					if boundary == "savepoint" {
						discardExec(t, p, b, a, "ROLLBACK TO s")
					}
					discardExec(t, p, b, a, "ROLLBACK")
					discardScalar(t, p, b, a, "SELECT v FROM discard_t", "v", "INT4", int64(7))
					discardScalar(t, p, b, a, "SELECT v FROM discard_owned", "v", "INT4", int64(7))
					discardScalar(t, p, b, a, "SELECT nextval('discard_owned_id_seq')", "nextval", "INT8", int64(2))
					discardScalar(t, p, b, a, "SELECT nextval('discard_owned_ident_seq')", "nextval", "INT8", int64(2))
					if boundary == "savepoint" {
						discardMissingTable(t, p, b, a, "discard_new")
					}
					discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(3))
					discardExec(t, p, b, a, "DISCARD TEMP")
				} else if boundary == "commit" {
					discardExec(t, p, b, a, "COMMIT")
				}
				discardMissingTable(t, p, b, a, "discard_t")
				discardMissingTable(t, p, b, a, "discard_owned")
				for _, name := range []string{"discard_ts", "discard_owned_id_seq", "discard_owned_ident_seq"} {
					q := "SELECT nextval('" + name + "')"
					discardError(t, p, b, a, q, "42P01", "relation \""+name+"\" does not exist", sqlPreparedPosition(q, "'"+name+"'"))
				}
				discardError(t, p, b, a, "EXECUTE temp_p", "42P01", "relation \"discard_t\" does not exist", "33")
				discardPeerIntact(t, p, b, peer)
				wantMemory := int64(2)
				if boundary == "rollback" || boundary == "savepoint" {
					wantMemory = 3
				}
				discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", wantMemory)
				discardScalar(t, p, b, a, "SELECT lastval()", "lastval", "INT8", wantMemory)
				discardExec(t, p, b, a, "CREATE TEMP TABLE discard_t(v int)")
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT v FROM discard_t", []string{"v"}, []string{"INT4"}, [][]any{}))
			})
		})
	}
	for _, state := range []string{"BeforeFirstFetch", "ActiveSource", "ExhaustedSource"} {
		t.Run(state, func(t *testing.T) {
			discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				discardSeed(t, p, b, a, peer)
				discardExec(t, p, b, a, "CREATE TEMP TABLE discard_active(v int)")
				sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_active VALUES(7),(8)", 2))
				discardExec(t, p, b, a, "BEGIN")
				discardExec(t, p, b, a, "DECLARE c CURSOR FOR SELECT v FROM discard_active ORDER BY v")
				if state == "ExhaustedSource" {
					sqlPreparedStep(t, p, b, a, fixture.Select("FETCH ALL FROM c", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
				} else if state == "ActiveSource" {
					discardScalar(t, p, b, a, "FETCH 1 FROM c", "v", "INT4", int64(7))
				}
				discardExec(t, p, b, a, "SAVEPOINT recover")
				discardError(t, p, b, a, "DISCARD TEMP", "55006", "cannot DROP TABLE \"discard_active\" because it is being used by active queries in this session", "")
				discardExec(t, p, b, a, "ROLLBACK TO recover")
				switch state {
				case "BeforeFirstFetch":
					discardScalar(t, p, b, a, "FETCH 1 FROM c", "v", "INT4", int64(7))
				case "ActiveSource":
					discardScalar(t, p, b, a, "FETCH 1 FROM c", "v", "INT4", int64(8))
				case "ExhaustedSource":
					sqlPreparedStep(t, p, b, a, fixture.Select("FETCH ALL FROM c", []string{"v"}, []string{"INT4"}, [][]any{}))
				}
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT v FROM discard_active ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
				discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
				discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(1))
				discardExec(t, p, b, a, "CLOSE c")
				discardExec(t, p, b, a, "DISCARD TEMP")
				discardExec(t, p, b, a, "ROLLBACK")
				sqlPreparedStep(t, p, b, a, fixture.Select("SELECT v FROM discard_active ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			})
		})
	}
}
