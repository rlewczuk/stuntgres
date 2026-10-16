package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartPreparedPeerCommit(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "restart_commit_a", "restart_commit_b")
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{
			"CREATE SCHEMA restart_commit_a", "CREATE SCHEMA restart_commit_b",
			"CREATE TABLE restart_commit_b.t(id INT)",
			"CREATE SEQUENCE restart_commit_b.s START 10 OWNED BY restart_commit_b.t.id",
			"CREATE SEQUENCE restart_commit_b.other START 20",
		} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO restart_commit_b.t VALUES(7)", 1)
		truncateExec(t, f, f.B, "SET search_path TO restart_commit_a,restart_commit_b", 0)
		query := "SELECT nextval('s') AS first,nextval('other') AS second"
		consumer := truncateRestartRoute(t, p, f.B, query, true)
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(20)}}))
		truncateExec(t, f, f.A, "CREATE SEQUENCE restart_commit_a.s START 100", 0)
		truncateExec(t, f, f.A, "CREATE SEQUENCE restart_commit_a.other START 200", 0)
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(11), int64(21)}}))
		q := "TRUNCATE restart_commit_b.t RESTART IDENTITY"
		reset := truncateRestartRoute(t, p, f.A, q, held)
		defer f.A.ExecContext(p.Ctx, "ROLLBACK")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateExec(t, f, reset, q, 0)
		// No B demand inside A's transaction: commit, unlike rollback,
		// must invalidate B's whole existing literal group.
		truncateExec(t, f, f.A, "COMMIT", 0)
		truncateRows(t, f, f.B, "restart_commit_b.t")
		truncateRestartState(t, f, f.B, "restart_commit_b.s", 10, false)
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(100), int64(200)}}))
		truncateRestartValue(t, f, f.A, "SELECT nextval('restart_commit_b.s')", "nextval", 10)
		truncateRestartValue(t, f, f.A, "SELECT nextval('restart_commit_b.other')", "nextval", 22)
		truncateExec(t, f, f.B, "SET search_path TO public", 0)
		truncateExec(t, f, f.A, "DROP SCHEMA restart_commit_a,restart_commit_b CASCADE", 0)
		*f.Notices = nil
	})
}

func TestReferenceTruncateRestartPreparedNegativeEvents(t *testing.T) {
	for _, action := range []string{"Nextval", "Currval", "Lastval", "Setval", "UnrelatedRestart", "RejectedOwner", "RejectedLock"} {
		t.Run(action, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "restart_negative_a", "restart_negative_b")
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				for _, q := range []string{
					"CREATE SCHEMA restart_negative_a", "CREATE SCHEMA restart_negative_b",
					"CREATE TABLE restart_negative_b.t(id INT)",
					"CREATE SEQUENCE restart_negative_b.s START 10 OWNED BY restart_negative_b.t.id",
					"CREATE SEQUENCE restart_negative_b.other START 20",
					"CREATE TABLE unrelated(id SERIAL)",
					"SET search_path TO restart_negative_a,restart_negative_b,public",
				} {
					truncateExec(t, f, f.A, q, 0)
				}
				truncateExec(t, f, f.A, "INSERT INTO restart_negative_b.t VALUES(7)", 1)
				truncateExec(t, f, f.A, "INSERT INTO unrelated DEFAULT VALUES", 1)
				if action == "RejectedOwner" {
					truncateExec(t, f, f.A, "CREATE ROLE restart_negative_actor", 0)
					defer func() {
						f.A.ExecContext(p.Ctx, "RESET ROLE")
						f.A.ExecContext(p.Ctx, "DROP OWNED BY restart_negative_actor")
						f.A.ExecContext(p.Ctx, "DROP ROLE restart_negative_actor")
					}()
					truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA restart_negative_a,restart_negative_b,public TO restart_negative_actor", 0)
					truncateExec(t, f, f.A, "GRANT TRUNCATE ON restart_negative_b.t TO restart_negative_actor", 0)
				}
				query := "SELECT nextval('s') AS first,nextval('other') AS second"
				consumer := truncateRestartRoute(t, p, f.A, query, true)
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(20)}}))
				truncateExec(t, f, f.A, "CREATE SEQUENCE restart_negative_a.s START 100", 0)
				truncateExec(t, f, f.A, "CREATE SEQUENCE restart_negative_a.other START 200", 0)
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(11), int64(21)}}))
				before, first := int64(11), int64(12)
				switch action {
				case "Nextval":
					q := "SELECT nextval('restart_negative_b.s')"
					truncateRestartValue(t, f, truncateRestartRoute(t, p, f.A, q, held), q, "nextval", 12)
					before, first = 12, 13
				case "Currval":
					q := "SELECT currval('restart_negative_b.s')"
					truncateRestartValue(t, f, truncateRestartRoute(t, p, f.A, q, held), q, "currval", 11)
				case "Lastval":
					q := "SELECT lastval()"
					truncateRestartValue(t, f, truncateRestartRoute(t, p, f.A, q, held), q, "lastval", 21)
				case "Setval":
					q := "SELECT setval('restart_negative_b.s',40)"
					truncateRestartValue(t, f, truncateRestartRoute(t, p, f.A, q, held), q, "setval", 40)
					before, first = 40, 41
				case "UnrelatedRestart":
					q := "TRUNCATE unrelated RESTART IDENTITY"
					truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
					truncateRows(t, f, f.A, "unrelated")
					truncateRestartState(t, f, f.A, "unrelated_id_seq", 1, false)
				case "RejectedOwner":
					q := "TRUNCATE restart_negative_b.t RESTART IDENTITY"
					x := truncateRestartRoute(t, p, f.A, q, held)
					truncateExec(t, f, f.A, "SET ROLE restart_negative_actor", 0)
					truncateRestartError(t, f, x, q, truncateWant("42501", "must be owner of sequence s"))
					truncateExec(t, f, f.A, "RESET ROLE", 0)
				case "RejectedLock":
					q := "TRUNCATE restart_negative_b.t RESTART IDENTITY"
					x := truncateRestartRoute(t, p, f.A, q, held)
					tx := truncateBegin(t, p, f.B, nil)
					truncateRestartState(t, f, tx, "restart_negative_b.s", 11, true)
					truncateExec(t, f, f.A, "SET lock_timeout='30ms'", 0)
					truncateRestartError(t, f, x, q, truncateWant("55P03", "canceling statement due to lock timeout"))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					truncateExec(t, f, f.A, "SET lock_timeout=0", 0)
				}
				// Neither rejected reset nor unrelated activity may publish a
				// table/generator prefix or retarget either held literal.
				truncateRows(t, f, f.A, "restart_negative_b.t", 7)
				truncateRestartState(t, f, f.A, "restart_negative_b.s", before, true)
				truncateRestartState(t, f, f.A, "restart_negative_b.other", 21, true)
				if action != "UnrelatedRestart" {
					truncateRows(t, f, f.A, "unrelated", 1)
					truncateRestartState(t, f, f.A, "unrelated_id_seq", 1, true)
				}
				truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{first, int64(22)}}))
				truncateRestartState(t, f, f.A, "restart_negative_a.s", 100, false)
				truncateRestartState(t, f, f.A, "restart_negative_a.other", 200, false)
				truncateExec(t, f, f.A, "SET search_path TO public", 0)
				truncateExec(t, f, f.A, "DROP SCHEMA restart_negative_a,restart_negative_b CASCADE", 0)
				*f.Notices = nil
			})
		})
	}
}
