package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterNotNullPrepared(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.d AS integer",
			"CREATE DOMAIN domain_s.child AS domain_s.d",
			"CREATE TABLE domain_s.t(id integer,v domain_s.child)",
			"CREATE VIEW domain_s.converted AS SELECT NULL::domain_s.child AS v FROM public.one",
			"CREATE VIEW domain_s.parent_converted AS SELECT NULL::domain_s.d AS v FROM public.one",
			"PREPARE domain_nn_cast(domain_s.child) AS SELECT $1 AS v",
			"PREPARE domain_nn_insert(integer,domain_s.child) AS INSERT INTO domain_s.t VALUES($1,$2)",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_nn_cast")
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_nn_insert")
		cast := domainAlterHold(t, p, c, "SELECT $1::domain_s.child AS v FROM public.one")
		insert := domainAlterHold(t, p, c, "INSERT INTO domain_s.t VALUES($1,$2)")
		view := domainAlterHold(t, p, c, "SELECT v FROM domain_s.converted")
		parentView := domainAlterHold(t, p, c, "SELECT v FROM domain_s.parent_converted")
		data := domainAlterHold(t, p, c, "SELECT id,v FROM domain_s.t ORDER BY id")
		domainID := domainAlterOID(t, p, c, "SELECT 'domain_s.child'::regtype::oid")
		arrayID := domainAlterOID(t, p, c, "SELECT 'domain_s.child[]'::regtype::oid")
		checkFreshView := func(q string, enforced bool) {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				s.Prepared = prepared
				if enforced {
					s = domainDefaultFailure(q, domainNotNullDiagnostic("domain_s", "child"), prepared, true)
				}
				t.Run(fmt.Sprintf("fresh_view_%t_%t_%s", enforced, prepared, q), func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
			}
		}
		check := func(enforced bool, id int) {
			for _, value := range []any{nil, int64(2)} {
				s := fixture.Select(cast.SQL, []string{"v"}, []string{"INT4"}, [][]any{{value}}, value)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, true, s.Types) }
				if enforced && value == nil {
					s = domainDefaultFailure(cast.SQL, domainNotNullDiagnostic("domain_s", "child"), false, true, value)
					s.Diagnostic.Context = "unnamed portal parameter $1"
				}
				t.Run(fmt.Sprintf("cast_%t_%v", enforced, value), func(t *testing.T) { fixture.RunStep(t, p.Ctx, cast, r, s) })
			}
			if enforced {
				s := domainDefaultFailure(insert.SQL, domainNotNullDiagnostic("domain_s", "child"), false, false, id, nil)
				s.Diagnostic.Context = "unnamed portal parameter $2"
				t.Run("inferred_nil", func(t *testing.T) { fixture.RunStep(t, p.Ctx, insert, r, s) })
				for _, q := range []string{"EXECUTE domain_nn_cast(NULL)", fmt.Sprintf("EXECUTE domain_nn_insert(%d,NULL)", id)} {
					t.Run(q, func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, domainDefaultFailure(q, domainNotNullDiagnostic("domain_s", "child"), false, q == "EXECUTE domain_nn_cast(NULL)"))
					})
				}
				// PostgreSQL's cached child coercion was eliminated with a plan
				// dependency on child, not parent. Native has no executable-plan
				// cache and enforces current ancestry. Neither case changes the
				// separate outer-join/scalar-subquery domain-NULL bypass contract.
				t.Run("held_view", func(t *testing.T) {
					s := domainDefaultFailure(view.SQL, domainNotNullDiagnostic("domain_s", "child"), false, true)
					if b.PG {
						s = fixture.Select(view.SQL, []string{"v"}, []string{"INT4"}, [][]any{{nil}})
					}
					fixture.RunStep(t, p.Ctx, view, r, s)
				})
			} else {
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, id, nil))
				run(fixture.Select("EXECUTE domain_nn_cast(NULL)", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
				run(fixture.Exec(fmt.Sprintf("EXECUTE domain_nn_insert(%d,NULL)", id+1), 1))
				fixture.RunStep(t, p.Ctx, view, r, fixture.Select(view.SQL, []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			}
			fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, id+2, 2))
			run(fixture.Select("EXECUTE domain_nn_cast(2)", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		}
		empty := fixture.Select(data.SQL, []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
		empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, empty.Types) }
		fixture.RunStep(t, p.Ctx, data, r, empty)
		check(false, 10)
		checkFreshView(view.SQL, false)
		fixture.RunStep(t, p.Ctx, parentView, r, fixture.Select(parentView.SQL, []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		run(fixture.Exec("UPDATE domain_s.t SET v=2 WHERE v IS NULL", 2))
		run(fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0))
		// Unlike the child-domain constant, this held plan references the
		// exact altered TYPEOID. Pinned18.6 tests-2 observes rechecking here;
		// native's current-descriptor execution independently requires it too.
		t.Run("held_parent_view_after_set", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, parentView, r, domainDefaultFailure(parentView.SQL, domainNotNullDiagnostic("domain_s", "d"), false, true))
		})
		run(fixture.Exec("CREATE VIEW domain_s.late_converted AS SELECT NULL::domain_s.child AS v FROM public.one", 0))
		// Fresh planning and late-created stored definitions are separate
		// oracle witnesses, not substitutes for the retained held route.
		checkFreshView(view.SQL, true)
		checkFreshView("SELECT v FROM domain_s.late_converted", true)
		check(true, 20)
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP NOT NULL", 0))
		check(false, 30)
		fixture.RunStep(t, p.Ctx, parentView, r, fixture.Select(parentView.SQL, []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		checkFreshView(view.SQL, false)
		checkFreshView("SELECT v FROM domain_s.late_converted", false)
		empty.Rows = [][]any{{int64(10), int64(2)}, {int64(11), int64(2)}, {int64(12), int64(2)}, {int64(22), int64(2)}, {int64(30), nil}, {int64(31), nil}, {int64(32), int64(2)}}
		fixture.RunStep(t, p.Ctx, data, r, empty)
		if domainAlterOID(t, p, c, "SELECT 'domain_s.child'::regtype::oid") != domainID || domainAlterOID(t, p, c, "SELECT 'domain_s.child[]'::regtype::oid") != arrayID {
			t.Fatal("ALTER changed derived logical identities")
		}
	})
}
