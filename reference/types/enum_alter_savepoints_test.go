package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterSavepointProvenance(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		matrix := []struct {
			name                                                    string
			existing, createInner, addInner, releaseBeforeAdd, safe bool
		}{
			{"existing_outer", true, false, false, false, false},
			{"existing_inner", true, false, true, false, false},
			{"outer_create_outer_add", false, false, false, false, true},
			{"outer_create_inner_add", false, false, true, false, false},
			{"inner_create_inner_add", false, true, true, false, false},
			{"inner_create_release_outer_add", false, true, false, true, false},
		}
		for i, tc := range matrix {
			t.Run(tc.name, func(t *testing.T) {
				typ := fmt.Sprintf("alter_provenance_%d", i)
				create := "CREATE TYPE " + typ + " AS ENUM ('old')"
				if tc.existing {
					enumAlterExec(t, f, f.A, create)
				}
				enumAlterExec(t, f, f.A, "BEGIN")
				if tc.createInner {
					enumAlterExec(t, f, f.A, "SAVEPOINT creation")
				}
				if !tc.existing {
					enumAlterExec(t, f, f.A, create)
				}
				// Initial labels remain safe even when CREATE was in a child.
				enumAlterRows(t, f, f.A, "SELECT 'old'::"+typ+" AS v", "old")
				if tc.releaseBeforeAdd {
					enumAlterExec(t, f, f.A, "RELEASE creation")
				}
				if tc.addInner && !tc.createInner {
					enumAlterExec(t, f, f.A, "SAVEPOINT addition")
				}
				enumAlterExec(t, f, f.A, "ALTER TYPE "+typ+" ADD VALUE 'new'")
				for _, afterRelease := range []bool{false, true} {
					if afterRelease && tc.addInner {
						sp := "addition"
						if tc.createInner {
							sp = "creation"
						}
						enumAlterExec(t, f, f.A, "RELEASE "+sp)
					}
					enumAlterExec(t, f, f.A, "SAVEPOINT use_value")
					if tc.safe {
						enumAlterRows(t, f, f.A, "SELECT 'new'::"+typ+" AS v", "new")
					} else {
						want := enumAlterUnsafe("new", typ)
						want.Position = "8"
						enumAlterError(t, f, f.A, "SELECT 'new'::"+typ, false, true, "execute", want)
						enumAlterExec(t, f, f.A, "ROLLBACK TO use_value")
					}
					enumAlterExec(t, f, f.A, "RELEASE use_value")
				}
				enumAlterExec(t, f, f.A, "COMMIT")
				enumAlterRows(t, f, f.A, "SELECT 'new'::"+typ+" AS v", "new")
			})
		}
		enumAlterExec(t, f, f.A, "CREATE TYPE alter_rollback AS ENUM ('old')")
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_rollback ADD VALUE 'outer'")
		enumAlterExec(t, f, f.A, "SAVEPOINT child")
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_rollback ADD VALUE 'inner'")
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_rollback RENAME VALUE 'old' TO 'safe_renamed'")
		enumAlterRows(t, f, f.A, "SELECT 'safe_renamed'::alter_rollback AS v", "safe_renamed")
		enumAlterExec(t, f, f.A, "CREATE TYPE alter_child_created AS ENUM ('initial')")
		enumAlterRows(t, f, f.A, "SELECT 'initial'::alter_child_created AS v", "initial")
		enumAlterExec(t, f, f.A, "ROLLBACK TO child")
		enumAlterRows(t, f, f.A, "SELECT 'old'::alter_rollback AS v", "old")
		enumAlterExec(t, f, f.A, "SAVEPOINT outer_use")
		outerUnsafe := enumAlterUnsafe("outer", "alter_rollback")
		outerUnsafe.Position = "8"
		enumAlterError(t, f, f.A, "SELECT 'outer'::alter_rollback", false, true, "execute", outerUnsafe)
		enumAlterExec(t, f, f.A, "ROLLBACK TO outer_use")
		enumAlterExec(t, f, f.A, "RELEASE outer_use")
		for _, label := range []string{"inner", "safe_renamed"} {
			enumAlterExec(t, f, f.A, "SAVEPOINT absent")
			want := enumAlterDiagnostic("22P02", `invalid input value for enum alter_rollback: "`+label+`"`)
			want.Position = "8"
			enumAlterError(t, f, f.A, "SELECT '"+label+"'::alter_rollback", false, true, "execute", want)
			enumAlterExec(t, f, f.A, "ROLLBACK TO absent")
			enumAlterExec(t, f, f.A, "RELEASE absent")
		}
		enumAlterExec(t, f, f.A, "SAVEPOINT absent_type")
		want := enumAlterDiagnostic("42704", `type "alter_child_created" does not exist`)
		want.Position = "14"
		enumAlterError(t, f, f.A, "SELECT NULL::alter_child_created", false, true, "execute", want)
		enumAlterExec(t, f, f.A, "ROLLBACK TO absent_type")
		enumAlterExec(t, f, f.A, "COMMIT")
		enumAlterRows(t, f, f.A, "SELECT 'outer'::alter_rollback AS v", "outer")
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "CREATE TYPE alter_top_abort AS ENUM ('old')")
		enumAlterExec(t, f, f.A, "ROLLBACK")
		want = enumAlterDiagnostic("42704", `type "alter_top_abort" does not exist`)
		want.Position = "14"
		enumAlterError(t, f, f.A, "SELECT NULL::alter_top_abort", false, true, "execute", want)
	})
}
