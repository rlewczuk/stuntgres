package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The released btree and hash adjustment routines start with the creating class,
// not a family/input lookup that can find an older incomplete class. Hash support
// 2 is optional (unlike support 1); both methods leave cross-type operators loose.
func TestReferenceOperatorClassMemberOwnership(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			schema := "class_member_" + method
			fixture.SchemaReferenceCleanup(t, schema)
			var emitted string
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, run := classConn(t, p, b)
					family := schema + ".members"
					oldClass := schema + ".older"
					newClass := schema + ".creating"
					table := schema + ".rows"
					index := "class_member_" + method + "_index"
					items := classItems
					loose := `OPERATOR 1 pg_catalog.< (integer,bigint), FUNCTION 1 pg_catalog.btint48cmp(integer,bigint), FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`
					strategies := 5
					if method == "hash" {
						items = `OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)`
						loose = `OPERATOR 1 pg_catalog.= (integer,bigint), FUNCTION 2 (integer,integer) pg_catalog.hashint4extended(integer,bigint)`
						strategies = 1
					}
					for _, q := range []string{
						`CREATE SCHEMA ` + schema,
						`CREATE OPERATOR FAMILY ` + family + ` USING ` + method,
						`ALTER OPERATOR FAMILY ` + family + ` USING ` + method + ` ADD ` + loose,
						`CREATE OPERATOR CLASS ` + oldClass + ` FOR TYPE integer USING ` + method + ` FAMILY ` + family + ` AS STORAGE integer`,
						`CREATE OPERATOR CLASS ` + newClass + ` FOR TYPE integer USING ` + method + ` FAMILY ` + family + ` AS ` + items,
						`CREATE TABLE ` + table + `(id integer,n integer)`,
					} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec(`INSERT INTO `+table+` VALUES(1,2),(2,1),(3,NULL),(4,2)`, 4))
					run(fixture.Prepared(fixture.Exec(`CREATE INDEX `+index+` ON `+table+` USING `+method+` (n `+newClass+`)`, 0)))
					run(fixture.Exec(`INSERT INTO `+table+` VALUES(5,3)`, 1))

					// Rediscover destination identities after Restore/replay rather than
					// assuming native and PostgreSQL allocate the same OIDs.
					familyID := func() int64 {
						return classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='members' AND opfnamespace=$1::regnamespace`, schema)
					}
					classID := func(name string) int64 {
						return classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_opclass WHERE opcname=$1 AND opcnamespace=$2::regnamespace`, name, schema)
					}
					checkMembers := func(alive bool) {
						t.Helper()
						f := familyID()
						var owner int64
						if alive {
							owner = classID("creating")
						}
						operators := [][]any{{int64(1), int64(23), int64(20)}}
						if alive {
							for n := 1; n <= strategies; n++ {
								operators = append(operators, []any{int64(n), int64(23), int64(23)})
							}
						}
						classRoutes(t, p, b, conn, fixture.Select(`SELECT amopstrategy,amoplefttype::bigint,amoprighttype::bigint FROM pg_amop WHERE amopfamily=$1::oid ORDER BY amoprighttype,amopstrategy`, []string{"amopstrategy", "amoplefttype", "amoprighttype"}, []string{"INT2", "INT8", "INT8"}, operators, f))
						var support [][]any
						if method == "btree" {
							support = append(support, []any{int64(1), int64(23), int64(20)})
						}
						if alive {
							support = append(support, []any{int64(1), int64(23), int64(23)})
						}
						support = append(support, []any{int64(2), int64(23), int64(23)})
						classRoutes(t, p, b, conn, fixture.Select(`SELECT amprocnum,amproclefttype::bigint,amprocrighttype::bigint FROM pg_amproc WHERE amprocfamily=$1::oid ORDER BY amprocnum,amprocrighttype`, []string{"amprocnum", "amproclefttype", "amprocrighttype"}, []string{"INT2", "INT8", "INT8"}, support, f))
						for _, member := range []struct {
							catalog int64
							query   string
							number  string
						}{
							{2602, `SELECT a.amopstrategy,d.refclassid::bigint,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_amop a JOIN pg_depend d ON d.classid=2602 AND d.objid=a.oid WHERE a.amopfamily=$1::oid AND d.deptype='i' ORDER BY a.amopstrategy`, "operator"},
							{2603, `SELECT a.amprocnum,d.refclassid::bigint,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_amproc a JOIN pg_depend d ON d.classid=2603 AND d.objid=a.oid WHERE a.amprocfamily=$1::oid AND d.deptype='i' ORDER BY a.amprocnum`, "support"},
						} {
							var want [][]any
							if alive {
								count := 1
								if member.catalog == 2602 {
									count = strategies
								}
								for n := 1; n <= count; n++ {
									want = append(want, []any{int64(n), int64(2616), owner, int64(0), "i"})
								}
							}
							column := "amopstrategy"
							if member.number == "support" {
								column = "amprocnum"
							}
							classRoutes(t, p, b, conn, fixture.Select(member.query, []string{column, "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT2", "INT8", "INT8", "INT4", "CHAR"}, want, f))
						}
						var indexes [][]any
						if alive {
							indexes = [][]any{{index}}
						}
						run(fixture.Select(`SELECT relname FROM pg_class WHERE relname=$1 AND relnamespace=$2::regnamespace`, []string{"relname"}, []string{"NAME"}, indexes, index, schema))
						if alive {
							relation := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_class WHERE relname=$1 AND relnamespace=$2::regnamespace`, index, schema)
							run(fixture.Select(`SELECT refobjid::bigint,deptype FROM pg_depend WHERE classid=1259 AND objid=$1::oid AND refclassid=2616`, []string{"refobjid", "deptype"}, []string{"INT8", "CHAR"}, [][]any{{owner, "n"}}, relation))
						}
					}
					data := func() {
						classRoutes(t, p, b, conn, fixture.Select(`SELECT id,n FROM `+table+` ORDER BY n NULLS LAST,id`, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {int64(4), int64(2)}, {int64(5), int64(3)}, {int64(3), nil}}))
					}
					checkMembers(true)
					data()
					// Explicit native-only replacement phase; the following PostgreSQL
					// phase executes these actual emitted bytes, never reconstructed SQL.
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						emitted = dump.String()
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
							t.Fatal("ownership Restore", err)
						}
					} else if emitted != "" {
						run(fixture.Exec(`DROP SCHEMA `+schema+` CASCADE`, 0))
						if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
							t.Fatal("PostgreSQL replay of native ownership dump", err)
						}
					}
					checkMembers(true)
					if classID("older") == classID("creating") {
						t.Fatal("distinct classes share an identity")
					}
					run(fixture.Exec(`DROP OPERATOR CLASS `+oldClass+` USING `+method, 0))
					run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='older' AND opcnamespace=$1::regnamespace`, []string{"opcname"}, []string{"NAME"}, nil, schema))
					checkMembers(true)
					data()
					// A maintained insert after replacement and older-class retirement
					// must still execute the surviving index's required support.
					run(fixture.Exec(`INSERT INTO `+table+` VALUES(6,4)`, 1))
					run(fixture.Select(`SELECT id FROM `+table+` WHERE n=4`, []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(`DROP OPERATOR CLASS `+newClass+` USING `+method+` RESTRICT`, fixture.Diagnostic{
							Code:    "2BP01",
							Message: fmt.Sprintf("cannot drop operator class %s for access method %s because other objects depend on it", newClass, method),
							Detail:  fmt.Sprintf("index %s.%s depends on operator class %s for access method %s", schema, index, newClass, method),
							Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
						})
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						checkMembers(true)
						run(fixture.Select(`SELECT count(*) FROM `+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}))
					}
					run(fixture.Exec(`DROP OPERATOR CLASS `+newClass+` USING `+method+` CASCADE`, 0))
					checkMembers(false)
					run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcnamespace=$1::regnamespace`, []string{"opcname"}, []string{"NAME"}, nil, schema))
					run(fixture.Select(`SELECT id,n FROM `+table+` ORDER BY id`, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}, {int64(4), int64(2)}, {int64(5), int64(3)}, {int64(6), int64(4)}}))
				})
			}
		})
	}
}
