package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func inspectAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, role string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE USER "+role, 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER TABLE r ADD COLUMN allowed integer", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT SELECT(allowed) ON r TO "+role, 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SCHEMA inspect_private", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_private.secret(v int4range)", 0))
	defer func() {
		if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA inspect_private CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	if b.PG {
		if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE "+role+" PASSWORD 'role-core-fixture'"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.DB.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO "+role); err != nil {
			t.Fatal(err)
		}
	}
	actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, "reference/"+t.Name(), role)
	if err := actor.PingContext(p.Ctx); err != nil {
		t.Fatal(err)
	}
	var previous int64
	for _, data := range []string{"'[1,4]'", "'empty'", "NULL", ""} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM r", previous))
		previous = 0
		if data != "" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r(v) VALUES ("+data+")", 1))
			previous = 1
		}
		allowed := fixture.Select("SELECT allowed FROM r", []string{"allowed"}, []string{"INT4"}, [][]any{})
		if data != "" {
			allowed.Rows = [][]any{{nil}}
		}
		allowedRows, err := actor.QueryContext(p.Ctx, allowed.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, b.PG, allowedRows, allowed.Columns, allowed.Types, allowed.Rows, false)
		for _, q := range []string{"SELECT lower(v) AS lo FROM r", "SELECT isempty(v) AS empty FROM r", "SELECT range_merge(v,v) AS hull FROM r"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42501", "permission denied for table r", "", "", "", ""))
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		q := "SELECT lower(v) FROM inspect_private.secret"
		s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42501", "permission denied for schema inspect_private", "", "inspect_private", "", ""))
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT SELECT(v) ON r TO "+role, 0))
	inspectRunActor := inspectTupleStep(b, "v", "INT4", []any{nil, nil, nil, nil, nil, nil, nil})
	inspectRunActor.Query += " FROM r"
	inspectRunActor.Rows = [][]any{}
	actorRows, err := actor.QueryContext(p.Ctx, inspectRunActor.Query)
	if err != nil {
		t.Fatal(err)
	}
	fixture.RangeConsumeHeld(t, actorRows, inspectRunActor)
}
func TestReferenceRangeInspectAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"inspect_reader"})
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v int4range)", 0))
		inspectAuthorization(t, p, b, "inspect_reader")
	})
}
