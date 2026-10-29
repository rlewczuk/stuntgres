package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTcnChannelsTransactions(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		sender, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer sender.Close()
		receiver := sessionMessageConn(t, p, b)
		run := func(q string, count int64) { fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec(q, count)) }
		run("CREATE EXTENSION tcn", 0)
		run("CREATE TABLE tcn_tx(id int PRIMARY KEY)", 0)
		run(`CREATE TRIGGER changes AFTER INSERT OR UPDATE OR DELETE ON tcn_tx FOR EACH ROW EXECUTE FUNCTION triggered_change_notification('Tcn.Custom')`, 0)
		sessionMessageSQL(t, p, b, receiver, `LISTEN "Tcn.Custom"`)
		var pid uint64
		if err := sender.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"BEGIN", "INSERT INTO tcn_tx VALUES (1)", "ROLLBACK", "BEGIN", "SAVEPOINT s", "INSERT INTO tcn_tx VALUES (2)", "ROLLBACK TO s", "COMMIT"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 1
			}
			run(q, count)
		}
		fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Error("INSERT INTO tcn_tx VALUES (3),(3)", "23505"))
		run("DELETE FROM tcn_tx WHERE id=99", 0)
		run("INSERT INTO tcn_tx VALUES (9)", 1)
		got := sessionMessageReceive(t, p, b, receiver, "Tcn.Custom", `"tcn_tx",I,"id"='9'`)
		if got.PID != pid {
			t.Fatalf("PID = %d, want sender %d", got.PID, pid)
		}
		run("BEGIN", 0)
		run("UPDATE tcn_tx SET id=id WHERE id=9", 1)
		run("UPDATE tcn_tx SET id=id WHERE id=9", 1)
		run("INSERT INTO tcn_tx VALUES (10)", 1)
		run("COMMIT", 0)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Custom", `"tcn_tx",U,"id"='9'`)
		sessionMessageReceive(t, p, b, receiver, "Tcn.Custom", `"tcn_tx",I,"id"='10'`)
		fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Select("SELECT id FROM tcn_tx ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
	})
	t.Run("byte_boundaries", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			sender := sessionMessageConn(t, p, b)
			receiver := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, sender, "CREATE EXTENSION tcn")
			sessionMessageSQL(t, p, b, sender, "CREATE TABLE tcn_size(id text PRIMARY KEY)")
			for _, channel := range []string{strings.Repeat("a", 63), strings.Repeat("é", 31) + "x"} {
				sessionMessageSQL(t, p, b, receiver, `LISTEN "`+channel+`"`)
				sessionMessageSQL(t, p, b, sender, "CREATE TRIGGER changes AFTER INSERT ON tcn_size FOR EACH ROW EXECUTE FUNCTION triggered_change_notification('"+channel+"')")
				fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec("INSERT INTO tcn_size VALUES (repeat('x',7979))", 1))
				sessionMessageReceive(t, p, b, receiver, channel, `"tcn_size",I,"id"='`+strings.Repeat("x", 7979)+`'`)
				sessionMessageSQL(t, p, b, sender, "DROP TRIGGER changes ON tcn_size")
				fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec("DELETE FROM tcn_size", 1))
			}
		})
	})
}
