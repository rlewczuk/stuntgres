package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageSystemColumns(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		create := "CREATE TABLE ccu_system(a integer,CONSTRAINT ccu_system_check CHECK(tableoid=tableoid AND a>0))"
		held := c.hold(c.conn, ccuQuery)
		if c.b.PG {
			c.exec(create)
			identity := fixture.Select("SELECT attnum,attname,atttypid::bigint AS type_oid,attisdropped FROM pg_attribute WHERE attrelid='ccu_system'::regclass::oid AND attname='tableoid'", []string{"attnum", "attname", "type_oid", "attisdropped"}, []string{"INT2", "NAME", "INT8", "BOOL"}, [][]any{{int64(-6), "tableoid", int64(26), false}})
			c.run(c.conn, identity)
			c.run(c.conn, fixture.Prepared(identity))
			check := func(column string) {
				rows := [][]any{c.pub("ccu_system", column, "ccu_system_check"), c.pub("ccu_system", "tableoid", "ccu_system_check")}
				c.check(c.conn, "ccu_system", rows...)
				c.check(held, "ccu_system", rows...)
			}
			check("a")
			c.exec("ALTER TABLE ccu_system RENAME COLUMN a TO b")
			check("b")
			c.exec("ALTER TABLE ccu_system DROP CONSTRAINT ccu_system_check")
		} else {
			// The current native CHECK binding namespace exposes user columns
			// only. This feature reads admitted definitions; it does not add
			// tableoid admission or fabricate a constraint after failed CREATE.
			want := fixture.ControlsReferenceDiagnostic("42703", `column "tableoid" does not exist`)
			want.Position = fixture.Position(strings.Index(create, "tableoid") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(create, want)
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				c.run(c.conn, s)
				c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='ccu_system'", []string{"relname"}, []string{"NAME"}, nil))
				c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ccu_system_check'", []string{"conname"}, []string{"NAME"}, nil))
				c.check(c.conn, "ccu_system")
				c.check(held, "ccu_system")
			}
		}
		c.check(c.conn, "ccu_system")
		c.check(held, "ccu_system")

		c.exec("CREATE TABLE ccu_system_alter(a integer,CONSTRAINT ccu_system_existing CHECK(a>0))")
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_system_alter VALUES(1)", 1))
		baseline := [][]any{c.pub("ccu_system_alter", "a", "ccu_system_existing")}
		c.check(c.conn, "ccu_system_alter", baseline...)
		c.check(held, "ccu_system_alter", baseline...)
		alter := "ALTER TABLE ccu_system_alter ADD CONSTRAINT ccu_system_new CHECK(tableoid=tableoid AND a>0)"
		if c.b.PG {
			c.exec(alter)
			want := append(append([][]any(nil), baseline...), c.pub("ccu_system_alter", "a", "ccu_system_new"), c.pub("ccu_system_alter", "tableoid", "ccu_system_new"))
			c.check(c.conn, "ccu_system_alter", want...)
			c.check(held, "ccu_system_alter", want...)
			c.exec("ALTER TABLE ccu_system_alter DROP CONSTRAINT ccu_system_new")
		} else {
			// ALTER's existing CHECK cooking reports the semantic error without
			// the source position retained by CREATE; both fail at execution.
			want := fixture.ControlsReferenceDiagnostic("42703", `column "tableoid" does not exist`)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(alter, want)
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				c.run(c.conn, s)
				c.check(c.conn, "ccu_system_alter", baseline...)
				c.check(held, "ccu_system_alter", baseline...)
				c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='ccu_system_alter'::regclass::oid AND conname='ccu_system_new'", []string{"conname"}, []string{"NAME"}, nil))
				c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE oid='ccu_system_alter'::regclass::oid", []string{"relname"}, []string{"NAME"}, [][]any{{"ccu_system_alter"}}))
				c.run(c.conn, fixture.Select("SELECT a FROM ccu_system_alter", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		}
		c.check(c.conn, "ccu_system_alter", baseline...)
		c.check(held, "ccu_system_alter", baseline...)
		c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='ccu_system_alter'::regclass::oid ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ccu_system_existing"}}))
		c.run(c.conn, fixture.Select("SELECT a FROM ccu_system_alter", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
