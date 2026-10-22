package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorFamilyPinnedDependencies(t *testing.T) {
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY pg_catalog.integer_ops USING btree DROP FUNCTION 1(integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 1 (integer, integer) of operator family integer_ops for access method btree: btint4cmp(integer,integer) because it is required by the database system`}))
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY pg_catalog.integer_ops USING btree DROP FUNCTION 2(integer)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 2 (integer, integer) of operator family integer_ops for access method btree: btint4sortsupport(internal) because it is required by the database system`}))
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY pg_catalog.integer_ops USING btree DROP OPERATOR 1(smallint,bigint)`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator 1 (smallint, bigint) of operator family integer_ops for access method btree: <(smallint,bigint) because it is required by the database system`}))
		run(fixture.ImplicitError(`DROP OPERATOR FAMILY pg_catalog.integer_ops USING btree`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator family integer_ops for access method btree because it is required by the database system`}))
		// New optional and cross-type rows in a classless family are loose.
		// Never install an unrelated INTERNAL callback into a live system opclass.
		run(fixture.Exec(familyCreate, 0))
		optionalAdded, crossAdded := false, false
		defer func() {
			if optionalAdded {
				if _, err := conn.ExecContext(ctx, `ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 5(integer)`); err != nil {
					t.Error("cleanup optional membership", err)
				}
			}
			if crossAdded {
				if _, err := conn.ExecContext(ctx, `ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer,boolean)`); err != nil {
					t.Error("cleanup cross membership", err)
				}
			}
		}()
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 5 (integer,integer) pg_catalog.btint4sortsupport(internal)`, 0))
		optionalAdded = true
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 5(integer)`, 0))
		optionalAdded = false
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 (integer,boolean) pg_catalog.btint4cmp(integer,integer)`, 0))
		crossAdded = true
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer,boolean)`, 0))
		crossAdded = false
		run(fixture.Select(`SELECT count(*) FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(`SELECT btint4cmp(1,3) AS comparison`, []string{"comparison"}, []string{"INT4"}, [][]any{{int64(-1)}}))
	})
}
