package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINScalarPresentation(t *testing.T) {
	for _, kind := range []string{"CLASS", "FAMILY", "EXTENSION"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"scalar_present_owner"})
			fixture.SchemaReferenceCleanup(t, "scalar_present_schema")
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				scalarControlSeed(run)
				run(fixture.Exec("CREATE SCHEMA scalar_present_schema", 0))
				run(fixture.Exec("CREATE ROLE scalar_present_owner", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public,scalar_present_schema TO scalar_present_owner", 0))
				type identity struct {
					class, family, helper, index int64
					name, schema                 string
				}
				identities := make([]identity, len(scalarCases))
				held := make([]fixture.HeldStatement, len(scalarCases))
				queries := make([]string, len(scalarCases))
				for i, tc := range scalarCases {
					c := &identities[i]
					c.name = tc.typ + "_ops"
					c.schema = "public"
					if err := conn.QueryRowContext(p.Ctx, "SELECT c.oid::bigint,c.opcfamily::bigint,a.amproc::oid::bigint,i.indexrelid::bigint FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$2::regclass WHERE c.opcmethod=2742 AND c.opcnamespace='public'::regnamespace AND c.opcname=$1", c.name, "scalar_saved_"+tc.typ+"_i").Scan(&c.class, &c.family, &c.helper, &c.index); err != nil {
						t.Fatal(err)
					}
					queries[i] = "SELECT id FROM scalar_saved_" + tc.typ + " WHERE v=$1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				check := func() {
					t.Helper()
					scalarControlCheck(t, p, b, conn, run)
					for i, tc := range scalarCases {
						s := ginIDs(queries[i], scalarTruth(tc)[2], tc.boundary)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil))
						c := identities[i]
						classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcfamily=$2::oid AS family_link,a.amproc=$3::oid AS helper_link,i.indexrelid=$4::oid AS index_link FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$4::oid WHERE c.oid=$1::oid", []string{"family_link", "helper_link", "index_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, c.class, c.family, c.helper, c.index))
					}
				}
				check()
				if kind == "EXTENSION" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA scalar_present_schema", 0))
					check()
					scalarControlDML(t, p, b, conn, run)
					run(fixture.Exec("ROLLBACK", 0))
					check()
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA scalar_present_schema", 0))
					check()
					for i := range identities {
						identities[i].schema = "scalar_present_schema"
					}
				} else {
					for i := range scalarCases {
						c := &identities[i]
						for _, action := range []string{" RENAME TO " + fmt.Sprintf("scalar_current_%d", i), " OWNER TO scalar_present_owner", " SET SCHEMA scalar_present_schema"} {
							q := "ALTER OPERATOR " + kind + " " + c.schema + "." + c.name + " USING gin" + action
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec(q, 0))
							scalarControlDML(t, p, b, conn, run)
							run(fixture.Exec("ROLLBACK", 0))
							check()
							run(fixture.Exec(q, 0))
							if action[:7] == " RENAME" {
								c.name = fmt.Sprintf("scalar_current_%d", i)
							}
							if action == " SET SCHEMA scalar_present_schema" {
								c.schema = "scalar_present_schema"
							}
							relation, prefix, oid := "pg_opclass", "opc", c.class
							if kind == "FAMILY" {
								relation, prefix, oid = "pg_opfamily", "opf", c.family
							}
							run(fixture.Select("SELECT "+prefix+"name,n.nspname FROM "+relation+" o JOIN pg_namespace n ON n.oid=o."+prefix+"namespace WHERE o.oid=$1::oid", []string{prefix + "name", "nspname"}, []string{"NAME", "NAME"}, [][]any{{c.name, c.schema}}, oid))
							if action == " OWNER TO scalar_present_owner" {
								run(fixture.Select("SELECT "+prefix+"owner=(SELECT oid FROM pg_roles WHERE rolname='scalar_present_owner') AS owned FROM "+relation+" WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							check()
						}
					}
				}
				helperSchema := "public"
				if kind == "EXTENSION" {
					helperSchema = "scalar_present_schema"
				}
				for _, tc := range scalarCases {
					run(fixture.Exec("ALTER FUNCTION "+helperSchema+".gin_extract_value_"+tc.typ+"("+tc.typ+",internal) OWNER TO scalar_present_owner", 0))
					check()
					run(fixture.Select("SELECT p.proowner=r.oid AS owned FROM pg_proc p JOIN pg_roles r ON r.rolname='scalar_present_owner' WHERE p.pronamespace=$1::regnamespace AND p.proname=$2", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, helperSchema, "gin_extract_value_"+tc.typ))
				}
				scalarControlDML(t, p, b, conn, run)
			})
		})
	}
}
