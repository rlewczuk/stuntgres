package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalOperatorClassDefinitions(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit_family", true: "implicit_family"}[implicit], func(t *testing.T) {
			definitionRoutes(t, func(t *testing.T, r definitionRoute) {
				fixture.RoleReferenceCleanup(t, []string{"tx_class_owner"})
				fixture.SchemaReferenceCleanup(t, "tx_class_dest")
				definitionScenario(t, []string{"tx_class_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := definitionRun(t, p, b, c)
					run(fixture.Exec(`CREATE SCHEMA tx_class_dest`, 0))
					run(fixture.Exec(`CREATE ROLE tx_class_owner`, 0))
					run(fixture.Exec(`GRANT CREATE ON SCHEMA tx_class_dest TO tx_class_owner`, 0))
					run(fixture.Exec(`CREATE ACCESS METHOD tx_class_am TYPE INDEX HANDLER bthandler`, 0))
					family := "tx_class_family"
					familyClause := ` FAMILY public.tx_class_family`
					if implicit {
						family = "tx_class"
						familyClause = ""
					} else {
						run(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_class_family USING tx_class_am`, 0))
					}
					queries := []string{
						`CREATE OPERATOR CLASS public.tx_class DEFAULT FOR TYPE integer USING tx_class_am` + familyClause + ` AS ` + definitionClassItems + `, STORAGE integer`,
						`ALTER OPERATOR CLASS public.tx_class USING tx_class_am RENAME TO tx_class_renamed`,
						`ALTER OPERATOR CLASS public.tx_class_renamed USING tx_class_am SET SCHEMA tx_class_dest`,
						`ALTER OPERATOR CLASS tx_class_dest.tx_class_renamed USING tx_class_am OWNER TO tx_class_owner`,
						`DROP OPERATOR CLASS tx_class_dest.tx_class_renamed USING tx_class_am RESTRICT`,
					}
					command := r.commands(t, p, b, c, queries)
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_class'`, 0))
					e, end := r.begin(t, p, b, c)
					command(e, queries[0])
					id := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_class'`)
					familyID := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname=$1`, family)
					methodID := definitionID(t, p, e, `SELECT oid::bigint FROM pg_am WHERE amname='tx_class_am'`)
					definitionRun(t, p, b, e)(fixture.Select(`SELECT opcmethod::bigint,opcintype::bigint,opcdefault,opckeytype::bigint,opcfamily::bigint,opcnamespace='public'::regnamespace FROM pg_opclass WHERE opcname='tx_class'`, []string{"opcmethod", "opcintype", "opcdefault", "opckeytype", "opcfamily", "?column?"}, []string{"INT8", "INT8", "BOOL", "INT8", "INT8", "BOOL"}, [][]any{{methodID, int64(23), true, int64(0), familyID, true}}))
					definitionRun(t, p, b, e)(fixture.Select(`SELECT amprocnum,amproc::oid::bigint,amproclefttype::bigint,amprocrighttype::bigint FROM pg_amproc WHERE amprocfamily=$1::oid`, []string{"amprocnum", "amproc", "amproclefttype", "amprocrighttype"}, []string{"INT2", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(351), int64(23), int64(23)}}, familyID))
					definitionRun(t, p, b, e)(definitionCount(`SELECT count(*) FROM pg_amop WHERE amopfamily=$1::oid AND amopmethod=$2::oid`, 5, familyID, methodID))
					for _, q := range queries[1:4] {
						command(e, q)
					}
					definitionRun(t, p, b, e)(fixture.Select(`SELECT c.oid::bigint,n.nspname,r.rolname,c.opcfamily::bigint FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_roles r ON r.oid=c.opcowner WHERE opcname='tx_class_renamed'`, []string{"oid", "nspname", "rolname", "opcfamily"}, []string{"INT8", "NAME", "NAME", "INT8"}, [][]any{{id, "tx_class_dest", "tx_class_owner", familyID}}))
					absent := definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_class','tx_class_renamed')`, 0)
					if r.mode != "autocommit" {
						definitionRun(t, p, b, b.DB)(absent)
					}
					end()
					n := int64(0)
					if r.commit {
						n = 1
					}
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_class_renamed'`, n))
					if !r.commit {
						if implicit {
							run(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_class'`, 0))
						}
						for _, q := range queries[:4] {
							run(fixture.Exec(q, 0))
						}
						id = definitionID(t, p, c, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_class_renamed'`)
					}
					e, end = r.begin(t, p, b, c)
					command(e, queries[4])
					definitionRun(t, p, b, e)(absent)
					end()
					if !r.commit {
						if got := definitionID(t, p, c, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_class_renamed'`); got != id {
							t.Fatal("DROP rollback changed class identity", id, got)
						}
						run(fixture.Exec(queries[4], 0))
					}
					run(absent)
					run(fixture.Exec(`DROP OPERATOR FAMILY public.`+family+` USING tx_class_am RESTRICT`, 0))
				})
			})
		})
	}
}
