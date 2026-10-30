package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipMembership(t *testing.T) {
	t.Run("upgrade_preserves_nonrecipe_membership", ownershipUpdateMembership)
	t.Run("ordinary_overloaded_function", func(t *testing.T) {
		fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := fixture.OwnershipRun(t, p, b, c)
			ownershipSetup(run)
			for _, q := range []string{`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, `CREATE FUNCTION eo_old.member(IN x integer,OUT result integer) LANGUAGE SQL AS 'SELECT x+1'`, `CREATE FUNCTION eo_old.member(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'`} {
				run(fixture.Exec(q, 0))
			}
			q := `SELECT oid::bigint,proowner::bigint,proargtypes::text,prosrc FROM pg_proc WHERE oid='eo_old.member(integer)'::regprocedure`
			before := fixture.OwnershipSnapshot(t, p, c, q)
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_old.member(IN x integer,OUT result integer)`, 0))
			ownershipMemberCount(run, 7)
			fixture.OwnershipUnchanged(t, p, c, q, before)
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_old.member(integer)'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND deptype='e'`, 1))
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_old.member(text)'::regprocedure AND deptype='e'`, 0))
			for _, ext := range []string{"unaccent", `"uuid-ossp"`} {
				run(fixture.OwnershipError(`ALTER EXTENSION `+ext+` ADD FUNCTION eo_old.member(integer)`, "55000", `function eo_old.member(integer) is already a member of extension "unaccent"`))
			}
			for _, cascade := range []string{"", " CASCADE"} {
				s := fixture.OwnershipError(`DROP FUNCTION eo_old.member(integer)`+cascade, "2BP01", `cannot drop function eo_old.member(integer) because extension unaccent requires it`)
				s.Diagnostic.Hint = "You can drop extension unaccent instead."
				run(s)
			}
			run(fixture.OwnershipError(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.member(integer)`, "55000", `function eo_old.member(integer) is not a member of extension "uuid-ossp"`))
			run(fixture.Prepared(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_old.member(integer)`, 0)))
			fixture.OwnershipUnchanged(t, p, c, q, before)
			ownershipMemberCount(run, 6)
			run(fixture.Select(`SELECT eo_old.member(4) AS answer,eo_old.member('text'::text) AS note`, []string{"answer", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), "text"}}))
			run(fixture.OwnershipError(`ALTER EXTENSION unaccent DROP FUNCTION eo_old.member(integer)`, "55000", `function eo_old.member(integer) is not a member of extension "unaccent"`))
			run(fixture.Exec(`DROP EXTENSION unaccent`, 0))
			run(fixture.Select(`SELECT eo_old.member(4) AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
		})
	})
	t.Run("provider_origin_survives_membership_and_installation", func(t *testing.T) {
		fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := fixture.OwnershipRun(t, p, b, c)
			ownershipSetup(run)
			run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
			before := fixture.OwnershipSnapshot(t, p, c, ownershipProcedures)
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_old.unaccent(text)`, 0))
			ownershipCallable(run, "eo_old")
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION eo_old.unaccent(text)`, 0))
			ownershipCallable(run, "eo_old")
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.unaccent(text)`, 0))
			// Preserve actual dictionary/template/helper dependencies explicitly.
			for _, target := range []string{"FUNCTION eo_old.unaccent(regdictionary,text)", "FUNCTION eo_old.unaccent_init(internal)", "FUNCTION eo_old.unaccent_lexize(internal,internal,internal,internal)", "TEXT SEARCH DICTIONARY eo_old.unaccent", "TEXT SEARCH TEMPLATE eo_old.unaccent"} {
				run(fixture.Exec("ALTER EXTENSION unaccent DROP "+target, 0))
			}
			ownershipMemberCount(run, 0)
			run(fixture.Exec("DROP EXTENSION unaccent", 0))
			fixture.OwnershipUnchanged(t, p, c, ownershipProcedures, before)
			ownershipCallable(run, "eo_old")
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent'`, 0))
			// Names and current membership are not provider execution symbols.
			run(fixture.Exec(`ALTER FUNCTION eo_old.unaccent(text) RENAME TO detached_translate`, 0))
			run(fixture.Select(`SELECT eo_old.detached_translate('Hôtel') AS translated,eo_old.detached_translate(NULL::text) AS absent`, []string{"translated", "absent"}, []string{"TEXT", "TEXT"}, [][]any{{"Hotel", nil}}))
		})
	})
}
