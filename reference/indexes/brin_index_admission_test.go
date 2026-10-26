package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func brinIndexDiagnosticAdmission(t *testing.T) {
	t.Helper()
	t.Run("collision-and-notice", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			brinIndexSeed(run)
			run(fixture.Exec("CREATE INDEX bn ON t USING brin(n)", 0))
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX bn ON t USING brin(n int4_minmax_ops(foo=1))", fixture.Diagnostic{Code: "42P07", Message: `relation "bn" already exists`})
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX IF NOT EXISTS bn ON t USING brin(n missing_ops)", fixture.Diagnostic{Code: "42704", Message: `operator class "missing_ops" does not exist for access method "brin"`})
			var notices []fixture.Diagnostic
			db, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("CREATE INDEX IF NOT EXISTS bn ON t USING brin(n int4_minmax_ops(foo=1))", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, db, recorder, s)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "bn" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
				notices = nil
			}
		})
	})
	t.Run("read-only", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			brinIndexSeed(run)
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s := fixture.ImplicitError("CREATE INDEX retry ON t USING brin(n)", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE INDEX retry ON t USING brin(n)", 0))
		})
	})
	t.Run("owner", func(t *testing.T) {
		for _, plain := range []bool{false, true} {
			t.Run(fmt.Sprintf("plain=%v", plain), func(t *testing.T) {
				// Each route owns its cluster-role cleanup before fixture cleanup,
				// so even a failed assertion cannot leak a role into the next route.
				fixture.RoleReferenceCleanup(t, []string{"brin_reader"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) {
							s.NonContext = plain
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						}
						brinIndexSeed(run)
						run(fixture.Exec("CREATE ROLE brin_reader", 0))
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO brin_reader", 0))
						for _, prepared := range []bool{false, true} {
							tx, err := b.DB.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							func() {
								defer tx.Rollback()
								crun := func(s fixture.Step) {
									s.NonContext = plain
									fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
								}
								crun(fixture.Exec("SET ROLE brin_reader", 0))
								s := fixture.ImplicitError("CREATE INDEX retry ON public.t USING brin(n)", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"})
								s.ErrorStage = "execute"
								if prepared {
									s = fixture.Prepared(s)
								}
								crun(s)
							}()
						}
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
						run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM brin_reader", 0))
						run(fixture.Exec("DROP ROLE brin_reader", 0))
					})
				}
			})
		}
	})
	t.Run("syntax", func(t *testing.T) {
		for _, tc := range []struct{ query, position string }{
			{"CREATE INDEX broken ON t USING brin(n) WITH(pages_per_range=)", "61"},
			{"/*é*/ CREATE INDEX broken ON t USING brin(n) WITH(pages_per_range=)", "67"},
		} {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				brinIndexSeed(run)
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: tc.position})
				s.ErrorStage = "execute"
				run(s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				run(s)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='broken'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX broken ON t USING brin(n)", 0))
			})
		}
	})
	t.Run("missing-method-and-table", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			brinIndexSeed(run)
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING missing(n)", fixture.Diagnostic{Code: "42704", Message: `access method "missing" does not exist`})
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON absent_brin USING brin(n)", fixture.Diagnostic{Code: "42P01", Message: `relation "absent_brin" does not exist`})
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE INDEX retry ON t USING brin(n)", 0))
		})
	})
}
