package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

func analyzeProgressLogin(t *testing.T, e *core.Engine, name string) *session.Session {
	t.Helper()
	roles := e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, roles.LookupRef(name))
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

func TestAnalyzeProgressVisibility(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE analyze_auth LOGIN", "CREATE ROLE analyze_effective LOGIN",
		"CREATE ROLE analyze_member LOGIN", "CREATE ROLE analyze_other LOGIN",
		"CREATE ROLE analyze_stats LOGIN", "CREATE ROLE analyze_monitor LOGIN",
		"GRANT analyze_effective TO analyze_auth", "GRANT analyze_auth TO analyze_member",
		"GRANT pg_read_all_stats TO analyze_stats", "GRANT pg_monitor TO analyze_monitor",
		"GRANT CREATE ON SCHEMA public TO analyze_effective",
	} {
		enginetest.SessionSQL(t, admin, q)
	}
	worker := analyzeProgressLogin(t, e, "analyze_auth")
	enginetest.SessionSQL(t, worker, "SET ROLE analyze_effective")
	enginetest.SessionSQL(t, worker, "CREATE TABLE analyze_visibility(k INT)")
	enginetest.SessionSQL(t, admin, "GRANT SELECT, MAINTAIN ON analyze_visibility TO analyze_other")
	relation := enginetest.CatalogRef(t, e, e.CatalogRead(), "analyze_visibility").Address
	scope, _ := analyzeProgressScope(t, e, worker)
	p := analyzeProgressBegin(e, scope, relation)
	defer p.End()
	capture := func(s *session.Session) pgcatalog.AnalyzeProgressFact {
		t.Helper()
		op := enginetest.TransactionOperation(t, s)
		defer op.Done()
		return analyzeProgressOne(t, e, op)
	}
	for _, tc := range []struct {
		name    string
		visible bool
	}{
		{"analyze_auth", true}, {"analyze_member", true}, {"analyze_other", false},
		{"analyze_effective", false}, {"analyze_stats", true}, {"analyze_monitor", true},
	} {
		f := capture(analyzeProgressLogin(t, e, tc.name))
		if f.Visible != tc.visible || f.PID != int64(worker.Token().ID()) || f.Relation != relation.ObjectID || !f.DatabaseExists || f.Database != e.Catalog.DatabaseAddress().ObjectID {
			t.Fatalf("%s: %+v", tc.name, f)
		}
	}
	if f := capture(admin); !f.Visible {
		t.Fatal(f)
	}
	member := analyzeProgressLogin(t, e, "analyze_member")
	saved := capture(member)
	enginetest.SessionSQL(t, admin, "REVOKE analyze_auth FROM analyze_member")
	if f := capture(member); f.Visible || !saved.Visible {
		t.Fatal("stale observer closure or borrowed authority", f, saved)
	}
	enginetest.SessionSQL(t, admin, "GRANT analyze_auth TO analyze_member")
	if f := capture(member); !f.Visible {
		t.Fatal("fresh inherited grant missing", f)
	}
	enginetest.SessionSQL(t, member, "SET ROLE analyze_effective")
	if f := capture(member); f.Visible {
		t.Fatal("worker effective principal transferred ownership", f)
	}
	enginetest.SessionSQL(t, member, "RESET ROLE")
	if f := capture(member); !f.Visible {
		t.Fatal("RESET ROLE not recaptured", f)
	}
	// Recreating an identically named role must not authenticate an old owner.
	for _, q := range []string{"CREATE ROLE analyze_old", "CREATE ROLE analyze_inheritor", "GRANT analyze_old TO analyze_inheritor"} {
		enginetest.SessionSQL(t, admin, q)
	}
	roles := e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	old := roles.LookupRef("analyze_old")
	for _, q := range []string{"DROP ROLE analyze_old", "CREATE ROLE analyze_old", "GRANT analyze_old TO analyze_inheritor"} {
		enginetest.SessionSQL(t, admin, q)
	}
	roles = e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	viewer, err := pgcatalog.NewProgressViewer(context.Background(), roles, roles.LookupRef("analyze_inheritor"))
	if err != nil || viewer.Visible(old) || !viewer.Visible(roles.LookupRef("analyze_old")) {
		t.Fatal("recreated name authenticated old owner", err)
	}
}
