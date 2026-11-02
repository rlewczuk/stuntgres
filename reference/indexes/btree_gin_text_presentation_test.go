package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextPresentation(t *testing.T) {
	for _, kind := range []string{"CLASS", "FAMILY", "EXTENSION"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"text_present_owner"})
			fixture.SchemaReferenceCleanup(t, "text_present_schema")
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				textControlSeed(run)
				run(fixture.Exec("CREATE TABLE text_present_empty(t text,v varchar)", 0))
				run(fixture.Exec("CREATE INDEX text_present_empty_i ON text_present_empty USING gin(t text_ops,v varchar_ops)", 0))
				run(fixture.Exec("CREATE SCHEMA text_present_schema", 0))
				run(fixture.Exec("CREATE ROLE text_present_owner", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public,text_present_schema TO text_present_owner", 0))
				type identity struct {
					class, family, helper, index int64
					name, schema                 string
				}
				identities := make([]identity, len(textCases))
				held := make([]fixture.HeldStatement, len(textCases))
				queries := make([]string, len(textCases))
				for i, tc := range textCases {
					c := &identities[i]
					c.name = tc.typ + "_ops"
					c.schema = "public"
					if err := conn.QueryRowContext(p.Ctx, "SELECT c.oid::bigint,c.opcfamily::bigint,a.amproc::oid::bigint,i.indexrelid::bigint FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$2::regclass WHERE c.opcmethod=2742 AND c.opcnamespace='public'::regnamespace AND c.opcname=$1", c.name, "text_saved_"+tc.typ+"_i").Scan(&c.class, &c.family, &c.helper, &c.index); err != nil {
						t.Fatal(err)
					}
					queries[i] = "SELECT id FROM text_saved_" + tc.typ + " WHERE v=$1::" + textSQLType(tc) + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				if identities[0].helper != identities[1].helper || identities[0].helper == 0 {
					t.Fatal("text/varchar do not share actual helper identity")
				}
				check := func() {
					textControlCheck(t, p, b, conn, run)
					for i, tc := range textCases {
						s := ginIDs(queries[i], textTruth(tc)[2], textArgument(tc, tc.boundary))
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil))
						c := identities[i]
						classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcfamily=$2::oid AS family_link,a.amproc=$3::oid AS helper_link,i.indexrelid=$4::oid AS index_link FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$4::oid WHERE c.oid=$1::oid", []string{"family_link", "helper_link", "index_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, c.class, c.family, c.helper, c.index))
					}
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_saved_text WHERE v<>'ab'::text ORDER BY id", []int64{1, 2, 5, 6}))
				}
				check()
				if kind == "EXTENSION" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA text_present_schema", 0))
					check()
					textControlDML(t, p, b, conn, run)
					run(fixture.Exec("ROLLBACK", 0))
					check()
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA text_present_schema", 0))
					for i := range identities {
						identities[i].schema = "text_present_schema"
					}
					check()
				} else {
					for i := range textCases {
						c := &identities[i]
						for _, action := range []string{" RENAME TO " + fmt.Sprintf("text_current_%d", i), " OWNER TO text_present_owner", " SET SCHEMA text_present_schema"} {
							q := "ALTER OPERATOR " + kind + " " + c.schema + "." + c.name + " USING gin" + action
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec(q, 0))
							textControlDML(t, p, b, conn, run)
							run(fixture.Exec("ROLLBACK", 0))
							check()
							run(fixture.Exec(q, 0))
							if action[:7] == " RENAME" {
								c.name = fmt.Sprintf("text_current_%d", i)
							}
							if action == " SET SCHEMA text_present_schema" {
								c.schema = "text_present_schema"
							}
							relation, prefix, oid := "pg_opclass", "opc", c.class
							if kind == "FAMILY" {
								relation, prefix, oid = "pg_opfamily", "opf", c.family
							}
							run(fixture.Select("SELECT "+prefix+"name,n.nspname FROM "+relation+" o JOIN pg_namespace n ON n.oid=o."+prefix+"namespace WHERE o.oid=$1::oid", []string{prefix + "name", "nspname"}, []string{"NAME", "NAME"}, [][]any{{c.name, c.schema}}, oid))
							if action == " OWNER TO text_present_owner" {
								run(fixture.Select("SELECT "+prefix+"owner=(SELECT oid FROM pg_roles WHERE rolname='text_present_owner') AS owned FROM "+relation+" WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							check()
						}
					}
				}
				helperSchema := "public"
				if kind == "EXTENSION" {
					helperSchema = "text_present_schema"
				}
				// One actual text helper mutation must refresh BOTH selected classes,
				// including the empty mixed root, not just the text class's roots.
				helper := helperSchema + ".gin_extract_value_text(text,internal)"
				for _, q := range []string{"ALTER FUNCTION " + helper + " OWNER TO text_present_owner", "ALTER FUNCTION " + helper + " RENAME TO text_current_extract"} {
					run(fixture.Exec(q, 0))
					check()
					textControlDML(t, p, b, conn, run)
				}
				helper = helperSchema + ".text_current_extract(text,internal)"
				run(fixture.Exec("ALTER EXTENSION btree_gin DROP FUNCTION "+helper, 0))
				check()
				textControlDML(t, p, b, conn, run)
				run(amCount("SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::oid AND refclassid='pg_extension'::regclass AND deptype='e'", 0, identities[0].helper))
				run(fixture.Exec("ALTER FUNCTION "+helper+" SET SCHEMA "+map[bool]string{true: "public", false: "text_present_schema"}[helperSchema == "text_present_schema"], 0))
				helperSchema = map[bool]string{true: "public", false: "text_present_schema"}[helperSchema == "text_present_schema"]
				helper = helperSchema + ".text_current_extract(text,internal)"
				check()
				run(fixture.Exec("ALTER EXTENSION btree_gin ADD FUNCTION "+helper, 0))
				check()
				run(fixture.Exec("INSERT INTO text_present_empty VALUES('ab','ab ')", 1))
				classRoutes(t, p, b, conn, fixture.Select("SELECT t,v FROM text_present_empty", []string{"t", "v"}, []string{"TEXT", "VARCHAR"}, [][]any{{"ab", "ab "}}))
			})
		})
	}
}
