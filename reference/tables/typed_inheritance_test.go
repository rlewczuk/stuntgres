package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableInheritedPropagation(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note text)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE left_child(local boolean) INHERITS(t)", 0))
		run(fixture.Exec("CREATE TABLE right_child() INHERITS(t)", 0))
		run(fixture.Exec("CREATE TABLE diamond() INHERITS(left_child,right_child)", 0))
		run(fixture.Exec("INSERT INTO diamond VALUES(11,'hey',true)", 1))
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE flag CASCADE", 0))
		for _, table := range []string{"t", "left_child", "right_child", "diamond"} {
			names, types := []string{"id", "label"}, []string{"INT8", "TEXT"}
			if table == "left_child" || table == "diamond" {
				names = append(names, "local")
				types = append(types, "BOOL")
			}
			var expected [][]any
			if table == "diamond" {
				expected = [][]any{{int64(11), "hey", true}}
			}
			if b.PG {
				run(fixture.Select("SELECT * FROM ONLY "+table, names, types, expected))
			} else if table == "diamond" {
				run(fixture.Select("SELECT * FROM diamond", names, types, expected))
			} else {
				// Parent scans remain unsupported even when the query asks for no rows.
				fixture.CompositeReferenceError(t, p, c, "SELECT * FROM "+table+" LIMIT 0", "prepare", typedTableError("0A000", "recursive table inheritance is unsupported"))
			}
			if table == "t" {
				run(typedTableAttachment(table, "pair", true))
			}
			if table == "diamond" {
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM diamond", names, types, makeNullable(len(names)), 1)
			}
			run(fixture.Select("SELECT '"+table+"'::regtype::oid<>'pair'::regtype::oid AS independent_row", []string{"independent_row"}, []string{"BOOL"}, [][]any{{true}}))
		}
	})
}

func makeNullable(n int) []bool {
	result := make([]bool, n)
	for i := range result {
		result[i] = true
	}
	return result
}

func TestReferenceTypedTableInheritedLocalRetention(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE child(id integer,flag boolean) INHERITS(t)", 0))
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE flag CASCADE", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM child LIMIT 0", []string{"id", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, 0)
		run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE id CASCADE", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM child LIMIT 0", []string{"id", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, 0)
	})
}

func TestReferenceTypedTableInheritedCollisionAtomicity(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE child(flag text) INHERITS(t)", 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", "execute", typedTableError("42804", `child table "child" has different type for column "flag"`))
		if b.PG {
			fixture.TableDDLMetadata(t, p.Ctx, c, true, prepared, "SELECT * FROM ONLY t LIMIT 0", []string{"id"}, []string{"INT4"}, []bool{true}, 0)
		} else {
			fixture.CompositeReferenceError(t, p, c, "SELECT * FROM t LIMIT 0", "prepare", typedTableError("0A000", "recursive table inheritance is unsupported"))
		}
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM child LIMIT 0", []string{"id", "flag"}, []string{"INT4", "TEXT"}, []bool{true, true}, 0)
		run(typedTableAttachment("t", "pair", true))
	})
}
