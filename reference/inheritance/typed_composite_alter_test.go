package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableCompositeAlterConversions(t *testing.T) {
	for _, mode := range []string{"widen", "incompatible_empty", "incompatible_null", "overflow"} {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(mode+"_"+name, func(t *testing.T) {
				fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE a OF pair (id WITH OPTIONS DEFAULT 7, CONSTRAINT positive CHECK(id>0))`, `CREATE TABLE z OF pair`, `CREATE TABLE child() INHERITS(a)`, `CREATE TABLE detached OF pair`, `ALTER TABLE detached NOT OF`, `CREATE TABLE copied(LIKE a INCLUDING DEFAULTS)`)
					if mode == "incompatible_null" {
						run(fixture.Exec(`INSERT INTO a VALUES(NULL,NULL)`, 1))
						run(fixture.Exec(`INSERT INTO z VALUES(NULL,NULL)`, 1))
					}
					if mode == "widen" || mode == "overflow" {
						run(fixture.Exec(`INSERT INTO a(note) VALUES('x')`, 1))
						run(fixture.Exec(`INSERT INTO child VALUES(11,'y')`, 1))
						last := int64(11)
						if mode == "overflow" {
							last = 40000
						}
						run(fixture.Exec(`INSERT INTO z VALUES($1,'z')`, 1, last))
					}
					q := `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE`
					if mode == "widen" {
						run(fixture.Exec(q, 0))
					} else {
						d := typedTableError("42804", `column "note" cannot be cast automatically to type integer`)
						d.Hint = `You might need to specify "USING note::integer".`
						q = `ALTER TYPE pair ALTER ATTRIBUTE note TYPE integer CASCADE`
						if mode == "overflow" {
							q = `ALTER TYPE pair ALTER ATTRIBUTE id TYPE smallint CASCADE`
							d = typedTableError("22003", "smallint out of range")
						}
						fixture.CompositeReferenceError(t, p, c, q, "execute", d)
					}
					typeName := "INT4"
					if mode == "widen" {
						typeName = "INT8"
					}
					rows := [][]any{}
					child := [][]any{}
					last := [][]any{}
					if mode == "widen" || mode == "overflow" {
						rows = [][]any{{int64(7), "x"}, {int64(11), "y"}}
						child = [][]any{{int64(11), "y"}}
						n := int64(11)
						if mode == "overflow" {
							n = 40000
						}
						last = [][]any{{n, "z"}}
					}
					if mode == "incompatible_null" {
						rows = [][]any{{nil, nil}}
						last = [][]any{{nil, nil}}
					}
					for _, tc := range []struct {
						table string
						rows  [][]any
					}{{"a", rows}, {"child", child}, {"z", last}} {
						run(fixture.Select("SELECT * FROM "+tc.table+" ORDER BY id", []string{"id", "note"}, []string{typeName, "TEXT"}, tc.rows))
					}
					for _, table := range []string{"detached", "copied"} {
						run(fixture.Select("SELECT * FROM "+table, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{}))
					}
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE probe OF pair`)
					fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, `SELECT * FROM probe`, []string{"id", "note"}, []string{typeName, "TEXT"}, []bool{true, true}, 0)
					if mode == "widen" || mode == "overflow" {
						run(fixture.Exec(`INSERT INTO a(note) VALUES('default')`, 1))
						d := typedTableError("23514", `new row for relation "a" violates check constraint "positive"`)
						d.Detail = "Failing row contains (-1, bad)."
						d.Schema = "public"
						d.Table = "a"
						d.Constraint = "positive"
						fixture.CompositeReferenceError(t, p, c, `INSERT INTO a VALUES(-1,'bad')`, "execute", d)
					}
				})
			})
		}
	}
}

func TestReferenceTypedTableCompositeAlterModes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE a OF pair`, `CREATE TABLE z OF pair`, `CREATE TABLE left_child() INHERITS(a)`, `CREATE TABLE right_child() INHERITS(a)`, `CREATE TABLE diamond() INHERITS(left_child,right_child)`)
				run(fixture.Exec(`INSERT INTO diamond VALUES(7,'x')`, 1))
				run(fixture.Exec(`INSERT INTO z VALUES(11,'y')`, 1))
				for _, action := range []string{`ADD ATTRIBUTE extra text`, `DROP ATTRIBUTE note`, `RENAME ATTRIBUTE note TO label`, `ALTER ATTRIBUTE id TYPE bigint`} {
					for _, mode := range []string{"", " RESTRICT"} {
						fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair "+action+mode, "execute", typedTableRestriction("pair"))
					}
				}
				fixture.CompositeReferenceError(t, p, c, `ALTER TYPE pair ADD ATTRIBUTE extra text CASCADE, DROP ATTRIBUTE note RESTRICT`, "execute", typedTableRestriction("pair"))
				for _, table := range []string{"a", "diamond"} {
					run(fixture.Select("SELECT * FROM "+table, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}}))
				}
				run(fixture.Exec(`ALTER TYPE pair ADD ATTRIBUTE extra text CASCADE`, 0))
				run(fixture.Exec(`ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE`, 0))
				run(fixture.Exec(`ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE`, 0))
				run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE label CASCADE`, 0))
				for _, table := range []string{"a", "left_child", "right_child", "diamond", "z"} {
					id := int64(7)
					if table == "z" {
						id = 11
					}
					run(fixture.Select("SELECT * FROM "+table, []string{"id", "extra"}, []string{"INT8", "TEXT"}, [][]any{{id, nil}}))
				}
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE own_holder(v z)`)
				fixture.CompositeReferenceError(t, p, c, `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE`, "execute", typedTableError("0A000", `cannot alter table "z" because column "own_holder.v" uses its row type`))
			})
		})
	}
}
