package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Inventory expectations are the finite REL_18_6 signature lists, not counts
// learned from a backend. PG's additional hash/cross-type index objects are not
// part of this bounded same-type ORDER BY contract.
func TestReferenceIsnConversionInventory(t *testing.T) {
	t.Run("predicates", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			roles := isnConversionRoleList()
			q := `SELECT l.typname::text AS lhs,r.typname::text AS rhs,p.proname::text AS routine,p.prorettype='boolean'::regtype AS result,lang.lanname::text AS language,p.prosrc,p.probin,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proretset FROM pg_proc p JOIN pg_type l ON l.oid=p.proargtypes[0] JOIN pg_type r ON r.oid=p.proargtypes[1] JOIN pg_language lang ON lang.oid=p.prolang WHERE p.pronamespace='public'::regnamespace AND p.pronargs=2 AND p.proname IN ('isneq','isnne','isnlt','isnle','isngt','isnge') AND l.typname IN ` + roles + ` AND r.typname IN ` + roles
			rows := [][]any{}
			for _, pair := range isnConversionPairs() {
				for _, op := range isnConversionOps {
					rows = append(rows, []any{isnConversionRoles[pair[0]].name, isnConversionRoles[pair[1]].name, op.routine, true, "internal", op.body, nil, true, "i", "s", false})
				}
			}
			s := fixture.Select(q, []string{"lhs", "rhs", "routine", "result", "language", "prosrc", "probin", "proisstrict", "volatility", "parallel", "proretset"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL"}, rows)
			s.Bag = true
			run(s)
			run(fixture.Prepared(s))
		})
	})
	t.Run("operators", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			roles := isnConversionRoleList()
			q := `SELECT l.typname::text AS lhs,r.typname::text AS rhs,o.oprname::text AS operator,p.proname::text AS routine,o.oprresult='boolean'::regtype AS result,p.proargtypes[0]=o.oprleft AND p.proargtypes[1]=o.oprright AND p.prorettype=o.oprresult AS signature,comm.oprname::text AS commutator,comm.oprleft=o.oprright AND comm.oprright=o.oprleft AS reversed,neg.oprname::text AS negator,neg.oprleft=o.oprleft AND neg.oprright=o.oprright AS same_pair,o.oprcanhash,o.oprcanmerge,rs.proname::text AS restriction,js.proname::text AS joiner FROM pg_operator o JOIN pg_type l ON l.oid=o.oprleft JOIN pg_type r ON r.oid=o.oprright LEFT JOIN pg_proc p ON p.oid=o.oprcode LEFT JOIN pg_operator comm ON comm.oid=o.oprcom LEFT JOIN pg_operator neg ON neg.oid=o.oprnegate LEFT JOIN pg_proc rs ON rs.oid=o.oprrest LEFT JOIN pg_proc js ON js.oid=o.oprjoin WHERE o.oprnamespace='public'::regnamespace AND o.oprname IN ('=','<>','<','<=','>','>=') AND l.typname IN ` + roles + ` AND r.typname IN ` + roles
			rows := [][]any{}
			for _, pair := range isnConversionPairs() {
				for _, op := range isnConversionOps {
					rows = append(rows, []any{isnConversionRoles[pair[0]].name, isnConversionRoles[pair[1]].name, op.operator, op.routine, true, true, op.commutator, true, op.negator, true, op.operator == "=", op.operator == "=", op.restriction, op.join})
				}
			}
			s := fixture.Select(q, []string{"lhs", "rhs", "operator", "routine", "result", "signature", "commutator", "reversed", "negator", "same_pair", "oprcanhash", "oprcanmerge", "restriction", "joiner"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, rows)
			s.Bag = true
			run(s)
			run(fixture.Prepared(s))
		})
	})
	t.Run("casts_and_functions", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			roles := isnConversionRoleList()
			q := `SELECT s.typname::text AS source,t.typname::text AS target,c.castcontext::text AS context,c.castmethod::text AS method,c.castfunc=0 AS no_function,p.proname::text AS routine FROM pg_cast c JOIN pg_type s ON s.oid=c.castsource JOIN pg_type t ON t.oid=c.casttarget LEFT JOIN pg_proc p ON p.oid=c.castfunc WHERE s.typnamespace='public'::regnamespace AND t.typnamespace='public'::regnamespace AND s.typname IN ` + roles + ` AND t.typname IN ` + roles
			rows := [][]any{}
			for _, edge := range isnConversionEdges() {
				context, method, absent := "a", "b", true
				var name any
				if edge[0] == 0 {
					context, method, absent, name = "e", "f", false, isnConversionRoles[edge[1]].name
				}
				rows = append(rows, []any{isnConversionRoles[edge[0]].name, isnConversionRoles[edge[1]].name, context, method, absent, name})
			}
			s := fixture.Select(q, []string{"source", "target", "context", "method", "no_function", "routine"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT"}, rows)
			s.Bag = true
			run(s)
			rows = nil
			for i := 1; i < len(isnConversionRoles); i++ {
				role := isnConversionRoles[i].name
				family := role
				if i == 2 || i == 4 || i == 6 {
					family = isnConversionRoles[i-1].name
				}
				rows = append(rows, []any{role, role, "c", family + "_cast_from_ean13", "$libdir/isn", true, "i", "s", false, true})
			}
			q = `SELECT p.proname::text AS routine,t.typname::text AS result,l.lanname::text AS language,p.prosrc,p.probin,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proretset,c.castfunc=p.oid AS cast_identity FROM pg_proc p JOIN pg_type t ON t.oid=p.prorettype JOIN pg_language l ON l.oid=p.prolang JOIN pg_cast c ON c.castsource=p.proargtypes[0] AND c.casttarget=p.prorettype WHERE p.pronamespace='public'::regnamespace AND p.pronargs=1 AND p.proargtypes[0]='ean13'::regtype AND p.proname IN ` + roles
			s = fixture.Select(q, []string{"routine", "result", "language", "prosrc", "probin", "proisstrict", "volatility", "parallel", "proretset", "cast_identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL"}, rows)
			s.Bag = true
			run(s)
			run(fixture.Prepared(s))
		})
	})
	t.Run("same_type_btree", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			q := `SELECT t.typname::text AS role,c.opcname::text AS class,f.opfname::text AS family,c.opcdefault,sp.amprocnum::integer AS slot,p.proname::text AS routine,p.prorettype='integer'::regtype AS result,l.lanname::text AS language,p.prosrc,p.probin,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel FROM pg_opclass c JOIN pg_am am ON am.oid=c.opcmethod JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_type t ON t.oid=c.opcintype JOIN pg_amproc sp ON sp.amprocfamily=f.oid AND sp.amproclefttype=t.oid AND sp.amprocrighttype=t.oid JOIN pg_proc p ON p.oid=sp.amproc JOIN pg_language l ON l.oid=p.prolang WHERE am.amname='btree' AND c.opcnamespace='public'::regnamespace AND t.typname IN ` + isnConversionRoleList()
			rows := [][]any{}
			for _, role := range isnConversionRoles {
				rows = append(rows, []any{role.name, role.name + "_ops", "isn_ops", true, int64(1), "bt" + role.name + "cmp", true, "internal", "btint8cmp", nil, true, "i", "s"})
			}
			s := fixture.Select(q, []string{"role", "class", "family", "opcdefault", "slot", "routine", "result", "language", "prosrc", "probin", "proisstrict", "volatility", "parallel"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "INT4", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT"}, rows)
			s.Bag = true
			run(s)
			q = `SELECT t.typname::text AS role,m.amopstrategy::integer AS strategy,o.oprname::text AS operator,m.amoppurpose::text AS purpose,o.oprleft=t.oid AND o.oprright=t.oid AS signature FROM pg_opclass c JOIN pg_am am ON am.oid=c.opcmethod JOIN pg_type t ON t.oid=c.opcintype JOIN pg_amop m ON m.amopfamily=c.opcfamily AND m.amoplefttype=t.oid AND m.amoprighttype=t.oid JOIN pg_operator o ON o.oid=m.amopopr WHERE am.amname='btree' AND c.opcnamespace='public'::regnamespace AND t.typname IN ` + isnConversionRoleList()
			rows = nil
			for _, role := range isnConversionRoles {
				for i, op := range []string{"<", "<=", "=", ">=", ">"} {
					rows = append(rows, []any{role.name, int64(i + 1), op, "s", true})
				}
			}
			s = fixture.Select(q, []string{"role", "strategy", "operator", "purpose", "signature"}, []string{"TEXT", "INT4", "TEXT", "TEXT", "BOOL"}, rows)
			s.Bag = true
			run(s)
		})
	})
}
