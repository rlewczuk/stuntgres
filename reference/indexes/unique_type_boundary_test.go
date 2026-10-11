package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// UNIQUE is no longer an excluded TYPE dependency. The former reference-only
// capability witness now exercises supported behavior against both backends.
func TestReferenceUniqueAlterTypeDependentBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(c INT UNIQUE)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
					constraintQuery := "SELECT c.oid FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname='t' AND r.relnamespace=$1 AND c.conname='t_c_key'"
					backingQuery := "SELECT oid FROM pg_class WHERE relname='t_c_key' AND relnamespace=$1"
					var constraintID, backingID int64
					if err := b.DB.QueryRowContext(p.Ctx, constraintQuery, namespace).Scan(&constraintID); err != nil || constraintID <= 0 {
						t.Fatal(constraintID, err)
					}
					if err := b.DB.QueryRowContext(p.Ctx, backingQuery, namespace).Scan(&backingID); err != nil || backingID <= 0 {
						t.Fatal(backingID, err)
					}
					want := fixture.UniqueReferenceDuplicate("t", "t_c_key", "Key (c)=(0) is duplicated.")
					want.Message = `could not create unique index "t_c_key"`
					failure := fixture.ErrorDiagnostic("ALTER TABLE t ALTER c TYPE INT USING 0", want)
					if prepared {
						failure = fixture.Prepared(failure)
					}
					run(failure)
					var retainedConstraint, retainedBacking int64
					if err := b.DB.QueryRowContext(p.Ctx, constraintQuery, namespace).Scan(&retainedConstraint); err != nil || retainedConstraint != constraintID {
						t.Fatal("failed TYPE changed constraint identity", constraintID, retainedConstraint, err)
					}
					if err := b.DB.QueryRowContext(p.Ctx, backingQuery, namespace).Scan(&retainedBacking); err != nil || retainedBacking != backingID {
						t.Fatal("failed TYPE changed backing identity", backingID, retainedBacking, err)
					}
					// Additional catalog validation fields remain PostgreSQL-only.
					if b.PG {
						run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, constraintID))
						run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='t_c_key'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
					}
					run(fixture.Select("SELECT pg_get_indexdef('t_c_key'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX t_c_key ON public.t USING btree (c)"}}))
					run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE oid=$1", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (c)"}}, constraintID))
					duplicate := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_c_key", "Key (c)=(1) already exists."))
					if prepared {
						duplicate = fixture.Prepared(duplicate)
					}
					run(duplicate)
					run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
				})
			}
		})
	}
}
