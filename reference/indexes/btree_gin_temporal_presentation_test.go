package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINTemporalPresentation(t *testing.T) {
	for _, kind := range []string{"CLASS", "FAMILY", "EXTENSION"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"temporal_present_owner"})
			fixture.SchemaReferenceCleanup(t, "temporal_present_schema")
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				temporalControlSeed(run)
				run(fixture.Exec("CREATE SCHEMA temporal_present_schema", 0))
				run(fixture.Exec("CREATE ROLE temporal_present_owner", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public,temporal_present_schema TO temporal_present_owner", 0))
				type identity struct {
					class, family, helper, index int64
					name, schema                 string
				}
				identities := make([]identity, len(temporalCases))
				held := make([]fixture.HeldStatement, len(temporalCases))
				queries := make([]string, len(temporalCases))
				for i, tc := range temporalCases {
					c := &identities[i]
					c.name = tc.typ + "_ops"
					c.schema = "public"
					if err := conn.QueryRowContext(p.Ctx, "SELECT c.oid::bigint,c.opcfamily::bigint,a.amproc::oid::bigint,i.indexrelid::bigint FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$2::regclass WHERE c.opcmethod=2742 AND c.opcnamespace='public'::regnamespace AND c.opcname=$1", c.name, "temporal_saved_"+tc.typ+"_i").Scan(&c.class, &c.family, &c.helper, &c.index); err != nil {
						t.Fatal(err)
					}
					queries[i] = "SELECT id FROM temporal_saved_" + tc.typ + " WHERE v=$1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				check := func() {
					t.Helper()
					temporalControlCheck(t, p, b, conn, run)
					for i, tc := range temporalCases {
						s := ginIDs(queries[i], []int64{3, 4}, tc.boundary)
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
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA temporal_present_schema", 0))
					check()
					temporalControlDML(t, p, b, conn, run)
					run(fixture.Exec("ROLLBACK", 0))
					check()
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA temporal_present_schema", 0))
					check()
					for i := range identities {
						identities[i].schema = "temporal_present_schema"
					}
				} else {
					for i := range temporalCases {
						c := &identities[i]
						for _, action := range []string{" RENAME TO " + fmt.Sprintf("temporal_current_%d", i), " OWNER TO temporal_present_owner", " SET SCHEMA temporal_present_schema"} {
							q := "ALTER OPERATOR " + kind + " " + c.schema + "." + c.name + " USING gin" + action
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec(q, 0))
							temporalControlDML(t, p, b, conn, run)
							run(fixture.Exec("ROLLBACK", 0))
							check()
							run(fixture.Exec(q, 0))
							if action[:7] == " RENAME" {
								c.name = fmt.Sprintf("temporal_current_%d", i)
							}
							if action == " SET SCHEMA temporal_present_schema" {
								c.schema = "temporal_present_schema"
							}
							relation, prefix, oid := "pg_opclass", "opc", c.class
							if kind == "FAMILY" {
								relation, prefix, oid = "pg_opfamily", "opf", c.family
							}
							run(fixture.Select("SELECT "+prefix+"name,n.nspname FROM "+relation+" o JOIN pg_namespace n ON n.oid=o."+prefix+"namespace WHERE o.oid=$1::oid", []string{prefix + "name", "nspname"}, []string{"NAME", "NAME"}, [][]any{{c.name, c.schema}}, oid))
							if action == " OWNER TO temporal_present_owner" {
								run(fixture.Select("SELECT "+prefix+"owner=(SELECT oid FROM pg_roles WHERE rolname='temporal_present_owner') AS owned FROM "+relation+" WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							check()
						}
					}
				}
				helperSchema := "public"
				if kind == "EXTENSION" {
					helperSchema = "temporal_present_schema"
				}
				for _, tc := range temporalCases {
					run(fixture.Exec("ALTER FUNCTION "+helperSchema+".gin_extract_value_"+tc.typ+"("+tc.typ+",internal) OWNER TO temporal_present_owner", 0))
					check()
					run(fixture.Select("SELECT p.proowner=r.oid AS owned FROM pg_proc p JOIN pg_roles r ON r.rolname='temporal_present_owner' WHERE p.pronamespace=$1::regnamespace AND p.proname=$2", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, helperSchema, "gin_extract_value_"+tc.typ))
				}
				// Current extension membership is not the index's sealed provider origin.
				className := identities[0].schema + "." + identities[0].name
				if kind == "FAMILY" {
					className = "public.date_ops"
				}
				run(fixture.Exec("ALTER EXTENSION btree_gin DROP OPERATOR CLASS "+className+" USING gin", 0))
				run(fixture.Exec("ALTER EXTENSION btree_gin DROP FUNCTION "+helperSchema+".gin_extract_value_date(date,internal)", 0))
				check()
				temporalControlDML(t, p, b, conn, run)
				run(amCount("SELECT count(*) FROM pg_depend WHERE classid='pg_opclass'::regclass AND objid=$1::oid AND refclassid='pg_extension'::regclass AND deptype='e'", 0, identities[0].class))
				run(fixture.Exec("ALTER EXTENSION btree_gin ADD FUNCTION "+helperSchema+".gin_extract_value_date(date,internal)", 0))
				run(fixture.Exec("ALTER EXTENSION btree_gin ADD OPERATOR CLASS "+className+" USING gin", 0))
				check()
			})
		})
	}
}
