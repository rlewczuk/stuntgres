package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesTransactions(t *testing.T) {
	t.Run("commit-rollback-dedup-savepoints", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			for _, q := range []string{"LISTEN a", "LISTEN b"} {
				sessionMessageSQL(t, p, b, r, q)
			}
			for _, q := range []string{
				"BEGIN", "NOTIFY a, 'rolled-back'", "ROLLBACK",
				"BEGIN", "NOTIFY a, '1'", "NOTIFY b, '1'", "NOTIFY a, '1'", "NOTIFY a, '2'",
				"SAVEPOINT child", "NOTIFY a, 'child'", "UNLISTEN a", "ROLLBACK TO child",
				"NOTIFY a, '1'", "SAVEPOINT kept", "NOTIFY b, 'kept'", "RELEASE kept", "COMMIT",
			} {
				sessionMessageSQL(t, p, b, s, q)
			}
			for _, event := range [][2]string{{"a", "1"}, {"b", "1"}, {"a", "2"}, {"b", "kept"}} {
				sessionMessageReceive(t, p, b, r, event[0], event[1])
			}
			sessionMessageSQL(t, p, b, s, "NOTIFY a, '1'")
			sessionMessageReceive(t, p, b, r, "a", "1")
			// A failed child must restore the parent's event root as well as
			// explicit ROLLBACK TO; a later duplicate is still folded.
			for _, q := range []string{"BEGIN", "NOTIFY a, 'parent'", "SAVEPOINT bad", "NOTIFY a, 'lost'"} {
				sessionMessageSQL(t, p, b, s, q)
			}
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error("SELECT 1/0", "22012"))
			for _, q := range []string{"ROLLBACK TO bad", "NOTIFY a, 'parent'", "NOTIFY b, 'after-error'", "COMMIT"} {
				sessionMessageSQL(t, p, b, s, q)
			}
			sessionMessageReceive(t, p, b, r, "a", "parent")
			sessionMessageReceive(t, p, b, r, "b", "after-error")
			for _, q := range []string{"BEGIN READ ONLY", "LISTEN readonly", "NOTIFY a, 'read-only'", "UNLISTEN readonly", "COMMIT"} {
				sessionMessageSQL(t, p, b, s, q)
			}
			sessionMessageReceive(t, p, b, r, "a", "read-only")
		})
	})

	t.Run("precommit-withheld", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN a")
			sessionMessageSQL(t, p, b, s, "BEGIN")
			sessionMessageSQL(t, p, b, s, "NOTIFY a, 'uncommitted'")
			sessionMessageAbsent(t, p, b, r)
		})
	})

	t.Run("self-and-subscription-savepoints", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			for _, q := range []string{
				"LISTEN a", "BEGIN", "NOTIFY a, 'self-rollback'", "ROLLBACK",
				"BEGIN", "NOTIFY a, 'self-commit'", "COMMIT",
			} {
				sessionMessageSQL(t, p, b, r, q)
			}
			sessionMessageReceive(t, p, b, r, "a", "self-commit")
			for _, q := range []string{
				"BEGIN", "SAVEPOINT child", "UNLISTEN a", "LISTEN lost", "ROLLBACK TO child",
				"SAVEPOINT kept", "LISTEN b", "RELEASE kept",
			} {
				sessionMessageSQL(t, p, b, r, q)
			}
			sessionMessageSQL(t, p, b, s, "NOTIFY a, 'kept-parent'")
			sessionMessageSQL(t, p, b, s, "NOTIFY lost, 'rolled-back-child'")
			sessionMessageSQL(t, p, b, r, "COMMIT")
			sessionMessageSQL(t, p, b, s, "NOTIFY b, 'released-child'")
			sessionMessageReceive(t, p, b, r, "a", "kept-parent")
			sessionMessageReceive(t, p, b, r, "b", "released-child")
		})
	})

	t.Run("implicit-message-and-internal-commit", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN a")
			sessionMessageSQL(t, p, b, s, "CREATE TABLE message_atomic(v INT)")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error(
				"INSERT INTO message_atomic VALUES(1); NOTIFY a, 'implicit-lost'; SELECT 1/0", "22012"))
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT v FROM message_atomic", []string{"v"}, []string{"INT4"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error(
				"BEGIN; INSERT INTO message_atomic VALUES(2); NOTIFY a, 'prefix'; COMMIT; BEGIN; NOTIFY a, 'suffix'; SELECT 1/0", "22012"))
			sessionMessageSQL(t, p, b, s, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT v FROM message_atomic", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
			sessionMessageSQL(t, p, b, s, "NOTIFY a, 'sentinel'")
			sessionMessageReceive(t, p, b, r, "a", "prefix")
			sessionMessageReceive(t, p, b, r, "a", "sentinel")
		})
	})
}
