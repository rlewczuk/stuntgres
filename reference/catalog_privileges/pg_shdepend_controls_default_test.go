package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShdependControlsDefaultACL(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, _, owner, reader, _ := shSetup(t, p, b)
		var pristine bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &pristine); err != nil {
				t.Fatal(err)
			}
		}
		shExec(t, p, b, b.DB, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner GRANT SELECT ON TABLES TO sh_reader")
		defer b.DB.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner REVOKE SELECT ON TABLES FROM sh_reader")
		id := shOID(t, p, b.DB, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0 AND defaclobjtype='r'", owner)
		expected := [][]any{shEdge(db, 826, id, 0, owner, "o"), shEdge(db, 826, id, 0, reader, "a")}
		check := func() {
			t.Helper()
			shDatabaseBag(t, p, b, b.DB, db, 826, id, expected...)
		}
		check()
		if !b.PG {
			// Existing controls deliberately reject default-ACL serialization.
			// The catalog must preserve exact dependencies at that boundary.
			var writer pgParameterACLWriter
			err := stuntgres.Dump(p.Ctx, p.Native, &writer)
			var state interface{ SQLState() string }
			if err == nil || err.Error() != "stuntgres: dump cannot serialize default privileges" || writer.calls != 0 || errors.As(err, &state) {
				t.Fatal("ordinary prewriter default-ACL rejection", err, writer.calls)
			}
			check()
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE sh_default_replacement(a INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC"))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"})
			check()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(pristine.String())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=826 AND objid=$2", db, id)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=826", [][]any{}, db))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pristine.String())); err != nil {
				t.Fatal(err)
			}
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=826", [][]any{}, db))
			shRetained(t, old, expected)
		}
		if b.PG {
			shExec(t, p, b, b.DB, "ALTER DEFAULT PRIVILEGES FOR ROLE sh_owner REVOKE SELECT ON TABLES FROM sh_reader")
			shDatabaseBag(t, p, b, b.DB, db, 826, id)
		}
	}
}
