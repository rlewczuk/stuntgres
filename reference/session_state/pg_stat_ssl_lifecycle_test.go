package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSSLLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		a, other := statActivityConn(t, p, b), statActivityConn(t, p, b)
		apid, bpid := statActivityPID(t, p, a), statActivityPID(t, p, other)
		sslPair(t, p, b, o, apid, bpid)
		for _, query := range []string{"SELECT 7", "BEGIN", "SELECT 11", "ROLLBACK", "SET track_activities=off", "SELECT 13", "SET track_activities=on"} {
			activityExec(t, p, a, query)
			fixture.StatisticsClearSnapshot(t, p, b, o)
			sslPair(t, p, b, o, apid, bpid)
		}
		if !b.PG {
			step := fixture.Select("SELECT client_addr,client_hostname,client_port FROM pg_stat_activity WHERE pid=$1", []string{"client_addr", "client_hostname", "client_port"}, []string{"INET", "TEXT", "INT4"}, [][]any{{nil, nil, nil}}, apid)
			fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		}
		// Returning a borrower is not a physical close. ErrBadConn drains the
		// actual owner; the independent observer waits for server retirement.
		fixture.StatisticsRetire(t, a)
		statActivityRetired(t, p, o, apid)
		fixture.StatisticsClearSnapshot(t, p, b, o)
		sslWant(t, p, b, o, apid, false)
		sslWant(t, p, b, o, bpid, true)
		newcomer := statActivityConn(t, p, b)
		newpid := statActivityPID(t, p, newcomer)
		if newpid == apid || newpid == bpid || newpid <= 0 {
			t.Fatal("new physical identity", newpid, apid, bpid)
		}
		fixture.StatisticsClearSnapshot(t, p, b, o)
		sslPair(t, p, b, o, newpid, bpid)
	})
}
