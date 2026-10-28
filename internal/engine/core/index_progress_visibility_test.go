package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

func indexProgressLogin(t *testing.T, e *core.Engine, name string) *session.Session {
	t.Helper()
	roles := e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: roles.LookupRef(name)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func indexProgressRecreatedRole(t *testing.T) {
	e := engine.New()
	admin := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE ROLE progress_old_owner", "CREATE ROLE progress_inheritor", "GRANT progress_old_owner TO progress_inheritor"} {
		sessionSQL(t, admin, q)
	}
	roles := e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	old := roles.LookupRef("progress_old_owner")
	viewer, err := pgcatalog.NewProgressViewer(context.Background(), roles, roles.LookupRef("progress_inheritor"))
	if err != nil || !viewer.Visible(old) {
		t.Fatal("original inherited principal", err)
	}
	for _, q := range []string{"DROP ROLE progress_old_owner", "CREATE ROLE progress_old_owner", "GRANT progress_old_owner TO progress_inheritor"} {
		sessionSQL(t, admin, q)
	}
	roles = e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	viewer, err = pgcatalog.NewProgressViewer(context.Background(), roles, roles.LookupRef("progress_inheritor"))
	if err != nil || viewer.Visible(old) || !viewer.Visible(roles.LookupRef("progress_old_owner")) {
		t.Fatal("role name reused as authenticated authority", err)
	}
}

func TestCreateIndexProgressVisibility(t *testing.T) {
	t.Run("drop_recreate", indexProgressRecreatedRole)
	e := engine.New()
	admin := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE progress_auth LOGIN",
		"CREATE ROLE progress_effective LOGIN",
		"CREATE ROLE progress_member LOGIN",
		"CREATE ROLE progress_other LOGIN",
		"CREATE ROLE progress_stats LOGIN",
		"CREATE ROLE progress_monitor LOGIN",
		"GRANT progress_effective TO progress_auth",
		"GRANT progress_auth TO progress_member",
		"GRANT pg_read_all_stats TO progress_stats",
		"GRANT pg_monitor TO progress_monitor",
		"GRANT CREATE ON SCHEMA public TO progress_effective",
	} {
		sessionSQL(t, admin, q)
	}
	worker := indexProgressLogin(t, e, "progress_auth")
	sessionSQL(t, worker, "SET ROLE progress_effective")
	sessionSQL(t, worker, "CREATE TABLE progress_build(k INT)")
	sessionSQL(t, worker, "INSERT INTO progress_build VALUES(1),(NULL),(1)")
	// Table SELECT alone is tested during actual work, but its setup need not
	// contend with the admitted builder's catalog-authority predecessors.
	sessionSQL(t, admin, "GRANT SELECT ON progress_build TO progress_other")
	b := indexProgressStage(t, e, worker, "CREATE INDEX progress_new ON progress_build(k)")
	done := b.run()
	joined := false
	defer func() {
		close(b.gate.release)
		if !joined {
			b.op.Cancel(context.Canceled)
			<-done
		}
	}()
	var step indexProgressStep
	select {
	case step = <-b.gate.steps:
	case err := <-done:
		joined = true
		t.Fatal("build ended before real scan", err)
	}
	if step.kind != "scan" {
		t.Fatal(step)
	}
	capture := func(s *session.Session) pgcatalog.CreateIndexProgressFact {
		t.Helper()
		op := transactionOperation(t, s)
		facts := indexProgressFacts(t, e, op)
		op.Done()
		if len(facts) != 1 {
			t.Fatal(facts)
		}
		return facts[0]
	}
	for _, tc := range []struct {
		name    string
		visible bool
	}{
		{"postgres", true}, {"progress_auth", true}, {"progress_member", true},
		{"progress_effective", false}, {"progress_other", false}, {"progress_stats", true}, {"progress_monitor", true},
	} {
		var viewer *session.Session
		if tc.name == "postgres" {
			viewer = admin
		} else {
			viewer = indexProgressLogin(t, e, tc.name)
		}
		f := capture(viewer)
		if f.Visible != tc.visible || f.PID != int64(worker.Token().ID()) || f.Relation != b.parent.ObjectID {
			t.Fatalf("%s: %+v", tc.name, f)
		}
	}
	member := indexProgressLogin(t, e, "progress_member")
	if !capture(member).Visible {
		t.Fatal("inherited authenticated owner not visible")
	}
	sessionSQL(t, admin, "REVOKE progress_auth FROM progress_member")
	if capture(member).Visible {
		t.Fatal("revoked membership cached")
	}
	sessionSQL(t, admin, "GRANT progress_auth TO progress_member")
	if !capture(member).Visible {
		t.Fatal("fresh grant missing")
	}
	sessionSQL(t, member, "SET ROLE progress_effective")
	if capture(member).Visible {
		t.Fatal("effective/table owner incorrectly owns worker progress")
	}
	sessionSQL(t, member, "RESET ROLE")
	if !capture(member).Visible {
		t.Fatal("RESET ROLE not recaptured")
	}
	other := indexProgressLogin(t, e, "progress_other")
	if capture(other).Visible {
		t.Fatal("table SELECT is not statistics privilege")
	}
	b.op.Cancel(context.Canceled)
	close(step.ack)
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("cancelled build succeeded")
	}
	if facts := indexProgressFacts(t, e, b.observer); len(facts) != 0 {
		t.Fatal(facts)
	}
}
