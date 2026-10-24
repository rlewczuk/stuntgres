package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionDependencies(t *testing.T) {
	for _, alias := range []bool{false, true} {
		for _, mode := range []string{"autocommit", "sql", "go"} {
			t.Run(fmt.Sprintf("alias_%t/%s", alias, mode), func(t *testing.T) {
				definitionScenario(t, []string{"tx_dep_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := definitionRun(t, p, b, c)
					method := "btree"
					if alias {
						run(fixture.Exec(`CREATE ACCESS METHOD tx_dep_am TYPE INDEX HANDLER bthandler`, 0))
						method = "tx_dep_am"
					}
					r := definitionRoute{mode: mode, commit: true}
					e, end := r.begin(t, p, b, c)
					execute := definitionRun(t, p, b, e)
					execute(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_dep_family USING `+method, 0))
					execute(fixture.Exec(`ALTER OPERATOR FAMILY public.tx_dep_family USING `+method+` ADD OPERATOR 1 pg_catalog.<(integer,bigint), FUNCTION 1 pg_catalog.btint48cmp(integer,bigint), FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, 0))
					execute(fixture.Exec(`CREATE OPERATOR CLASS public.tx_dep_class FOR TYPE integer USING `+method+` FAMILY public.tx_dep_family AS `+definitionClassItems, 0))
					family := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_dep_family'`)
					class := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_dep_class'`)
					execute(fixture.Select(`SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=2616 AND objid=$1::oid AND refclassid=2753`, []string{"refclassid", "refobjid", "deptype"}, []string{"INT8", "INT8", "CHAR"}, [][]any{{int64(2753), family, "a"}}, class))
					for _, member := range []struct {
						table                        string
						catalog                      int64
						familyCol, slot, left, right string
						count                        int
					}{
						{"pg_amop", 2602, "amopfamily", "amopstrategy", "amoplefttype", "amoprighttype", 5},
						{"pg_amproc", 2603, "amprocfamily", "amprocnum", "amproclefttype", "amprocrighttype", 1},
					} {
						var owned [][]any
						for i := 1; i <= member.count; i++ {
							owned = append(owned, []any{int64(i), int64(2616), class, "i"})
						}
						execute(fixture.Select(fmt.Sprintf(`SELECT a.%s,d.refclassid::bigint,d.refobjid::bigint,d.deptype FROM %s a JOIN pg_depend d ON d.classid=%d AND d.objid=a.oid WHERE a.%s=$1::oid AND d.deptype='i' ORDER BY a.%s`, member.slot, member.table, member.catalog, member.familyCol, member.slot), []string{member.slot, "refclassid", "refobjid", "deptype"}, []string{"INT2", "INT8", "INT8", "CHAR"}, owned, family))
					}
					// Optional and cross-type members never acquire an INTERNAL class owner.
					execute(definitionCount(`SELECT count(*) FROM pg_depend d JOIN pg_amproc a ON d.classid=2603 AND d.objid=a.oid WHERE a.amprocfamily=$1::oid AND (a.amprocnum=2 OR a.amprocrighttype=20) AND d.deptype='i'`, 0, family))
					if alias {
						methodID := definitionID(t, p, e, `SELECT oid::bigint FROM pg_am WHERE amname='tx_dep_am'`)
						execute(fixture.Select(`SELECT deptype FROM pg_depend WHERE classid=2753 AND objid=$1::oid AND refclassid=2601 AND refobjid=$2::oid`, []string{"deptype"}, []string{"CHAR"}, [][]any{{"a"}}, family, methodID))
					}
					end()
					// Required CREATE members cannot be removed independently, even CASCADE is
					// not an authorization to detach an INTERNAL object from its class.
					q := `ALTER OPERATOR FAMILY public.tx_dep_family USING ` + method + ` DROP FUNCTION 1(integer,integer)`
					want := fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 1 (integer, integer) of operator family tx_dep_family for access method ` + method + `: btint4cmp(integer,integer) because operator class tx_dep_class for access method ` + method + ` requires it`, Hint: `You can drop operator class tx_dep_class for access method ` + method + ` instead.`}
					for _, held := range []bool{false, true} {
						s := fixture.ImplicitError(q, want)
						s.ErrorStage = "execute"
						if held {
							fixture.RunStep(t, p.Ctx, definitionHeld(t, p, c, q), b.Recorder, s)
						} else {
							run(s)
						}
					}
					run(fixture.Exec(`ALTER OPERATOR FAMILY public.tx_dep_family USING `+method+` DROP FUNCTION 2(integer,integer), OPERATOR 1(integer,bigint)`, 0))
					run(fixture.Exec(`DROP OPERATOR CLASS public.tx_dep_class USING `+method+` RESTRICT`, 0))
					run(definitionCount(`SELECT count(*) FROM pg_amop WHERE amopfamily=$1::oid`, 0, family))
					run(fixture.Select(`SELECT amprocnum,amproclefttype::bigint,amprocrighttype::bigint,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=$1::oid`, []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(23), int64(20), int64(2188)}}, family))
					// ALTER ADD fallback searches physical btree's class, not a SQL alias.
					run(fixture.Exec(`CREATE OPERATOR CLASS public.tx_dep_incomplete FOR TYPE integer USING `+method+` FAMILY public.tx_dep_family AS STORAGE integer`, 0))
					run(fixture.Exec(`ALTER OPERATOR FAMILY public.tx_dep_family USING `+method+` ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
					internal := int64(1)
					if alias {
						internal = 0
					}
					run(definitionCount(`SELECT count(*) FROM pg_depend d JOIN pg_amproc a ON d.classid=2603 AND d.objid=a.oid WHERE a.amprocfamily=$1::oid AND a.amprocrighttype=23 AND d.deptype='i'`, internal, family))
					run(fixture.Exec(`DROP OPERATOR FAMILY public.tx_dep_family USING `+method+` RESTRICT`, 0))
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_dep_class','tx_dep_incomplete')`, 0))
				})
			})
		}
	}
}
