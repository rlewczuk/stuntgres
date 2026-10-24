package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// These queries reacquire current identities after every Restore as well.
func citextPatternCatalogChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, rhs := range []string{"citext", "text"} {
		for _, op := range citextPatternOps {
			var proc, operator, scalar, right, extension int64
			locate := func(q string, dst *int64, args ...any) {
				t.Helper()
				// Executor is public fixture API; identity discovery uses its QueryContext.
				rows, err := c.QueryContext(p.Ctx, q, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if !rows.Next() {
					t.Fatal("missing installed identity", q, args)
				}
				if err := rows.Scan(dst); err != nil {
					t.Fatal(err)
				}
				if rows.Next() {
					t.Fatal("duplicate installed identity", q, args)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if *dst <= 0 {
					t.Fatal("nonpositive installed identity")
				}
			}
			locate("SELECT oid::bigint FROM pg_type WHERE oid='public.citext'::regtype::oid", &scalar)
			locate("SELECT oid::bigint FROM pg_type WHERE oid=$1::regtype::oid", &right, rhs)
			locate("SELECT oid::bigint FROM pg_extension WHERE extname='citext'", &extension)
			locate("SELECT oid::bigint FROM pg_proc WHERE oid=$1::regprocedure::oid", &proc, "public."+op.routine+"(public.citext,"+rhs+")")
			locate("SELECT oid::bigint FROM pg_operator WHERE oprname=$1 AND oprleft=$2::oid AND oprright=$3::oid", &operator, op.catalog, scalar, right)
			q := "SELECT p.proname,p.pronargs,p.prorettype='bool'::regtype::oid AS result,p.proisstrict,p.provolatile,p.proparallel,p.pronamespace=t.typnamespace AS namespace,p.proowner=t.typowner AS owner,d.deptype FROM (SELECT * FROM pg_proc WHERE oid=$1::oid) p JOIN (SELECT * FROM pg_type WHERE oid=$2::oid) t ON t.typnamespace=p.pronamespace JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND refclassid=3079 AND refobjid=$3::oid) d ON d.objid=p.oid"
			citextPatternRoutes(t, p, b, c, fixture.Select(q, []string{"proname", "pronargs", "result", "proisstrict", "provolatile", "proparallel", "namespace", "owner", "deptype"}, []string{"NAME", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "CHAR"}, [][]any{{op.routine, int64(2), true, true, "i", "s", true, true, "e"}}, proc, scalar, extension))
			// Resolve the display row by the current exact overload, not its shared name.
			specific := op.routine + "_" + strconv.FormatInt(proc, 10)
			q = "SELECT routine_definition,external_language,external_name FROM information_schema.routines WHERE specific_schema='public' AND specific_name=$1"
			citextPatternRoutes(t, p, b, c, fixture.Select(q, []string{"routine_definition", "external_language", "external_name"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{op.routine, "INTERNAL", nil}}, specific))
			stem := "iclike"
			if op.regex {
				stem = "icregexeq"
			}
			if op.negative {
				if op.regex {
					stem = "icregexne"
				} else {
					stem = "icnlike"
				}
			}
			// Estimators are lookup-only builtin identities, not callable pg_proc rows.
			// Compare the actual operator references with exact public regproc lookup.
			q = "SELECT o.oprname,o.oprleft=$3::oid AS left_type,o.oprright=$4::oid AS right_type,o.oprresult='bool'::regtype::oid AS result,o.oprcode::oid=$2::oid AS backing,o.oprcom=0 AS no_commutator,n.oprname AS negator,n.oprleft=o.oprleft AND n.oprright=o.oprright AS negator_signature,NOT o.oprcanhash AS no_hash,NOT o.oprcanmerge AS no_merge,o.oprrest::oid='pg_catalog." + stem + "sel'::regproc::oid AS restriction,o.oprjoin::oid='pg_catalog." + stem + "joinsel'::regproc::oid AS joining,d.deptype FROM (SELECT * FROM pg_operator WHERE oid=$1::oid) o JOIN pg_operator n ON n.oid=o.oprnegate JOIN (SELECT * FROM pg_depend WHERE classid=2617 AND refclassid=3079 AND refobjid=$5::oid) d ON d.objid=o.oid"
			citextPatternRoutes(t, p, b, c, fixture.Select(q, []string{"oprname", "left_type", "right_type", "result", "backing", "no_commutator", "negator", "negator_signature", "no_hash", "no_merge", "restriction", "joining", "deptype"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR"}, [][]any{{op.catalog, true, true, true, true, true, op.negator, true, true, true, true, true, "e"}}, operator, proc, scalar, right, extension))
			citextPatternRoutes(t, p, b, c, fixture.Select("SELECT o.oprnamespace=t.typnamespace AS namespace,o.oprowner=t.typowner AS owner FROM (SELECT * FROM pg_operator WHERE oid=$1::oid) o CROSS JOIN (SELECT * FROM pg_type WHERE oid=$2::oid) t", []string{"namespace", "owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, operator, scalar))
		}
	}
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_operator WHERE oprleft='text'::regtype::oid AND oprright='citext'::regtype::oid AND oprname IN ('~','~*','!~','!~*','~~','~~*','!~~','!~~*')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_operator WHERE oprleft='citext'::regtype::oid AND oprright IN ('citext'::regtype::oid,'text'::regtype::oid) AND oprname IN ('~','~*','!~','!~*','~~','~~*','!~~','!~~*')", []string{"n"}, []string{"INT8"}, [][]any{{int64(16)}}))
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('texticlike','texticnlike','texticregexeq','texticregexne')", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
}

func TestReferenceCitextPatternCatalog(t *testing.T) {
	citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		citextPatternCatalogChecks(t, p, b, c)
		for _, rhs := range []string{"citext", "text"} {
			for _, tc := range []struct {
				name string
				want bool
			}{{"texticlike", true}, {"texticnlike", false}, {"texticregexeq", true}, {"texticregexne", false}} {
				citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT public."+tc.name+"('ABC'::citext,'abc'::"+rhs+") AS v", tc.want))
				citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT public."+tc.name+"($1::citext,$2::"+rhs+") AS v", tc.want, "ABC", "abc"))
			}
		}
	})
}
