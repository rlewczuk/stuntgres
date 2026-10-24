package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// The PostgreSQL branches are independently expected candidate contracts until
// the root records the pinned oracle: late ownership checking rejects a revoked
// owner, while DefineRule's post-transform name lookup selects a new shadow.
// Native publication/admission fences are ordinary errors, not SQL diagnostics.
func TestReferenceTransactionalRulesPhaseAuthority(t *testing.T) {
	for _, change := range []string{"membership-revoked", "membership-stable", "event-shadow-created", "event-shadow-stable"} {
		for _, mode := range []string{"autocommit", "sql", "go"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%v", change, mode, held), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"txr_phase_actor", "txr_phase_owner"})
					fixture.SchemaReferenceCleanup(t, "early", "late")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							transactionalRulesPhaseAuthority(t, p, b, change, mode, held)
						})
					}
				})
			}
		}
	}
}

func transactionalRulesPhaseAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, change, mode string, held bool) {
	t.Helper()
	c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	base := transactionalRulesRun(t, p, b, c, nil)
	observe := transactionalRulesRun(t, p, b, peer, nil)
	membership := change == "membership-revoked" || change == "membership-stable"
	event, selected := "src", "public.src"
	for _, q := range []string{"CREATE TABLE public.sink1(id INT,v INT)", "CREATE TABLE public.sink2(id INT,v INT)"} {
		base(fixture.Exec(q, 0))
	}
	if membership {
		for _, q := range []string{
			"CREATE ROLE txr_phase_owner", "CREATE ROLE txr_phase_actor",
			"GRANT txr_phase_owner TO txr_phase_actor",
			"GRANT USAGE ON SCHEMA public TO txr_phase_actor,txr_phase_owner",
			"CREATE TABLE public.src(id INT,v INT)",
			"ALTER TABLE public.src OWNER TO txr_phase_owner",
			"GRANT INSERT ON public.sink1,public.sink2 TO txr_phase_actor,txr_phase_owner",
			"SET SESSION AUTHORIZATION txr_phase_actor",
		} {
			base(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	} else {
		for _, q := range []string{"CREATE SCHEMA early", "CREATE SCHEMA late", "CREATE TABLE late.src(id INT,v INT)", "SET search_path=early,late"} {
			base(fixture.Exec(q, 0))
		}
		selected = "late.src"
		if change == "event-shadow-stable" {
			base(fixture.Exec("CREATE TABLE early.src(id INT,v INT)", 0))
			selected = "early.src"
		}
	}
	q := "CREATE RULE phase_authority AS ON INSERT TO " + event + " DO ALSO (INSERT INTO public.sink1 VALUES(NEW.id,NEW.v); INSERT INTO public.sink2 VALUES(NEW.id,abs(NEW.v)))"
	handles := transactionalRulesHandles(t, p, c, held, q)
	e, finish := transactionalViewsBegin(t, p, b, c, mode)
	ended := false
	defer func() {
		if !ended {
			finish(false)
		}
	}()
	var x fixture.Executor = e
	if stmt := handles[q]; stmt != nil {
		x = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
	}
	out := transactionalRulesPhaseWait(t, p, b, c, holder, x, q, func() {
		switch change {
		case "membership-revoked":
			observe(fixture.Exec("REVOKE txr_phase_owner FROM txr_phase_actor", 0))
		case "event-shadow-created":
			observe(fixture.Exec("CREATE TABLE early.src(id INT,v INT)", 0))
		}
	})
	rejected := change == "membership-revoked" || change == "event-shadow-created" && !b.PG
	if change == "membership-revoked" {
		if b.PG {
			fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table src"})
		} else {
			fixture.TransactionOrdinary(t, out.Err, "stuntgres: authorization changed before publication")
		}
	} else if change == "event-shadow-created" && !b.PG {
		fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
	} else {
		fixture.State(t, out.Err, "")
		if out.Result == nil {
			t.Fatal("missing CREATE RULE result")
		}
		if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
			t.Fatal("CREATE RULE affected", n, err)
		}
	}
	finish(!rejected)
	ended = true
	if change == "event-shadow-created" {
		// Independently expected PostgreSQL second lookup; never native retargeting.
		selected = "early.src"
	}
	var names []string
	if !rejected {
		names = []string{"phase_authority"}
	}
	observe(transactionalRulesPhaseNames(selected, names...))
	observe(transactionalRulesRows(selected))
	if !membership {
		other := "early.src"
		if selected == other {
			other = "late.src"
		}
		observe(transactionalRulesPhaseNames(other))
		observe(transactionalRulesRows(other))
	}
	observe(transactionalRulesRows("public.sink1"))
	observe(transactionalRulesRows("public.sink2"))
	if !rejected {
		observe(fixture.Exec("INSERT INTO "+selected+" VALUES(1,-10)", 1))
		observe(transactionalRulesRows(selected, []any{int64(1), int64(-10)}))
		observe(transactionalRulesRows("public.sink1", []any{int64(1), int64(-10)}))
		observe(transactionalRulesRows("public.sink2", []any{int64(1), int64(10)}))
		observe(fixture.Exec("DROP RULE phase_authority ON "+selected, 0))
	} else {
		// A rejected definition must not fire for a fresh privileged writer.
		observe(fixture.Exec("INSERT INTO "+selected+" VALUES(1,-10)", 1))
		observe(transactionalRulesRows(selected, []any{int64(1), int64(-10)}))
		observe(transactionalRulesRows("public.sink1"))
		observe(transactionalRulesRows("public.sink2"))
	}
	if change == "membership-revoked" {
		observe(fixture.Exec("GRANT txr_phase_owner TO txr_phase_actor", 0))
	}
	// Recover on the same physical owner and genuinely reuse the utility handle.
	e, finish = transactionalViewsBegin(t, p, b, c, mode)
	ended = false
	run := transactionalRulesRun(t, p, b, e, handles)
	run(fixture.Exec(q, 0))
	run(fixture.Exec("INSERT INTO "+selected+" VALUES(2,-20)", 1))
	finish(true)
	ended = true
	observe(transactionalRulesPhaseNames(selected, "phase_authority"))
	observe(transactionalRulesRows(selected, []any{int64(1), int64(-10)}, []any{int64(2), int64(-20)}))
	var prefix1, prefix2 [][]any
	if !rejected {
		prefix1 = append(prefix1, []any{int64(1), int64(-10)})
		prefix2 = append(prefix2, []any{int64(1), int64(10)})
	}
	observe(transactionalRulesRows("public.sink1", append(prefix1, []any{int64(2), int64(-20)})...))
	observe(transactionalRulesRows("public.sink2", append(prefix2, []any{int64(2), int64(20)})...))
}

func transactionalRulesPhaseNames(event string, names ...string) fixture.Step {
	rows := make([][]any, 0, len(names))
	for _, name := range names {
		rows = append(rows, []any{name})
	}
	return fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='"+event+"'::regclass ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, rows)
}

func transactionalRulesPhaseWait(t *testing.T, p *fixture.Pair, b fixture.Backend, c, holder *sql.Conn, x fixture.Executor, q string, change func()) fixture.RelationOutcome {
	t.Helper()
	pid, hpid := 0, 0
	var oid int64
	if b.PG {
		pid, hpid = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
		oid = fixture.LockOID(t, p.Ctx, "public.sink2")
	}
	htx := fixture.QueryLockBegin(t, p.Ctx, holder)
	defer htx.Rollback()
	fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec("LOCK TABLE public.sink2 IN ACCESS EXCLUSIVE MODE", 0))
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	done := make(chan struct{})
	join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(done)
		r, err := x.ExecContext(ctx, q)
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	joined := false
	defer func() {
		cancel()
		htx.Rollback()
		if !joined {
			join()
		}
	}()
	// Observe the actual later action wait before changing any authority/name.
	fixture.RelationLockPending(t, p, b, pid, hpid, oid, "RowExclusiveLock", done)
	change()
	if err := htx.Rollback(); err != nil {
		t.Fatal(err)
	}
	out := join()
	joined = true
	return out
}
