package reference_test

import "testing"

// UNIQUE is no longer an excluded TYPE dependency. The former reference-only
// capability witness now exercises supported behavior against both backends.
func TestReferenceUniqueAlterTypeDependentBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(c INT UNIQUE)", 0))
					run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
					namespace := uniqueReferencePublicNamespace(t, p.ctx, b.db, b.pg)
					constraintQuery := "SELECT c.oid FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname='t' AND r.relnamespace=$1 AND c.conname='t_c_key'"
					backingQuery := "SELECT oid FROM pg_class WHERE relname='t_c_key' AND relnamespace=$1"
					var constraintID, backingID int64
					if err := b.db.QueryRowContext(p.ctx, constraintQuery, namespace).Scan(&constraintID); err != nil || constraintID <= 0 {
						t.Fatal(constraintID, err)
					}
					if err := b.db.QueryRowContext(p.ctx, backingQuery, namespace).Scan(&backingID); err != nil || backingID <= 0 {
						t.Fatal(backingID, err)
					}
					want := uniqueReferenceDuplicate("t", "t_c_key", "Key (c)=(0) is duplicated.")
					want.Message = `could not create unique index "t_c_key"`
					failure := referenceErrorDiagnostic("ALTER TABLE t ALTER c TYPE INT USING 0", want)
					if prepared {
						failure = referencePrepared(failure)
					}
					run(failure)
					var retainedConstraint, retainedBacking int64
					if err := b.db.QueryRowContext(p.ctx, constraintQuery, namespace).Scan(&retainedConstraint); err != nil || retainedConstraint != constraintID {
						t.Fatal("failed TYPE changed constraint identity", constraintID, retainedConstraint, err)
					}
					if err := b.db.QueryRowContext(p.ctx, backingQuery, namespace).Scan(&retainedBacking); err != nil || retainedBacking != backingID {
						t.Fatal("failed TYPE changed backing identity", backingID, retainedBacking, err)
					}
					// Extra catalog validation fields and index deparsing are outside
					// the native surface; preserve these PostgreSQL-only controls.
					if b.pg {
						run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, constraintID))
						run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid='t_c_key'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
						run(referenceSelect("SELECT pg_get_indexdef('t_c_key'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX t_c_key ON public.t USING btree (c)"}}))
					}
					run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE oid=$1", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (c)"}}, constraintID))
					duplicate := referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_c_key", "Key (c)=(1) already exists."))
					if prepared {
						duplicate = referencePrepared(duplicate)
					}
					run(duplicate)
					run(referenceExec("INSERT INTO t VALUES(3)", 1))
				})
			}
		})
	}
}
