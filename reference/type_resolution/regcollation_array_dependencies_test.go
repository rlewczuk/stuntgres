package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL records scalar regcollation/regnamespace Const dependencies, not
// the object identities embedded in an opaque array Const. Ordinary stored
// array defaults and view constants therefore retain bits without blocking DROP.
func TestReferenceRegCollationArrayDependencies(t *testing.T) {
	regObjectScenario(t, []string{"rc_arraydeps", "rn_arraydeps"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA rc_arraydeps", "CREATE SCHEMA rn_arraydeps",
			`CREATE COLLATION rc_arraydeps.target FROM pg_catalog."C"`)
		var collation, namespace int64
		if err := c.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_catalog.pg_collation WHERE collname='target' AND collnamespace='rc_arraydeps'::regnamespace::oid`).Scan(&collation); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT 'rn_arraydeps'::regnamespace::bigint").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		regObjectExec(t, p, b, c,
			`CREATE TABLE public.rc_arraydeps_values(id integer,c regcollation[] DEFAULT '{rc_arraydeps.target,NULL,0,4294967295}'::regcollation[],n regnamespace[] DEFAULT '{rn_arraydeps,NULL,0,4294967295}'::regnamespace[])`,
			`CREATE VIEW public.rc_arraydeps_view AS SELECT '{rc_arraydeps.target,NULL,0,4294967295}'::regcollation[] AS c,'{rn_arraydeps,NULL,0,4294967295}'::regnamespace[] AS n`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.rc_arraydeps_values(id) VALUES(1)", 1))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT c::text AS c,n::text AS n FROM public.rc_arraydeps_values", []string{"c", "n"}, []string{"TEXT", "TEXT"}, [][]any{{"{rc_arraydeps.target,NULL,-,4294967295}", "{rn_arraydeps,NULL,-,4294967295}"}}))
		regObjectExec(t, p, b, c, "DROP COLLATION rc_arraydeps.target RESTRICT", "DROP SCHEMA rn_arraydeps RESTRICT")
		// Neither default nor view source spelling is reparsed after DROP. Both
		// retain their original opaque OIDs, including when the default runs anew.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.rc_arraydeps_values(id) VALUES(2)", 1))
		collationArray := "{" + strconv.FormatInt(collation, 10) + ",NULL,-,4294967295}"
		namespaceArray := "{" + strconv.FormatInt(namespace, 10) + ",NULL,-,4294967295}"
		regObjectBoth(t, p, b, c, fixture.Select("SELECT id,c::text AS c,n::text AS n FROM public.rc_arraydeps_values ORDER BY id", []string{"id", "c", "n"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), collationArray, namespaceArray}, {int64(2), collationArray, namespaceArray}}))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT c::text AS c,n::text AS n FROM public.rc_arraydeps_view", []string{"c", "n"}, []string{"TEXT", "TEXT"}, [][]any{{collationArray, namespaceArray}}))
	})
}
