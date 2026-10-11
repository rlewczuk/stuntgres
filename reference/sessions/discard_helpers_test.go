package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func discardScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			b.DB.SetMaxOpenConns(2)
			b.DB.SetMaxIdleConns(1)
			a, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer func() {
				for _, c := range []*sql.Conn{a, peer} {
					c.ExecContext(context.Background(), "ROLLBACK")
					c.ExecContext(context.Background(), "DEALLOCATE ALL")
					c.ExecContext(context.Background(), "SELECT pg_advisory_unlock_all()")
				}
			}()
			body(t, p, b, a, peer)
		})
	}
}

func discardExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string) {
	t.Helper()
	sqlPreparedStep(t, p, b, c, fixture.Exec(query, 0))
}

func discardScalar(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query, name, typ string, want any) {
	t.Helper()
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar(query, name, typ, want))
}

func discardError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query, code, message, position string) {
	t.Helper()
	d := sqlPreparedDiagnostic(code, message)
	d.Position = position
	s := fixture.ErrorDiagnostic(query, d)
	s.ErrorStage = "execute"
	sqlPreparedStep(t, p, b, c, s)
}

func discardMissingTable(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string) {
	t.Helper()
	q := "SELECT v FROM " + name
	discardError(t, p, b, c, q, "42P01", fmt.Sprintf("relation %q does not exist", name), sqlPreparedPosition(q, name))
}

func discardMissingMemory(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string) {
	t.Helper()
	discardError(t, p, b, c, "SELECT currval('"+name+"')", "55000", fmt.Sprintf("currval of sequence %q is not yet defined in this session", name), "")
	discardError(t, p, b, c, "SELECT lastval()", "55000", "lastval is not yet defined in this session", "")
}

func discardSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
	t.Helper()
	discardExec(t, p, b, a, "CREATE TABLE discard_durable(v int)")
	sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_durable VALUES(9)", 1))
	discardExec(t, p, b, a, "CREATE SEQUENCE discard_s CACHE 10")
	for i, c := range []*sql.Conn{a, peer} {
		v := 7 + i
		discardExec(t, p, b, c, fmt.Sprintf("PREPARE p AS SELECT %d AS v", v))
		discardExec(t, p, b, c, "CREATE TEMP TABLE discard_t(v int)")
		sqlPreparedStep(t, p, b, c, fixture.Exec(fmt.Sprintf("INSERT INTO discard_t VALUES(%d)", v), 1))
		discardExec(t, p, b, c, "CREATE TEMP SEQUENCE discard_ts")
		discardExec(t, p, b, c, fmt.Sprintf("SET application_name = 'discard_%d'", v))
		discardScalar(t, p, b, c, "SELECT nextval('discard_s')", "nextval", "INT8", int64(1+10*i))
	}
	discardScalar(t, p, b, a, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", true)
}

func discardPeerIntact(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn) {
	t.Helper()
	discardScalar(t, p, b, peer, "EXECUTE p", "v", "INT4", int64(8))
	discardScalar(t, p, b, peer, "SELECT v FROM discard_t", "v", "INT4", int64(8))
	discardScalar(t, p, b, peer, "SHOW application_name", "application_name", "TEXT", "discard_8")
	discardScalar(t, p, b, peer, "SELECT currval('discard_s')", "currval", "INT8", int64(11))
	discardScalar(t, p, b, peer, "SELECT v FROM discard_durable", "v", "INT4", int64(9))
}
