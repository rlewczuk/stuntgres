package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionPreparedAdmission(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
			run(fixture.Exec("CREATE TABLE expr_t(a INT,b INT)", 0))
			const q = "CREATE STATISTICS expr_prepared ON (a+1) FROM expr_t"
			statement, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='expr_prepared'", []string{"stxname"}, []string{"NAME"}, nil))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: statement, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			run(expressionDefinition("expr_prepared", "", "{e}"))
		})
	}
}

func TestReferenceStatisticsExpressionConditionalNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			defer conn.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, step) }
			for _, q := range []string{"CREATE TABLE expr_t(a INT,b INT)", "CREATE STATISTICS expr_single ON (a+1) FROM expr_t"} {
				run(fixture.Exec(q, 0))
			}
			oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'")
			for _, prepared := range []bool{false, true} {
				notices = nil
				wantError := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
				wantError.Position = "49"
				failed := fixture.ErrorDiagnostic("CREATE STATISTICS IF NOT EXISTS expr_single ON (missing+1) FROM expr_t", wantError)
				if prepared {
					failed = fixture.Prepared(failed)
				}
				run(failed)
				if len(notices) != 0 {
					t.Fatalf("failed binding emitted duplicate notice: %#v", notices)
				}
				step := fixture.Exec("CREATE STATISTICS IF NOT EXISTS expr_single ON (a+2) FROM expr_t", 0)
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `statistics object "expr_single" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("conditional diagnostics got %#v want %#v", notices, want)
				}
				if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'"); got != oid {
					t.Fatal("conditional create replaced identity")
				}
				run(expressionDefinition("expr_single", "", "{e}"))
			}
		})
	}
}
