package reference_test

import (
	"bytes"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINCompoundPresentation(t *testing.T) {
	for _, kind := range []string{"CLASS", "FAMILY", "EXTENSION"} {
		t.Run(kind, func(t *testing.T) {
			// Cleanup is LIFO: remove installed objects and granted schemas before
			// dropping their owner/grantee role.
			fixture.RoleReferenceCleanup(t, []string{"compound_present_owner"})
			fixture.SchemaReferenceCleanup(t, "compound_present_schema")
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				compoundControlSeed(run)
				run(fixture.Exec("CREATE SCHEMA compound_present_schema", 0))
				run(fixture.Exec("CREATE ROLE compound_present_owner", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public,compound_present_schema TO compound_present_owner", 0))
				type identity struct {
					name, schema string
					oid, family  int64
				}
				classes := []identity{{name: "bit_ops", schema: "public"}, {name: "varbit_ops", schema: "public"}, {name: "enum_ops", schema: "public"}, {name: "money_ops", schema: "public"}}
				for i := range classes {
					if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint,opcfamily::bigint FROM pg_opclass WHERE opcmethod=2742 AND opcnamespace='public'::regnamespace AND opcname=$1", classes[i].name).Scan(&classes[i].oid, &classes[i].family); err != nil {
						t.Fatal(err)
					}
				}
				type predicate struct {
					query    string
					held     fixture.HeldStatement
					ids      []int64
					boundary string
				}
				var predicates []predicate
				for _, tc := range []struct {
					column, typ, boundary string
					want                  [][]int64
				}{
					{"b", "bit(3)", "100", [][]int64{{2}, {1, 2, 4}, {1, 4}, {1, 4}, nil}},
					{"v", "varbit", "0", [][]int64{{2}, {1, 2}, {1}, {1, 4}, {4}}},
					{"e", "compound_saved_enum", "a", [][]int64{{2}, {1, 2, 4}, {1, 4}, {1, 4}, nil}},
					{"m", "money", "0.01", [][]int64{{2}, {1, 2, 4}, {1, 4}, {1, 4}, nil}},
				} {
					for i, op := range []string{"<", "<=", "=", ">=", ">"} {
						q := "SELECT id FROM compound_saved WHERE " + tc.column + op + "$1::" + tc.typ + " ORDER BY id"
						predicates = append(predicates, predicate{q, classHeld(t, p, conn, q), tc.want[i], tc.boundary})
					}
				}
				check := func() {
					t.Helper()
					compoundControlCheck(b, run)
					for _, v := range predicates {
						s := ginIDs(v.query, v.ids, v.boundary)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, v.held, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, v.held, b.Recorder, ginIDs(v.query, nil, nil))
					}
					for _, c := range classes {
						relation, prefix, oid := "pg_opclass", "opc", c.oid
						if kind == "FAMILY" {
							relation, prefix, oid = "pg_opfamily", "opf", c.family
						}
						if kind == "EXTENSION" {
							relation, prefix = "pg_opclass", "opc"
						}
						q := "SELECT o." + prefix + "name,n.nspname,o." + prefix + "method::bigint FROM " + relation + " o JOIN pg_namespace n ON n.oid=o." + prefix + "namespace WHERE o.oid=$1::oid"
						classRoutes(t, p, b, conn, fixture.Select(q, []string{prefix + "name", "nspname", prefix + "method"}, []string{"NAME", "NAME", "INT8"}, [][]any{{c.name, c.schema, int64(2742)}}, oid))
					}
				}
				const insertSQL = "INSERT INTO compound_saved VALUES($1,$2::bit(3),$3::varbit,$4::compound_saved_enum,$5::money,'{2}','cat:1 rat:2')"
				insert := classHeld(t, p, conn, insertSQL)
				const updateSQL = "UPDATE compound_saved SET b=$1::bit(3),v=$2::varbit,e=$3::compound_saved_enum,m=$4::money WHERE id=5"
				update := classHeld(t, p, conn, updateSQL)
				postDML := func() {
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 5, "111", "10", "m", "0.02"))
					run(ginIDs("SELECT id FROM compound_saved WHERE b>'100'::bit(3) AND v>'0'::varbit AND e>'a'::compound_saved_enum AND m>'0.01'::money ORDER BY id", []int64{5}))
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, "000", "", "z", "-0.02"))
					run(ginIDs("SELECT id FROM compound_saved WHERE b<'011'::bit(3) AND v=''::varbit AND e<'q'::compound_saved_enum AND m<'-0.01'::money ORDER BY id", []int64{5}))
					run(fixture.Exec("DELETE FROM compound_saved WHERE id=5", 1))
					check()
				}
				check()
				if kind == "EXTENSION" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA compound_present_schema", 0))
					for i := range classes {
						classes[i].schema = "compound_present_schema"
					}
					postDML()
					run(fixture.Exec("ROLLBACK", 0))
					for i := range classes {
						classes[i].schema = "public"
					}
					check()
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA compound_present_schema", 0))
					for i := range classes {
						classes[i].schema = "compound_present_schema"
					}
					postDML()
					classRoutes(t, p, b, conn, fixture.Select("SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"nspname"}, []string{"NAME"}, [][]any{{"compound_present_schema"}}))
				} else {
					for _, action := range []string{"rename", "owner", "schema"} {
						for i := range classes {
							c := &classes[i]
							clause := ""
							switch action {
							case "rename":
								clause = " RENAME TO " + fmt.Sprintf("compound_current_%d", i)
							case "owner":
								clause = " OWNER TO compound_present_owner"
							case "schema":
								clause = " SET SCHEMA compound_present_schema"
							}
							// Roll back each transition once while a populated root and held
							// plans exist; the exact original logical identity must survive.
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec("ALTER OPERATOR "+kind+" "+c.schema+"."+c.name+" USING gin"+clause, 0))
							fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 5, "111", "10", "m", "0.02"))
							run(ginIDs("SELECT id FROM compound_saved WHERE id=5 ORDER BY id", []int64{5}))
							run(fixture.Exec("ROLLBACK", 0))
							check()
							run(fixture.Exec("ALTER OPERATOR "+kind+" "+c.schema+"."+c.name+" USING gin"+clause, 0))
							if action == "rename" {
								c.name = fmt.Sprintf("compound_current_%d", i)
							}
							if action == "schema" {
								c.schema = "compound_present_schema"
							}
							if action == "owner" {
								relation, prefix, oid := "pg_opclass", "opc", c.oid
								if kind == "FAMILY" {
									relation, prefix, oid = "pg_opfamily", "opf", c.family
								}
								run(fixture.Select("SELECT o."+prefix+"owner=(SELECT oid FROM pg_roles WHERE rolname='compound_present_owner') AS owned FROM "+relation+" o WHERE o.oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							postDML()
						}
					}
				}
				// Inquiry resolves the same current class identities without assuming
				// extension OIDs agree between two independent allocators.
				for _, column := range []string{"b", "v", "e", "m"} {
					run(fixture.Select("SELECT pg_get_indexdef(indexrelid,1,true) AS key FROM pg_index WHERE indexrelid=$1::regclass", []string{"key"}, []string{"TEXT"}, [][]any{{column}}, "compound_saved_"+column))
				}
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					// Restored catalog IDs are rediscovered: old allocated numbers are not
					// an identity contract. Old enum-typed held plans are covered separately
					// by Controls; use fresh scalar assertions after generation replacement.
					compoundControlCheck(b, run)
					compoundControlPostDML(run)
					compoundControlCheck(b, run)
				}
			})
		})
	}
	t.Run("membership_dependencies", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TYPE compound_detached_enum AS ENUM('z','a','m')", 0))
			run(fixture.Exec("CREATE TABLE compound_detached(id integer,b bit(3),v varbit,e compound_detached_enum,m money)", 0))
			run(fixture.Exec("CREATE INDEX compound_detached_i ON compound_detached USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops)", 0))
			run(fixture.Exec("INSERT INTO compound_detached VALUES(1,'100','0','a','0.01'),(2,NULL,NULL,NULL,NULL)", 2))
			for _, name := range []string{"bit_ops", "varbit_ops", "enum_ops", "money_ops"} {
				run(fixture.Exec("ALTER EXTENSION btree_gin DROP OPERATOR CLASS "+name+" USING gin", 0))
				run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_opclass c ON c.oid=d.objid WHERE d.classid='pg_opclass'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e' AND c.opcmethod=2742 AND c.opcname=$1", 0, name))
				const q = "SELECT id FROM compound_detached WHERE b='100'::bit(3) AND v>=''::varbit AND e>='z'::compound_detached_enum AND m>'0'::money ORDER BY id"
				classRoutes(t, p, b, conn, ginIDs(q, []int64{1}))
				run(fixture.Prepared(fixture.Exec("INSERT INTO compound_detached VALUES(3,'111','10','m','0.02')", 1)))
				run(fixture.Exec("UPDATE compound_detached SET b='100',v='0',e='a',m='0.01' WHERE id=3", 1))
				run(ginIDs(q, []int64{1, 3}))
				run(fixture.Exec("DELETE FROM compound_detached WHERE id=3", 1))
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop operator class "+name+" for access method gin because other objects depend on it")
				d.Detail = "index compound_detached_i depends on operator class " + name + " for access method gin"
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				ginUtilityError(t, p, b, conn, "DROP OPERATOR CLASS "+name+" USING gin", d)
				run(ginIDs(q, []int64{1}))
				run(fixture.Exec("ALTER EXTENSION btree_gin ADD OPERATOR CLASS "+name+" USING gin", 0))
			}
			run(fixture.Exec("ALTER EXTENSION btree_gin DROP OPERATOR CLASS money_ops USING gin", 0))
			run(fixture.Exec("DROP OPERATOR CLASS money_ops USING gin CASCADE", 0))
			compoundIndexAbsent(run, "compound_detached", "compound_detached_i", 0)
			run(ginIDs("SELECT id FROM compound_detached WHERE m>'0'::money ORDER BY id", []int64{1}))
			run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 1))
		})
	})
}
