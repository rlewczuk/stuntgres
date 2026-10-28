package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func subscriptionControlScenario(t *testing.T, name string) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"sub_control_owner"})
	p := subscriptionScenario(t, name, "SubEnabledControl")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_control_owner", false)
			subscriptionExec(t, p, b, b.DB, "CREATE TABLE sub_control_sentinel(v int)", false)
			subscriptionRun(t, p, b, b.DB, fixture.Exec("INSERT INTO sub_control_sentinel VALUES (77)", 1))
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION "+name+" CONNECTION 'host=publisher.invalid password=secret application_name=controls' PUBLICATION second,first WITH (connect=false,slot_name=NONE,binary=true,streaming=true,two_phase=true,disable_on_error=true,password_required=false,run_as_owner=true,failover=true,synchronous_commit=remote_apply,origin=none)", true)
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+name+" SKIP (lsn='A/1234')", true)
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+name+" OWNER TO sub_control_owner", true)
			query := "SELECT subskiplsn,subenabled,subbinary,substream,subtwophasestate,subdisableonerr,subpasswordrequired,subrunasowner,subfailover,subconninfo,subslotname,subsynccommit,subpublications,suborigin FROM pg_subscription WHERE subname='" + name + "'"
			want := fixture.Select(query, []string{"subskiplsn", "subenabled", "subbinary", "substream", "subtwophasestate", "subdisableonerr", "subpasswordrequired", "subrunasowner", "subfailover", "subconninfo", "subslotname", "subsynccommit", "subpublications", "suborigin"}, []string{"PG_LSN", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "NAME", "TEXT", "_TEXT", "TEXT"}, [][]any{{"A/1234", false, true, "t", "p", true, false, true, true, "host=publisher.invalid password=secret application_name=controls", nil, "remote_apply", "{second,first}", "none"}})
			check := func() {
				subscriptionRun(t, p, b, b.DB, want)
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT r.rolname,d.datname=current_database() AS local_database FROM pg_subscription s JOIN pg_roles r ON r.oid=s.subowner JOIN pg_database d ON d.oid=s.subdbid WHERE s.subname='"+name+"'", []string{"rolname", "local_database"}, []string{"NAME", "BOOL"}, [][]any{{"sub_control_owner", true}}))
			}
			check()
			if !b.PG {
				subscriptionExec(t, p, b, b.DB, `CREATE SUBSCRIPTION "SubEnabledControl" CONNECTION '' PUBLICATION enabled_remote WITH (connect=false,two_phase=true)`, false)
				subscriptionExec(t, p, b, b.DB, `ALTER SUBSCRIPTION "SubEnabledControl" ENABLE`, true)
				old, err := p.Native.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				var first, second bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &first); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(first.Bytes(), second.Bytes()) {
					t.Fatal("subscription dump is not deterministic")
				}
				failure := errors.New("subscription control I/O")
				if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(first.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(first.String()+"CREATE SUBSCRIPTION invalid_late CONNECTION 'testconn' PUBLICATION p WITH (connect=false);")); err == nil {
					t.Fatal("invalid later subscription restore published")
				}
				check()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				subscriptionRun(t, p, b, b.DB, subscriptionEmpty(false, "SELECT * FROM pg_subscription WHERE subname='"+name+"'", false))
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(first.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT v FROM sub_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
				subscriptionRun(t, p, b, b.DB, fixture.Select(`SELECT subenabled,subslotname,subtwophasestate FROM pg_subscription WHERE subname='SubEnabledControl'`, []string{"subenabled", "subslotname", "subtwophasestate"}, []string{"BOOL", "NAME", "CHAR"}, [][]any{{true, "SubEnabledControl", "p"}}))
				// The actual typed Rows were acquired before replacement, not reconstructed.
				if !old.Next() {
					t.Fatal("retained subscription row missing", old.Err())
				}
				var lsn, stream, state, connection, sync, publications, origin string
				var enabled, binary, disable, password, run, failover bool
				var slot any
				if err := old.Scan(&lsn, &enabled, &binary, &stream, &state, &disable, &password, &run, &failover, &connection, &slot, &sync, &publications, &origin); err != nil {
					t.Fatal(err)
				}
				if lsn != "A/1234" || enabled || !binary || stream != "t" || state != "p" || !disable || password || !run || !failover || slot != nil || publications != "{second,first}" || sync != "remote_apply" || origin != "none" || connection != "host=publisher.invalid password=secret application_name=controls" {
					t.Fatal("retained row changed")
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained subscription cardinality", old.Err())
				}
				old.Close()
				subscriptionDetach(t, p, b, b.DB, `"SubEnabledControl"`)
			}
			subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION "+name, true)
			subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_control_owner", false)
		})
	}
}
func TestReferenceSubscriptionControls(t *testing.T) { subscriptionControlScenario(t, "sub_controls") }
func TestReferenceSubscriptionControlsSmoke(t *testing.T) {
	subscriptionControlScenario(t, "sub_controls_smoke")
}

func TestReferenceSubscriptionPopulatedACLControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sub_secret_owner", "sub_secret_delegate"})
	p := subscriptionScenario(t, "sub_secret")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			subscriptionExec(t, p, b, admin, "CREATE ROLE sub_secret_owner", false)
			subscriptionExec(t, p, b, admin, "CREATE ROLE sub_secret_delegate", false)
			subscriptionExec(t, p, b, admin, "CREATE SUBSCRIPTION sub_secret CONNECTION 'host=publisher.invalid password=actual_secret' PUBLICATION remote_secret WITH (connect=false,slot_name=NONE)", false)
			subscriptionExec(t, p, b, admin, "ALTER SUBSCRIPTION sub_secret OWNER TO sub_secret_owner", false)
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "REVOKE SELECT(subconninfo) ON pg_catalog.pg_subscription FROM sub_secret_owner,sub_secret_delegate", "REVOKE sub_secret_delegate FROM sub_secret_owner"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
			}()
			subscriptionExec(t, p, b, actor, "SET ROLE sub_secret_owner", false)
			query := "SELECT subconninfo FROM pg_subscription WHERE subname='sub_secret'"
			stmt, err := actor.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: query}
			check := func(allowed bool) {
				for _, executor := range []fixture.Executor{actor, held} {
					if allowed {
						subscriptionRun(t, p, b, executor, fixture.Select(query, []string{"subconninfo"}, []string{"TEXT"}, [][]any{{"host=publisher.invalid password=actual_secret"}}))
					} else {
						subscriptionDenied(t, p, b, executor, query, false)
					}
				}
				if !allowed {
					subscriptionDenied(t, p, b, actor, query, true)
					subscriptionDenied(t, p, b, actor, "SELECT subname FROM pg_subscription WHERE subconninfo='host=publisher.invalid password=actual_secret'", true)
					subscriptionDenied(t, p, b, actor, "SELECT pg_subscription FROM pg_subscription WHERE subname='sub_secret'", false)
				}
				subscriptionRun(t, p, b, actor, fixture.Select("SELECT subname,subpublications FROM pg_subscription WHERE subname='sub_secret'", []string{"subname", "subpublications"}, []string{"NAME", "_TEXT"}, [][]any{{"sub_secret", "{remote_secret}"}}))
			}
			cycle := func() {
				check(false) // Object ownership does not grant catalog conninfo access.
				subscriptionExec(t, p, b, admin, "GRANT SELECT(subconninfo) ON pg_catalog.pg_subscription TO sub_secret_owner", false)
				check(true)
				subscriptionExec(t, p, b, admin, "REVOKE SELECT(subconninfo) ON pg_catalog.pg_subscription FROM sub_secret_owner", false)
				check(false)
				subscriptionExec(t, p, b, admin, "GRANT SELECT(subconninfo) ON pg_catalog.pg_subscription TO sub_secret_delegate", false)
				subscriptionExec(t, p, b, admin, "GRANT sub_secret_delegate TO sub_secret_owner", false)
				check(true)
				subscriptionExec(t, p, b, admin, "REVOKE sub_secret_delegate FROM sub_secret_owner", false)
				check(false)
				subscriptionExec(t, p, b, admin, "REVOKE SELECT(subconninfo) ON pg_catalog.pg_subscription FROM sub_secret_delegate", false)
			}
			cycle()
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				subscriptionDenied(t, p, b, held, query, false)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				cycle() // Reuse the exact statement and actor after generation replacement.
			}
			stmt.Close()
			subscriptionExec(t, p, b, actor, "RESET ROLE", false)
			subscriptionExec(t, p, b, admin, "DROP SUBSCRIPTION sub_secret", false)
		})
	}
}

// RestoreStage captures owner incarnations before the reader is entered.
// Use that real I/O barrier, bounded joins and ordinary SQL mutations.
func subscriptionRestoreDuringRead(t *testing.T, p *fixture.Pair, dump []byte, mutate func(context.CancelFunc)) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
	defer cancel()
	reader := &fixture.SequenceGatedReader{Reader: bytes.NewReader(dump), Started: make(chan struct{}), Release: make(chan struct{})}
	done := make(chan error, 1)
	joined, released := false, false
	go func() { done <- stuntgres.Restore(ctx, p.Native, reader) }()
	defer func() {
		cancel()
		if !released {
			close(reader.Release)
		}
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("gated subscription Restore did not join")
			}
		}
	}()
	select {
	case <-reader.Started:
	case err := <-done:
		joined = true
		t.Fatal("Restore ended before reader admission", err)
	case <-ctx.Done():
		t.Fatal("Restore reader admission", ctx.Err())
	}
	mutate(cancel)
	close(reader.Release)
	released = true
	select {
	case err := <-done:
		joined = true
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Restore publication did not join")
		return nil
	}
}

func TestReferenceSubscriptionRestoreFences(t *testing.T) {
	for _, change := range []string{"rename_owner", "recreate_owner", "cancel"} {
		t.Run(change, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"sub_fence_owner", "sub_fence_renamed"})
			p := subscriptionScenario(t, "sub_fence")
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_fence_owner", false)
					subscriptionExec(t, p, b, b.DB, "CREATE TABLE sub_fence_sentinel(v int)", false)
					subscriptionRun(t, p, b, b.DB, fixture.Exec("INSERT INTO sub_fence_sentinel VALUES (77)", 1))
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_fence CONNECTION 'password=fence_secret' PUBLICATION original WITH (connect=false,slot_name=NONE,binary=true)", false)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_fence OWNER TO sub_fence_owner", false)
					subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary,subconninfo,subpublications FROM pg_subscription WHERE subname='sub_fence'", []string{"subbinary", "subconninfo", "subpublications"}, []string{"BOOL", "TEXT", "_TEXT"}, [][]any{{true, "password=fence_secret", "{original}"}}))
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						err := subscriptionRestoreDuringRead(t, p, dump.Bytes(), func(cancel context.CancelFunc) {
							switch change {
							case "rename_owner":
								subscriptionExec(t, p, b, b.DB, "ALTER ROLE sub_fence_owner RENAME TO sub_fence_renamed", false)
							case "recreate_owner":
								subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_fence", false)
								subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_fence_owner", false)
								subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_fence_owner", false)
							case "cancel":
								cancel()
							}
						})
						if change == "cancel" {
							if !errors.Is(err, context.Canceled) {
								t.Fatal("gated Restore cancellation", err)
							}
						} else {
							message := `stuntgres: replacement principal "sub_fence_owner" does not exist`
							if change == "recreate_owner" {
								message = `stuntgres: replacement principal "sub_fence_owner" changed`
							}
							if err == nil || err.Error() != message {
								t.Fatal("Restore adopted stale owner", err)
							}
						}
						if change == "recreate_owner" {
							subscriptionRun(t, p, b, b.DB, subscriptionEmpty(false, "SELECT * FROM pg_subscription WHERE subname='sub_fence'", false))
						} else {
							subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary,subconninfo,subpublications FROM pg_subscription WHERE subname='sub_fence'", []string{"subbinary", "subconninfo", "subpublications"}, []string{"BOOL", "TEXT", "_TEXT"}, [][]any{{true, "password=fence_secret", "{original}"}}))
						}
						subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT v FROM sub_fence_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
					}
				})
			}
		})
	}
}
