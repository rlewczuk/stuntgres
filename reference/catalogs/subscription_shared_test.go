package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSubscriptionSharedCatalog(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sub_shared_owner", "sub_shared_target", "sub_shared_localowner"})
	databases := []string{"sub_shared_database"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := subscriptionScenario(t, "sub_shared", "sub_shared_renamed")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE DATABASE sub_shared_database", false)
			other, raw, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "sub_shared_database", "")
			ob := b
			ob.DB = other
			ob.Recorder = recorder
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				var n int
				if err := other.QueryRowContext(ctx, "SELECT count(*) FROM pg_subscription WHERE subname='sub_shared' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())").Scan(&n); err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
				for _, q := range []string{"ALTER SUBSCRIPTION sub_shared DISABLE", "ALTER SUBSCRIPTION sub_shared SET (slot_name=NONE)", "DROP SUBSCRIPTION sub_shared"} {
					if _, err := other.ExecContext(ctx, q); err != nil {
						t.Error(err)
						return
					}
				}
			})
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_shared CONNECTION 'application_name=first' PUBLICATION p WITH (connect=false,slot_name=NONE)", false)
			subscriptionExec(t, p, ob, other, "CREATE SUBSCRIPTION sub_shared CONNECTION 'application_name=second' PUBLICATION q WITH (connect=false,slot_name=NONE)", true)
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_shared_owner", false)
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_shared_target", false)
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_shared_localowner", false)
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_shared OWNER TO sub_shared_localowner", true)
			subscriptionExec(t, p, ob, other, "ALTER SUBSCRIPTION sub_shared OWNER TO sub_shared_owner", true)
			dependency := fixture.ControlsReferenceDiagnostic("2BP01", `role "sub_shared_owner" cannot be dropped because some objects depend on it`)
			dependency.Detail = "owner of subscription sub_shared"
			rejected := fixture.ErrorDiagnostic("DROP ROLE sub_shared_owner", dependency)
			rejected.ErrorStage = "execute"
			subscriptionRun(t, p, b, b.DB, rejected)
			subscriptionReassignContention(t, p, b, ob, other)
			peerOwnerName := "sub_shared_target"
			if b.PG {
				peerOwnerName = "sub_shared_owner"
			}
			subscriptionRun(t, p, ob, other, fixture.Select("SELECT r.rolname FROM pg_subscription s JOIN pg_roles r ON r.oid=s.subowner WHERE subconninfo='application_name=second'", []string{"rolname"}, []string{"NAME"}, [][]any{{peerOwnerName}}))
			subscriptionExec(t, p, b, b.DB, "DROP OWNED BY sub_shared_target", true)
			subscriptionRun(t, p, ob, other, fixture.Select("SELECT subpublications,subenabled FROM pg_subscription WHERE subname='sub_shared' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"subpublications", "subenabled"}, []string{"_TEXT", "BOOL"}, [][]any{{"{q}", false}}))
			if b.PG {
				// Failed catalog tuple publication retains the original dependency.
				subscriptionRun(t, p, b, b.DB, rejected)
			} else {
				subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_shared_owner", false)
			}
			for _, endpoint := range []struct {
				b fixture.Backend
				e fixture.Executor
			}{{b, b.DB}, {ob, other}} {
				subscriptionRun(t, p, endpoint.b, endpoint.e, fixture.Select("SELECT subconninfo,subpublications FROM pg_subscription WHERE subname='sub_shared' ORDER BY subconninfo", []string{"subconninfo", "subpublications"}, []string{"TEXT", "_TEXT"}, [][]any{{"application_name=first", "{p}"}, {"application_name=second", "{q}"}}))
			}
			count := subscriptionNumber(t, p, b.DB, "SELECT count(DISTINCT oid)::bigint FROM pg_subscription WHERE subname='sub_shared'")
			if count != 2 {
				t.Fatal("subscription OIDs not cluster unique", count)
			}
			count = subscriptionNumber(t, p, b.DB, "SELECT count(DISTINCT subdbid)::bigint FROM pg_subscription WHERE subname='sub_shared'")
			if count != 2 {
				t.Fatal("subscription databases not distinct", count)
			}
			a, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Rollback()
			c, err := other.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Rollback()
			subscriptionExec(t, p, b, a, "ALTER SUBSCRIPTION sub_shared SET (binary=true)", true)
			subscriptionExec(t, p, ob, c, "ALTER SUBSCRIPTION sub_shared SET (streaming=false)", true)
			commits := make(chan error, 2)
			go func() { commits <- a.Commit() }()
			go func() { commits <- c.Commit() }()
			var first, second error
			select {
			case first = <-commits:
			case <-p.Ctx.Done():
				t.Fatal("first cross-database commit did not join", p.Ctx.Err())
			}
			select {
			case second = <-commits:
			case <-p.Ctx.Done():
				t.Fatal("second cross-database commit did not join", p.Ctx.Err())
			}
			if first != nil || second != nil {
				t.Fatal("independent cross-database commits", first, second)
			}
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subconninfo,subbinary,substream FROM pg_subscription WHERE subname='sub_shared' ORDER BY subconninfo", []string{"subconninfo", "subbinary", "substream"}, []string{"TEXT", "BOOL", "CHAR"}, [][]any{{"application_name=first", true, "p"}, {"application_name=second", false, "f"}}))
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_shared RENAME TO sub_shared_renamed", true)
			subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_shared DISABLE", "42704", `subscription "sub_shared" does not exist`, false)
			subscriptionList(t, p, ob, other, "sub_shared", "{q}", false)
			if !b.PG {
				// Host replacement of one logical database must not erase its peer.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				oldID := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'")
				peerID := subscriptionNumber(t, p, other, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_shared'")
				peerOwner := subscriptionNumber(t, p, other, "SELECT subowner::bigint FROM pg_subscription WHERE subname='sub_shared'")
				localOwner := subscriptionNumber(t, p, b.DB, "SELECT subowner::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'")
				localDatabase := subscriptionNumber(t, p, b.DB, "SELECT subdbid::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'")
				peerDatabase := subscriptionNumber(t, p, other, "SELECT subdbid::bigint FROM pg_subscription WHERE subname='sub_shared'")
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				subscriptionList(t, p, ob, other, "sub_shared", "{q}", false)
				if err := subscriptionRestoreDuringRead(t, p, dump.Bytes(), func(context.CancelFunc) {
					subscriptionExec(t, p, ob, other, "ALTER SUBSCRIPTION sub_shared SET (origin=none)", true)
				}); err != nil {
					t.Fatal(err)
				}
				newID := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'")
				if newID <= 0 || newID == oldID || newID == peerID {
					t.Fatal("Restore did not remint selected subscription identity", oldID, newID, peerID)
				}
				if got := subscriptionNumber(t, p, other, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_shared'"); got != peerID {
					t.Fatal("Restore reminted peer identity", peerID, got)
				}
				if got := subscriptionNumber(t, p, other, "SELECT subowner::bigint FROM pg_subscription WHERE subname='sub_shared'"); got != peerOwner {
					t.Fatal("Restore changed peer owner identity", peerOwner, got)
				}
				for _, identity := range []struct {
					query string
					want  int64
				}{
					{"SELECT subowner::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'", localOwner},
					{"SELECT subdbid::bigint FROM pg_subscription WHERE subname='sub_shared_renamed'", localDatabase},
					{"SELECT subdbid::bigint FROM pg_subscription WHERE subname='sub_shared'", peerDatabase},
				} {
					if got := subscriptionNumber(t, p, b.DB, identity.query); got != identity.want {
						t.Fatal("Restore changed retained principal/database identity", identity.query, got, identity.want)
					}
				}
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subname,subskiplsn,subenabled,subbinary,substream,subtwophasestate,subdisableonerr,subpasswordrequired,subrunasowner,subfailover,subconninfo,subslotname,subsynccommit,subpublications,suborigin FROM pg_subscription ORDER BY subname", []string{"subname", "subskiplsn", "subenabled", "subbinary", "substream", "subtwophasestate", "subdisableonerr", "subpasswordrequired", "subrunasowner", "subfailover", "subconninfo", "subslotname", "subsynccommit", "subpublications", "suborigin"}, []string{"NAME", "PG_LSN", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "NAME", "TEXT", "_TEXT", "TEXT"}, [][]any{
					{"sub_shared", "0/0", false, false, "f", "d", true, true, false, false, "application_name=second", nil, "off", "{q}", "none"},
					{"sub_shared_renamed", "0/0", false, true, "p", "d", false, true, false, false, "application_name=first", nil, "off", "{p}", "any"},
				}))
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT s.subname,d.datname=current_database() AS local_database,r.rolname FROM pg_subscription s JOIN pg_database d ON d.oid=s.subdbid JOIN pg_roles r ON r.oid=s.subowner ORDER BY s.subname", []string{"subname", "local_database", "rolname"}, []string{"NAME", "BOOL", "NAME"}, [][]any{{"sub_shared", false, "sub_shared_target"}, {"sub_shared_renamed", true, "sub_shared_localowner"}}))
				dependency := fixture.ControlsReferenceDiagnostic("2BP01", `role "sub_shared_target" cannot be dropped because some objects depend on it`)
				dependency.Detail = "owner of subscription sub_shared"
				rejected := fixture.ErrorDiagnostic("DROP ROLE sub_shared_target", dependency)
				rejected.ErrorStage = "execute"
				subscriptionRun(t, p, b, b.DB, rejected)
				localDependency := fixture.ControlsReferenceDiagnostic("2BP01", `role "sub_shared_localowner" cannot be dropped because some objects depend on it`)
				localDependency.Detail = "owner of subscription sub_shared_renamed"
				localRejected := fixture.ErrorDiagnostic("DROP ROLE sub_shared_localowner", localDependency)
				localRejected.ErrorStage = "execute"
				subscriptionRun(t, p, b, b.DB, localRejected)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, raw); err != nil {
					t.Fatal(err)
				}
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT count(*) AS n FROM pg_subscription", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			} else {
				subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_shared_renamed", false)
				subscriptionExec(t, p, ob, other, "DROP SUBSCRIPTION sub_shared", false)
			}
			subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_shared_target", false)
			if b.PG {
				subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_shared_owner", false)
			}
			subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_shared_localowner", false)
		})
	}
}

// Local ALTER and another database's REASSIGN contend on one shared definition.
// PostgreSQL's owner handler does not take ALTER's shared-object lock: it waits
// in CatalogTupleUpdate and rejects the concurrently updated tuple. Native
// object admission instead recaptures authority before building its candidate.
func subscriptionReassignContention(t *testing.T, p *fixture.Pair, b, ob fixture.Backend, other *sql.DB) {
	t.Helper()
	holder, err := other.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	waiter, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	holderPID := int(subscriptionNumber(t, p, holder, "SELECT pg_backend_pid()"))
	waiterPID := int(subscriptionNumber(t, p, waiter, "SELECT pg_backend_pid()"))
	object := subscriptionNumber(t, p, holder, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_shared' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())")
	tx, err := holder.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	subscriptionExec(t, p, ob, tx, "ALTER SUBSCRIPTION sub_shared SET (disable_on_error=true)", false)
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	finished := make(chan struct{})
	join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		result, err := waiter.ExecContext(ctx, "REASSIGN OWNED BY sub_shared_owner TO sub_shared_target")
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	if b.PG {
		fixture.TransactionLockPending(t, p, waiterPID, holderPID, finished)
	} else {
		fixture.ObjectLockPending(t, p, b, waiterPID, holderPID, 6100, object, "AccessExclusiveLock", finished)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	outcome := join()
	if b.PG {
		want := fixture.ControlsReferenceDiagnostic("XX000", "tuple concurrently updated")
		fixture.AssertDiagnostic(t, outcome.Err, &want)
	} else if outcome.Err != nil {
		t.Fatal("REASSIGN after admitted local ALTER", outcome.Err)
	}
	subscriptionRun(t, p, ob, other, fixture.Select("SELECT subdisableonerr FROM pg_subscription WHERE subname='sub_shared' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"subdisableonerr"}, []string{"BOOL"}, [][]any{{true}}))
}

func TestReferenceSubscriptionNameContention(t *testing.T) {
	p := subscriptionScenario(t, "sub_name_contended")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			holderPID := int(subscriptionNumber(t, p, holder, "SELECT pg_backend_pid()"))
			waiterPID := int(subscriptionNumber(t, p, waiter, "SELECT pg_backend_pid()"))
			tx, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			subscriptionExec(t, p, b, tx, "CREATE SUBSCRIPTION sub_name_contended CONNECTION 'application_name=rolled_back' PUBLICATION abandoned WITH (connect=false,slot_name=NONE)", false)
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				result, err := waiter.ExecContext(ctx, "CREATE SUBSCRIPTION sub_name_contended CONNECTION 'application_name=winner' PUBLICATION winner WITH (connect=false,slot_name=NONE)")
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			fixture.RelationLockPending(t, p, b, waiterPID, holderPID, 0, "", finished)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			outcome := join()
			if outcome.Err != nil {
				t.Fatal("same-name CREATE after holder rollback", outcome.Err)
			}
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subconninfo,subpublications FROM pg_subscription WHERE subname='sub_name_contended'", []string{"subconninfo", "subpublications"}, []string{"TEXT", "_TEXT"}, [][]any{{"application_name=winner", "{winner}"}}))
			subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_name_contended", false)
		})
	}
}
