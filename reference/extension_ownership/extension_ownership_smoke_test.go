package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipFeatureSmoke(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
		ownershipCallable(run, "eo_new")
		ownershipMemberCount(run, 6)
		run(fixture.Exec(`CREATE FUNCTION eo_new.member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_new.member(integer)`, 0))
		run(ownershipError(`ALTER EXTENSION unaccent ADD FUNCTION eo_new.member(integer)`, "55000", `function eo_new.member(integer) is already a member of extension "unaccent"`))
		s := ownershipError(`DROP FUNCTION eo_new.member(integer)`, "2BP01", `cannot drop function eo_new.member(integer) because extension unaccent requires it`)
		s.Diagnostic.Hint = "You can drop extension unaccent instead."
		run(s)
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_new.member(integer)`, 0))
		run(fixture.Select(`SELECT eo_new.member(4) AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
		run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_new.member(integer)'::regprocedure AND deptype='e'`, 0))
		run(fixture.Exec(`ALTER EXTENSION plpgsql SET SCHEMA pg_catalog`, 0))
		run(ownershipError(`ALTER EXTENSION plpgsql SET SCHEMA eo_new`, "0A000", `extension "plpgsql" does not support SET SCHEMA`))
		defer ownershipStockCleanup(t, c)()
		run(fixture.Exec(`ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()`, 0))
		s = ownershipError(`DROP FUNCTION pg_catalog.plpgsql_call_handler()`, "2BP01", `cannot drop function plpgsql_call_handler() because other objects depend on it`)
		s.Diagnostic.Detail = "extension plpgsql depends on function plpgsql_call_handler()"
		s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(s)
		run(fixture.Exec(`ALTER EXTENSION plpgsql ADD FUNCTION pg_catalog.plpgsql_call_handler()`, 0))
		// The detached wrapper keeps its actual live dictionary/template/helpers.
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_new.unaccent(text)`, 0))
		run(fixture.Exec(`ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`, 0))
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			ownershipCallable(r, "eo_new")
			ownershipMemberCount(r, 5)
			r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_language'::regclass AND objid=(SELECT oid FROM pg_language WHERE lanname='plpgsql') AND deptype='e'`, 0))
			r(fixture.Select(`SELECT eo_new.member(4) AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
			r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND extnamespace='eo_new'::regnamespace`, 1))
			r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_extension'::regclass AND objid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
			blocked := ownershipError(`DROP FUNCTION eo_new.unaccent_init(internal)`, "2BP01", `cannot drop function eo_new.unaccent_init(internal) because extension unaccent requires it`)
			blocked.Diagnostic.Hint = "You can drop extension unaccent instead."
			r(blocked)
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		check(c, b.Recorder)
		connector, err := stuntgres.NewDriver().OpenConnector("eo_smoke_remap")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := sql.OpenDB(connector)
		defer other.Close()
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		target, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		check(target, recorder)
	})
}
