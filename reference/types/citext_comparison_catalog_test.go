package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextComparisonCatalog(t *testing.T) {
	citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		for _, tc := range []struct{ name, op, commutator, negator string }{
			{"citext_eq", "=", "=", "<>"}, {"citext_ne", "<>", "<>", "="},
			{"citext_lt", "<", ">", ">="}, {"citext_le", "<=", ">=", ">"},
			{"citext_gt", ">", "<", "<="}, {"citext_ge", ">=", "<=", "<"},
		} {
			// Resolve current locators, then independently assert semantic facts and
			// relational identity. Never compare allocator OIDs across backends.
			var proc, operator, scalar, extension int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE oid=$1::regprocedure::oid", "public."+tc.name+"(public.citext,public.citext)").Scan(&proc); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid='citext'::regtype::oid").Scan(&scalar); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_extension WHERE extname='citext'").Scan(&extension); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_operator WHERE oprname=$1 AND oprleft=$2::oid AND oprright=$2::oid", tc.op, scalar).Scan(&operator); err != nil {
				t.Fatal(err)
			}
			if proc <= 0 || operator <= 0 || scalar <= 0 || extension <= 0 {
				t.Fatal("nonpositive installed identity")
			}
			q := "SELECT p.proname,p.pronargs,p.prorettype='bool'::regtype::oid AS boolean_result,p.proisstrict,p.provolatile,p.proparallel,p.pronamespace=t.typnamespace AS same_namespace,d.deptype FROM (SELECT * FROM pg_proc WHERE oid=$1::oid) p JOIN (SELECT * FROM pg_type WHERE oid=$2::oid) t ON p.pronamespace=t.typnamespace JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND refclassid=3079 AND refobjid=$3::oid) d ON d.objid=p.oid"
			citextComparisonRoutes(t, p, b, c, fixture.Select(q, []string{"proname", "pronargs", "boolean_result", "proisstrict", "provolatile", "proparallel", "same_namespace", "deptype"}, []string{"NAME", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "CHAR"}, [][]any{{tc.name, int64(2), true, true, "i", "s", true, "e"}}, proc, scalar, extension))
			q = "SELECT o.oprname,o.oprleft=$3::oid AS left_type,o.oprright=$3::oid AS right_type,o.oprresult='bool'::regtype::oid AS boolean_result,o.oprcode::oid=$2::oid AS backing,c.oprname AS commutator,n.oprname AS negator,d.deptype FROM (SELECT * FROM pg_operator WHERE oid=$1::oid) o JOIN (SELECT * FROM pg_operator WHERE oprleft=$3::oid AND oprright=$3::oid) c ON c.oid=o.oprcom JOIN (SELECT * FROM pg_operator WHERE oprleft=$3::oid AND oprright=$3::oid) n ON n.oid=o.oprnegate JOIN (SELECT * FROM pg_depend WHERE classid=2617 AND refclassid=3079 AND refobjid=$4::oid) d ON d.objid=o.oid"
			citextComparisonRoutes(t, p, b, c, fixture.Select(q, []string{"oprname", "left_type", "right_type", "boolean_result", "backing", "commutator", "negator", "deptype"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "NAME", "CHAR"}, [][]any{{tc.op, true, true, true, true, tc.commutator, tc.negator, "e"}}, operator, proc, scalar, extension))
		}
	})
}
