package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func enumAlterAuthorization(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
	t.Helper()
	for _, q := range []string{"CREATE ROLE alter_owner", "CREATE ROLE alter_member", "GRANT alter_owner TO alter_member WITH INHERIT TRUE", "CREATE SCHEMA alter_owned AUTHORIZATION alter_owner", "CREATE SCHEMA alter_shadow", "CREATE SCHEMA alter_private", "CREATE TYPE alter_private.hidden AS ENUM ('old')", "CREATE TYPE alter_shadow.mood AS ENUM ('shadow')", "SET SESSION AUTHORIZATION alter_owner", "CREATE TYPE alter_owned.mood AS ENUM ('old')", "RESET SESSION AUTHORIZATION"} {
		enumAlterExec(t, f, f.A, q)
	}
	defer func() {
		f.A.ExecContext(f.Pair.Ctx, "RESET SESSION AUTHORIZATION")
		f.A.ExecContext(f.Pair.Ctx, "SET search_path TO public")
		for _, schema := range []string{"alter_owned", "alter_shadow", "alter_private"} {
			if _, err := f.A.ExecContext(f.Pair.Ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	// These ACL setup commands are PostgreSQL's external administration seam;
	// native type/schema ACL mutation remains explicitly outside this part.
	if pg {
		for _, q := range []string{"GRANT USAGE ON SCHEMA alter_owned TO alter_member", "REVOKE USAGE ON TYPE alter_owned.mood FROM PUBLIC", "REVOKE ALL ON TYPE alter_owned.mood FROM alter_owner", "REVOKE CREATE ON SCHEMA alter_owned FROM alter_owner"} {
			if _, err := f.A.ExecContext(f.Pair.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
	enumAlterExec(t, f, f.A, "SET SESSION AUTHORIZATION alter_owner")
	// Owner-only ALTER must not borrow input USAGE or schema CREATE checks.
	enumAlterExec(t, f, f.A, "ALTER TYPE alter_owned.mood ADD VALUE 'owner_added'")
	enumAlterExec(t, f, f.A, "ALTER TYPE alter_owned.mood RENAME VALUE 'old' TO 'owner_renamed'")
	enumAlterExec(t, f, f.A, "RESET SESSION AUTHORIZATION")
	enumAlterExec(t, f, f.A, "SET SESSION AUTHORIZATION alter_member")
	enumAlterExec(t, f, f.A, "ALTER TYPE alter_owned.mood ADD VALUE 'inherited'")
	enumAlterExec(t, f, f.A, "RESET SESSION AUTHORIZATION")
	// Superuser ownership override and qualified/shadowed lookup are real effects.
	enumAlterExec(t, f, f.A, "ALTER TYPE alter_owned.mood ADD VALUE 'super_added'")
	enumAlterExec(t, f, f.A, "SET search_path TO alter_shadow,public")
	enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'selected'")
	enumAlterRows(t, f, f.A, "SELECT 'selected'::alter_shadow.mood AS v", "selected")
	want := enumAlterDiagnostic("22P02", `invalid input value for enum public.mood: "selected"`)
	want.Position = "8"
	enumAlterError(t, f, f.A, "SELECT 'selected'::public.mood", false, true, "execute", want)
	enumAlterExec(t, f, f.A, "SET search_path TO public")
	enumAlterExec(t, f, f.A, "SET SESSION AUTHORIZATION alter_stranger")
	for _, prepared := range []bool{false, true} {
		want := enumAlterDiagnostic("42501", "permission denied for schema alter_private")
		enumAlterError(t, f, f.A, "ALTER TYPE alter_private.hidden ADD VALUE 'denied'", prepared, false, "execute", want)
	}
	enumAlterExec(t, f, f.A, "RESET SESSION AUTHORIZATION")
}
