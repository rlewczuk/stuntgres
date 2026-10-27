package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalMixedDeadlock(t *testing.T) {
	roleTransactionT2Cases(t, []string{"rd_role", "rd_a", "rd_b"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, apid := fixture.RoleTransactionConn(t, p, b)
		w, wpid := fixture.RoleTransactionConn(t, p, b)
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE rd_role", "CREATE ROLE rd_a", "CREATE ROLE rd_b", "CREATE TABLE rd_lock(i INT)", "CREATE TABLE rd_sentinel(i INT)"} {
			run(a, fixture.Exec(q, 0))
		}
		run(a, fixture.Exec("INSERT INTO rd_sentinel VALUES(7)", 1))
		var role, relation, database int64
		if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='rd_role'").Scan(&role); err != nil {
			t.Fatal(err)
		}
		if err := a.QueryRowContext(p.Ctx, "SELECT 'rd_lock'::regclass::bigint").Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(a, fixture.Exec("BEGIN", 0))
		run(a, fixture.Exec("LOCK TABLE rd_lock IN ACCESS EXCLUSIVE MODE", 0))
		run(a, fixture.Exec("INSERT INTO rd_sentinel VALUES(8)", 1))
		run(w, fixture.Exec("BEGIN", 0))
		run(w, fixture.Exec("GRANT rd_role TO rd_b", 0))
		run(w, fixture.Exec("INSERT INTO rd_sentinel VALUES(9)", 1))
		first, completed := roleTransactionT2Worker(t, p, a, "GRANT rd_role TO rd_a", true)
		t.Cleanup(func() { w.ExecContext(p.Ctx, "ROLLBACK") })
		fixture.RoleTransactionWait(t, p, b, apid, wpid, completed)
		second, _ := roleTransactionT2Worker(t, p, w, "LOCK TABLE rd_lock IN ACCESS SHARE MODE", false)
		left, right := first(), second()
		victim := roleTransactionDeadlockVictim(t, left, right)
		err := left.Err
		if victim == 1 {
			err = right.Err
		}
		lines := []string{
			fmt.Sprintf("Process %d waits for ShareUpdateExclusiveLock on object %d of class 1260 of database 0; blocked by process %d.", apid, role, wpid),
			fmt.Sprintf("Process %d waits for AccessShareLock on relation %d of database %d; blocked by process %d.", wpid, relation, database, apid),
		}
		roleTransactionDeadlockDiagnostic(t, b, err, lines, []int64{role}, relation, []int{apid, wpid})
		owners := []*sql.Conn{a, w}
		run(owners[victim], fixture.Exec("ROLLBACK", 0))
		run(owners[1-victim], fixture.Exec("COMMIT", 0))
		run(a, fixture.Select("SELECT pg_has_role('rd_a','rd_role','MEMBER') AS a,pg_has_role('rd_b','rd_role','MEMBER') AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{victim == 1, victim == 0}}))
		value := int64(8)
		if victim == 0 {
			value = 9
		}
		run(a, fixture.Select("SELECT i FROM rd_sentinel ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {value}}))
		for _, c := range owners {
			run(c, fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
	})
}

func roleTransactionDeadlockVictim(t *testing.T, left, right fixture.RelationOutcome) int {
	t.Helper()
	victim := -1
	for i, outcome := range []fixture.RelationOutcome{left, right} {
		if outcome.Err == nil {
			fixture.LockAffected(t, outcome.Result, 0)
			continue
		}
		fixture.State(t, outcome.Err, "40P01")
		if victim != -1 || outcome.Result != nil {
			t.Fatal("deadlock must have one victim with no result")
		}
		victim = i
	}
	if victim == -1 {
		t.Fatal("cycle completed without deadlock victim")
	}
	return victim
}

func roleTransactionDeadlockDiagnostic(t *testing.T, b fixture.Backend, err error, lines []string, roles []int64, relation int64, sessions []int) {
	t.Helper()
	diagnostic := fixture.ExtractDiagnostic(t, err)
	want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	if b.PG {
		forward, reverse := strings.Join(lines, "\n"), lines[1]+"\n"+lines[0]
		if diagnostic.Detail != forward && diagnostic.Detail != reverse {
			t.Fatalf("deadlock edge identities: %q; want %q or %q", diagnostic.Detail, forward, reverse)
		}
		want.Detail = diagnostic.Detail
	} else {
		roleTransactionNativeDeadlockDetail(t, diagnostic.Detail, roles, relation, sessions)
		want.Detail = diagnostic.Detail
	}
	fixture.AssertDiagnostic(t, err, &want)
}

func roleTransactionCrossDeadlock(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, apid int, w *sql.Conn, wpid int) {
	t.Helper()
	run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
	var firstRole, secondRole int64
	if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='rx_role'").Scan(&firstRole); err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='rx_second_role'").Scan(&secondRole); err != nil {
		t.Fatal(err)
	}
	run(a, fixture.Exec("BEGIN", 0))
	run(a, fixture.Exec("GRANT rx_role TO rx_member", 0))
	run(w, fixture.Exec("BEGIN", 0))
	run(w, fixture.Exec("GRANT rx_second_role TO rx_private", 0))
	first, completed := roleTransactionT2Worker(t, p, a, "GRANT rx_second_role TO rx_member", true)
	t.Cleanup(func() { w.ExecContext(p.Ctx, "ROLLBACK") })
	fixture.RoleTransactionWait(t, p, b, apid, wpid, completed)
	second, _ := roleTransactionT2Worker(t, p, w, "GRANT rx_role TO rx_private", true)
	left, right := first(), second()
	victim := roleTransactionDeadlockVictim(t, left, right)
	err := left.Err
	if victim == 1 {
		err = right.Err
	}
	roleTransactionDeadlockDiagnostic(t, b, err, []string{
		fmt.Sprintf("Process %d waits for ShareUpdateExclusiveLock on object %d of class 1260 of database 0; blocked by process %d.", apid, secondRole, wpid),
		fmt.Sprintf("Process %d waits for ShareUpdateExclusiveLock on object %d of class 1260 of database 0; blocked by process %d.", wpid, firstRole, apid),
	}, []int64{firstRole, secondRole}, 0, []int{apid, wpid})
	owners := []*sql.Conn{a, w}
	run(owners[victim], fixture.Exec("ROLLBACK", 0))
	run(owners[1-victim], fixture.Exec("COMMIT", 0))
	for _, role := range []string{"rx_role", "rx_second_role"} {
		run(a, fixture.Select("SELECT pg_has_role('rx_member','"+role+"','MEMBER') AS member,pg_has_role('rx_private','"+role+"','MEMBER') AS private", []string{"member", "private"}, []string{"BOOL", "BOOL"}, [][]any{{victim == 1, victim == 0}}))
	}
}
