package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalOperatorFamilyDefinitions(t *testing.T) {
	definitionRoutes(t, func(t *testing.T, r definitionRoute) {
		fixture.RoleReferenceCleanup(t, []string{"tx_definition_owner"})
		fixture.SchemaReferenceCleanup(t, "tx_definition_dest")
		definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := definitionRun(t, p, b, c)
			run(fixture.Exec(`CREATE SCHEMA tx_definition_dest`, 0))
			run(fixture.Exec(`CREATE ROLE tx_definition_owner`, 0))
			run(fixture.Exec(`GRANT CREATE ON SCHEMA tx_definition_dest TO tx_definition_owner`, 0))
			queries := []string{
				`CREATE OPERATOR FAMILY public.tx_definition_family USING btree`,
				`ALTER OPERATOR FAMILY public.tx_definition_family USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer), OPERATOR 3 pg_catalog.=(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
				`ALTER OPERATOR FAMILY public.tx_definition_family USING btree DROP OPERATOR 3(integer,integer), FUNCTION 2(integer,integer)`,
				`ALTER OPERATOR FAMILY public.tx_definition_family USING btree RENAME TO tx_definition_renamed`,
				`ALTER OPERATOR FAMILY public.tx_definition_renamed USING btree SET SCHEMA tx_definition_dest`,
				`ALTER OPERATOR FAMILY tx_definition_dest.tx_definition_renamed USING btree OWNER TO tx_definition_owner`,
				`DROP OPERATOR FAMILY tx_definition_dest.tx_definition_renamed USING btree RESTRICT`,
			}
			command := r.commands(t, p, b, c, queries)
			e, end := r.begin(t, p, b, c)
			command(e, queries[0])
			id := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_definition_family'`)
			command(e, queries[1])
			definitionFamilyMembers(t, p, b, e, "tx_definition_family", true)
			command(e, queries[2])
			definitionFamilyMembers(t, p, b, e, "tx_definition_family", false)
			for _, q := range queries[3:6] {
				command(e, q)
			}
			definitionRun(t, p, b, e)(fixture.Select(`SELECT f.oid::bigint,opfmethod::bigint,n.nspname,r.rolname FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace JOIN pg_roles r ON r.oid=f.opfowner WHERE opfname='tx_definition_renamed'`, []string{"oid", "opfmethod", "nspname", "rolname"}, []string{"INT8", "INT8", "NAME", "NAME"}, [][]any{{id, int64(403), "tx_definition_dest", "tx_definition_owner"}}))
			absent := definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname IN ('tx_definition_family','tx_definition_renamed')`, 0)
			if r.mode != "autocommit" {
				definitionRun(t, p, b, b.DB)(absent)
			}
			end()
			n := int64(0)
			if r.commit {
				n = 1
			}
			run(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_definition_renamed'`, n))
			if !r.commit {
				for _, q := range queries[:6] {
					run(fixture.Exec(q, 0))
				}
				id = definitionID(t, p, c, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_definition_renamed'`)
			}
			e, end = r.begin(t, p, b, c)
			command(e, queries[6])
			definitionRun(t, p, b, e)(absent)
			end()
			if !r.commit {
				if got := definitionID(t, p, c, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_definition_renamed'`); got != id {
					t.Fatal("DROP rollback changed family", id, got)
				}
				run(fixture.Exec(queries[6], 0))
			}
			run(absent)
			// Method is part of the namespace/name identity, not an alias for btree.
			run(fixture.Exec(queries[0], 0))
			run(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_definition_family USING hash`, 0))
			run(fixture.Select(`SELECT opfmethod::bigint FROM pg_opfamily WHERE opfname='tx_definition_family' ORDER BY opfmethod`, []string{"opfmethod"}, []string{"INT8"}, [][]any{{int64(403)}, {int64(405)}}))
		})
	})
}

func definitionFamilyMembers(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string, optional bool) {
	t.Helper()
	operators := [][]any{{int64(1), int64(23), int64(23), int64(97), "s", int64(403), int64(0)}}
	support := [][]any{{int64(1), int64(23), int64(23), int64(351)}}
	if optional {
		operators = append(operators, []any{int64(3), int64(23), int64(23), int64(96), "s", int64(403), int64(0)})
		support = append(support, []any{int64(2), int64(23), int64(23), int64(3130)})
	}
	run := definitionRun(t, p, b, e)
	run(fixture.Select(`SELECT a.amopstrategy,a.amoplefttype::bigint,a.amoprighttype::bigint,a.amopopr::bigint,a.amoppurpose,a.amopmethod::bigint,a.amopsortfamily::bigint FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname=$1 ORDER BY a.amopstrategy`, []string{"amopstrategy", "amoplefttype", "amoprighttype", "amopopr", "amoppurpose", "amopmethod", "amopsortfamily"}, []string{"INT2", "INT8", "INT8", "INT8", "CHAR", "INT8", "INT8"}, operators, name))
	run(fixture.Select(`SELECT a.amprocnum,a.amproclefttype::bigint,a.amprocrighttype::bigint,a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname=$1 ORDER BY a.amprocnum`, []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "INT8", "INT8", "INT8"}, support, name))
}
