package engine

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

type vacuumPhaseNativeOutcome struct {
	result  Result
	notices []sqlstate.Error
	err     error
}

func vacuumPhaseNativeStart(t *testing.T, e *Engine, s *session.Session, q string) func() vacuumPhaseNativeOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := statement(t, q)
	done := make(chan vacuumPhaseNativeOutcome, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("VACUUM worker failed cleanup join")
			}
		}
	})
	go func() {
		var out vacuumPhaseNativeOutcome
		op, err := s.Acquire(ctx)
		out.err = err
		if err == nil {
			out.result, out.err = e.Execute(op, ast, 0, nil)
			out.notices = op.DetachNotices()
			op.Done()
		}
		done <- out
	}()
	return func() vacuumPhaseNativeOutcome {
		t.Helper()
		select {
		case out := <-done:
			joined = true
			cancel()
			return out
		case <-ctx.Done():
			t.Fatal("VACUUM failed to join", ctx.Err())
		}
		return vacuumPhaseNativeOutcome{}
	}
}
func vacuumPhaseNativePending(t *testing.T, e *Engine, ref ids.ObjectRef, holder *lock.Ticket) {
	t.Helper()
	lockPending(t, e, 1)
	entry := e.locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != lock.ShareUpdateExclusive || entry.Pending[0].Terminal || entry.Holders[holder].Mode != lock.Share {
		t.Fatalf("not actual target SUE behind original SHARE holder: %#v", entry)
	}
	e.mu.Lock()
	progress := len(e.vacuumProgress)
	e.mu.Unlock()
	if progress != 0 {
		t.Fatal("queued work must not invent progress", progress)
	}
}
func vacuumPhaseNativeSuccess(t *testing.T, out vacuumPhaseNativeOutcome, want []sqlstate.Error) {
	t.Helper()
	if out.err != nil || out.result.Count != 0 || out.result.Rows != nil || out.result.CommandTag != "VACUUM" {
		t.Fatal("VACUUM outcome", out.result, out.err)
	}
	if len(out.notices) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(out.notices, want) {
		t.Errorf("complete phase diagnostics got %#v want %#v", out.notices, want)
	}
}
func TestVacuumF6ExecutionPhases(t *testing.T) {
	for _, main := range []bool{true, false} {
		for _, revoke := range []bool{false, true} {
			t.Run(fmt.Sprintf("main_%t/revoke_%t", main, revoke), func(t *testing.T) {
				e := New()
				owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE ROLE phase_actor", "GRANT USAGE,CREATE ON SCHEMA public TO phase_actor", "CREATE VIEW phase_view AS SELECT 1 AS id", "GRANT MAINTAIN ON phase_view TO phase_actor"} {
					sessionSQL(t, owner, q)
				}
				ref := catalogRef(t, e, e.CatalogRead(), "phase_view")
				sessionSQL(t, worker, "SET ROLE phase_actor")
				transactionBegin(t, holder)
				defer sessionSQL(t, holder, "ROLLBACK")
				ticket := holder.Transactions.Ticket
				if err := e.locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
					t.Fatal(err)
				}
				join := vacuumPhaseNativeStart(t, e, worker, fmt.Sprintf("VACUUM (PROCESS_MAIN %t) phase_view", main))
				vacuumPhaseNativePending(t, e, ref, ticket)
				message := `skipping "phase_view" --- cannot vacuum non-tables or special system tables`
				if revoke {
					sessionSQL(t, owner, "REVOKE MAINTAIN ON phase_view FROM phase_actor")
					message = `permission denied to vacuum "phase_view", skipping it`
				}
				sessionSQL(t, holder, "ROLLBACK")
				vacuumPhaseNativeSuccess(t, join(), []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message}})
				if len(e.vacuumProgress) != 0 {
					t.Fatal("nonheap target created progress")
				}
				lockEmpty(t, e)
			})
		}
	}
}
func TestVacuumF7ExpansionAuthority(t *testing.T) {
	for _, grant := range []bool{true, false} {
		t.Run(fmt.Sprintf("child_grant_%t", grant), func(t *testing.T) {
			e := New()
			owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE ROLE expansion_actor", "GRANT USAGE ON SCHEMA public TO expansion_actor", "CREATE TABLE phase_parent(id INT)", "CREATE TABLE phase_child() INHERITS(phase_parent)", "GRANT MAINTAIN ON phase_parent TO expansion_actor"} {
				sessionSQL(t, owner, q)
			}
			if !grant {
				sessionSQL(t, owner, "GRANT MAINTAIN ON phase_child TO expansion_actor")
			}
			parent, ps := vacuumStore(t, e, "phase_parent")
			child, cs := vacuumStore(t, e, "phase_child")
			sessionSQL(t, worker, "SET ROLE expansion_actor")
			transactionBegin(t, holder)
			defer sessionSQL(t, holder, "ROLLBACK")
			ticket := holder.Transactions.Ticket
			if err := e.locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: parent}, lock.Share); err != nil {
				t.Fatal(err)
			}
			join := vacuumPhaseNativeStart(t, e, worker, "VACUUM phase_parent")
			vacuumPhaseNativePending(t, e, parent, ticket)
			if entry := e.locks.Inspect().Resources[lock.Resource{ObjectRef: child}]; entry != nil {
				t.Error("descendant was locked during expansion before parent work", entry)
			}
			if grant {
				sessionSQL(t, owner, "GRANT MAINTAIN ON phase_child TO expansion_actor")
			} else {
				sessionSQL(t, owner, "REVOKE MAINTAIN ON phase_child FROM expansion_actor")
			}
			sessionSQL(t, holder, "ROLLBACK")
			var want []sqlstate.Error
			expected := int64(1)
			if !grant {
				expected = 0
				want = []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to vacuum "phase_child", skipping it`}}
			}
			vacuumPhaseNativeSuccess(t, join(), want)
			if ps.Maintenance.VacuumCount != 1 || cs.Maintenance.VacuumCount != expected || len(e.vacuumProgress) != 0 {
				t.Error("wrong actual post-grant/revoke effects", ps.Maintenance, cs.Maintenance)
			}
			lockEmpty(t, e)
		})
	}
	t.Run("breadth_first_per_expansion", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE phase_p(id INT)", "CREATE TABLE phase_a() INHERITS(phase_p)", "CREATE TABLE phase_b() INHERITS(phase_p)", "CREATE TABLE phase_g() INHERITS(phase_a,phase_b)"} {
			sessionSQL(t, s, q)
		}
		op := transactionOperation(t, s)
		result, err := e.Execute(op, statement(t, "VACUUM (VERBOSE) phase_p,phase_a"), 0, nil)
		notices := op.DetachNotices()
		op.Done()
		var want []sqlstate.Error
		for _, name := range []string{"phase_p", "phase_a", "phase_b", "phase_g", "phase_a", "phase_g"} {
			want = append(want, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: fmt.Sprintf(`vacuuming "public"."%s" (native logical storage): 0 live rows, 0 retained dead versions, 0 dead versions removed, 0 versions frozen`, name)})
		}
		vacuumPhaseNativeSuccess(t, vacuumPhaseNativeOutcome{result, notices, err}, want)
		for name, count := range map[string]int64{"phase_p": 1, "phase_a": 2, "phase_b": 1, "phase_g": 2} {
			_, store := vacuumStore(t, e, name)
			if store.Maintenance.VacuumCount != count {
				t.Error("per-expansion diamond count", name, store.Maintenance.VacuumCount, count)
			}
		}
		lockEmpty(t, e)
	})
}

func TestVacuumF7PartitionWorkAdmission(t *testing.T) {
	for _, mode := range []string{"allowed", "revoke", "only_skip"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{
				"CREATE ROLE partition_actor",
				"GRANT USAGE ON SCHEMA public TO partition_actor",
				"CREATE TABLE phase_partition(id INT) PARTITION BY RANGE(id)",
				"CREATE TABLE phase_leaf PARTITION OF phase_partition FOR VALUES FROM(0) TO(10)",
				"INSERT INTO phase_leaf VALUES(7)",
				"GRANT MAINTAIN ON phase_partition TO partition_actor",
			} {
				sessionSQL(t, owner, q)
			}
			ref := catalogRef(t, e, e.CatalogRead(), "phase_partition")
			_, leaf := vacuumStore(t, e, "phase_leaf")
			facts := leaf.Maintenance
			sessionSQL(t, worker, "SET ROLE partition_actor")
			transactionBegin(t, holder)
			defer sessionSQL(t, holder, "ROLLBACK")
			ticket := holder.Transactions.Ticket
			// The native public LOCK partition utility is preexisting0A000;
			// this is its existing canonical manager's real holder, not a hook.
			if err := e.locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
				t.Fatal(err)
			}
			if mode == "only_skip" {
				op := transactionOperation(t, worker)
				result, err := e.Execute(op, statement(t, "VACUUM (SKIP_LOCKED,PROCESS_MAIN false) ONLY phase_partition"), 0, nil)
				notices := op.DetachNotices()
				op.Done()
				vacuumPhaseNativeSuccess(t, vacuumPhaseNativeOutcome{result, notices, err}, []sqlstate.Error{
					{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `VACUUM ONLY of partitioned table "phase_partition" has no effect`},
					{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping vacuum of "phase_partition" --- lock not available`},
				})
			} else {
				join := vacuumPhaseNativeStart(t, e, worker, "VACUUM (PROCESS_MAIN false) phase_partition")
				vacuumPhaseNativePending(t, e, ref, ticket)
				var want []sqlstate.Error
				if mode == "revoke" {
					sessionSQL(t, owner, "REVOKE MAINTAIN ON phase_partition FROM partition_actor")
					want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to vacuum "phase_partition", skipping it`})
				}
				sessionSQL(t, holder, "ROLLBACK")
				// The child is separately queued without early ACL filtering,
				// even with disabled main work; grant only the parent above.
				want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to vacuum "phase_leaf", skipping it`})
				vacuumPhaseNativeSuccess(t, join(), want)
			}
			if leaf.Maintenance != facts || len(e.vacuumProgress) != 0 {
				t.Fatal("partition admission performed heap work or leaked progress", leaf.Maintenance)
			}
			sessionSQL(t, holder, "ROLLBACK")
			transactionRows(t, owner, "SELECT id FROM phase_leaf", 7).Close()
			lockEmpty(t, e)
		})
	}
}

func TestVacuumF6NonheapOwnerAuthority(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(fmt.Sprintf("inherited_%t", inherited), func(t *testing.T) {
			e := New()
			admin, creator, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE ROLE nonheap_owner", "CREATE ROLE nonheap_actor", "GRANT USAGE,CREATE ON SCHEMA public TO nonheap_owner", "GRANT nonheap_owner TO nonheap_actor"} {
				sessionSQL(t, admin, q)
			}
			sessionSQL(t, creator, "SET ROLE nonheap_owner")
			for _, q := range []string{"CREATE SEQUENCE owner_sequence", "CREATE VIEW owner_view AS SELECT 1 AS id", "CREATE TABLE owner_table(id INT)", "CREATE INDEX owner_index ON owner_table(id)"} {
				sessionSQL(t, creator, q)
			}
			role := "nonheap_owner"
			if inherited {
				role = "nonheap_actor"
			}
			sessionSQL(t, worker, "SET ROLE "+role)
			op := transactionOperation(t, worker)
			result, err := e.Execute(op, statement(t, "VACUUM owner_sequence,owner_view,owner_index"), 0, nil)
			notices := op.DetachNotices()
			op.Done()
			var want []sqlstate.Error
			for _, name := range []string{"owner_sequence", "owner_view", "owner_index"} {
				want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf(`skipping "%s" --- cannot vacuum non-tables or special system tables`, name)})
			}
			vacuumPhaseNativeSuccess(t, vacuumPhaseNativeOutcome{result, notices, err}, want)
			_, table := vacuumStore(t, e, "owner_table")
			if table.Maintenance.VacuumCount != 0 || len(e.vacuumProgress) != 0 {
				t.Fatal("nonheap ownership performed parent heap work")
			}
			lockEmpty(t, e)
		})
	}
}
