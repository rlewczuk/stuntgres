package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"eo_diag_owner"})
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		ownershipSetup(run)
		for _, tc := range []struct{ q, code, message string }{
			{`ALTER EXTENSION missing_ext SET SCHEMA eo_new`, "42704", `extension "missing_ext" does not exist`},
			{`ALTER EXTENSION unaccent SET SCHEMA missing_schema`, "3F000", `schema "missing_schema" does not exist`},
			{`ALTER EXTENSION missing_ext ADD FUNCTION missing_function(integer)`, "42704", `extension "missing_ext" does not exist`},
			{`ALTER EXTENSION unaccent ADD SCHEMA eo_old`, "55000", `cannot add schema "eo_old" to extension "unaccent" because the schema contains the extension`},
			{`ALTER EXTENSION unaccent ADD EXTENSION plpgsql`, "42P17", `cannot add an object of this type to an extension`},
			{`ALTER EXTENSION unaccent DROP EXTENSION plpgsql`, "42P17", `cannot add an object of this type to an extension`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.OwnershipError(tc.q, tc.code, tc.message)
				s.Prepared = prepared
				run(s)
			}
		}
		// Invalid classes are rejected before even resolving the extension.
		for _, target := range []string{"DATABASE eo_invalid", "INDEX eo_invalid", "PUBLICATION eo_invalid", "ROLE eo_invalid", "STATISTICS eo_invalid", "SUBSCRIPTION eo_invalid", "TABLESPACE eo_invalid"} {
			for _, action := range []string{"ADD", "DROP"} {
				run(fixture.OwnershipError("ALTER EXTENSION missing_ext "+action+" "+target, "42P17", "cannot add an object of this type to an extension"))
			}
		}
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD SCHEMA eo_new`, 0))
		run(fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "55000", `cannot move extension "unaccent" into schema "eo_new" because the extension contains the schema`))
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP SCHEMA eo_new`, 0))
		run(fixture.Exec(`CREATE FUNCTION eo_other.foreign_member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_other.foreign_member(integer)`, 0))
		s := fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "0A000", `extension "unaccent" does not support SET SCHEMA`)
		s.Diagnostic.Detail = `function eo_other.foreign_member(integer) is not in the extension's schema "eo_old"`
		run(s)
		ownershipCallable(run, "eo_old")
		ownershipMemberCount(run, 7)
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_old'::regnamespace`, 1))
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_other.foreign_member(integer)`, 0))
		// Bare signatures and wrong-kind use the existing routine identity rules.
		for _, q := range []string{`CREATE FUNCTION eo_old.overloaded(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`, `CREATE FUNCTION eo_old.overloaded(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'`, `CREATE PROCEDURE eo_old.proc() LANGUAGE SQL AS 'SELECT 1'`} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ q, code, message, hint string }{
			{`ALTER EXTENSION unaccent ADD FUNCTION eo_old.absent(integer)`, "42883", `function eo_old.absent(integer) does not exist`, ""},
			{`ALTER EXTENSION unaccent ADD FUNCTION eo_old.overloaded`, "42725", `function name "eo_old.overloaded" is not unique`, "Specify the argument list to select the function unambiguously."},
			{`ALTER EXTENSION unaccent ADD FUNCTION eo_old.proc()`, "42809", `eo_old.proc() is not a function`, ""},
		} {
			s := fixture.OwnershipError(tc.q, tc.code, tc.message)
			s.Diagnostic.Hint = tc.hint
			run(s)
		}
		// getObjectDescription uses raw extname even when it is not an SQL
		// identifier. Explicit membership diagnostics still quote that name.
		for _, q := range []string{
			`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`,
			`SET search_path TO eo_old,public`,
			`CREATE ROLE eo_diag_owner`,
			`CREATE TABLE eo_old."EO member" (n integer)`,
			`ALTER TABLE eo_old."EO member" OWNER TO eo_diag_owner`,
			`ALTER EXTENSION "uuid-ossp" ADD TABLE eo_old."EO member"`,
			`CREATE FUNCTION eo_old.eo_diag() RETURNS integer LANGUAGE SQL AS 'SELECT 1'`,
			`ALTER EXTENSION "uuid-ossp" ADD FUNCTION eo_old.eo_diag()`,
			`CREATE DOMAIN eo_old.eo_diag_domain AS integer`,
			`ALTER EXTENSION "uuid-ossp" ADD DOMAIN eo_old.eo_diag_domain`,
			`CREATE CAST (integer AS uuid) WITH INOUT`,
			`ALTER EXTENSION "uuid-ossp" ADD CAST (integer AS uuid)`,
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ q, code, message, hint string }{
			{`DROP TABLE eo_old."EO member"`, "2BP01", `cannot drop table "EO member" because extension uuid-ossp requires it`, "You can drop extension uuid-ossp instead."},
			{`DROP TABLE eo_old."EO member" CASCADE`, "2BP01", `cannot drop table "EO member" because extension uuid-ossp requires it`, "You can drop extension uuid-ossp instead."},
			{`DROP FUNCTION eo_old.eo_diag()`, "2BP01", "cannot drop function eo_diag() because extension uuid-ossp requires it", "You can drop extension uuid-ossp instead."},
			{`DROP DOMAIN eo_old.eo_diag_domain`, "2BP01", "cannot drop type eo_diag_domain because extension uuid-ossp requires it", "You can drop extension uuid-ossp instead."},
			{`DROP CAST (integer AS uuid)`, "2BP01", "cannot drop cast from integer to uuid because extension uuid-ossp requires it", "You can drop extension uuid-ossp instead."},
			{`DROP OWNED BY eo_diag_owner`, "2BP01", `cannot drop table "EO member" because extension uuid-ossp requires it`, "You can drop extension uuid-ossp instead."},
			{`ALTER EXTENSION "uuid-ossp" ADD TABLE eo_old."EO member"`, "55000", `table "EO member" is already a member of extension "uuid-ossp"`, ""},
		} {
			s := fixture.OwnershipError(tc.q, tc.code, tc.message)
			s.Diagnostic.Hint = tc.hint
			run(s)
		}
	})
}
