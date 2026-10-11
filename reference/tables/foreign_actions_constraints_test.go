package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsConstraints(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, prepared := range []bool{false, true} {
			for _, kind := range []string{"not-null", "check", "unique", "exclusion", "other-fk"} {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", direction, kind, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
						action, extra := "SET DEFAULT", ""
						d := foreignKeyError("23514", `new row for relation "c" violates check constraint "ck"`, "Failing row contains (10, 1, kept).", "c", "ck")
						switch kind {
						case "not-null":
							action, extra = "SET NULL", " NOT NULL"
							d = foreignKeyError("23502", `null value in column "pid" of relation "c" violates not-null constraint`, "Failing row contains (10, null, kept).", "c", "")
							d.Column = "pid"
						case "check":
							extra = " CONSTRAINT ck CHECK(pid>1)"
						case "unique":
							extra = " CONSTRAINT uq UNIQUE"
							d = foreignKeyError("23505", `duplicate key value violates unique constraint "uq"`, "Key (pid)=(1) already exists.", "c", "uq")
						case "exclusion":
							d = foreignKeyError("23P01", `conflicting key value violates exclusion constraint "ex"`, "Key (pid)=(1) conflicts with existing key (pid)=(1).", "c", "ex")
						case "other-fk":
							run(fixture.Exec("CREATE TABLE other(id INT PRIMARY KEY)", 0))
							run(fixture.Exec("INSERT INTO other VALUES(2)", 1))
							extra = " CONSTRAINT other_fk REFERENCES other"
							d = foreignKeyChild("c", "other_fk", `Key (pid)=(1) is not present in table "other".`)
						}
						q := "CREATE TABLE c(id INT PRIMARY KEY,pid INT DEFAULT 1" + extra + " CONSTRAINT fk REFERENCES p ON " + direction + " " + action + ",note TEXT"
						if kind == "exclusion" {
							q += ",CONSTRAINT ex EXCLUDE USING btree(pid WITH =) DEFERRABLE INITIALLY IMMEDIATE"
						}
						run(fixture.Exec(q+")", 0))
						run(fixture.Exec("INSERT INTO c VALUES(10,2,'kept')", 1))
						if kind == "unique" || kind == "exclusion" {
							run(fixture.Exec("INSERT INTO c VALUES(11,1,'other')", 1))
						}
						q = "DELETE FROM p WHERE id=2"
						if direction == "UPDATE" {
							q = "UPDATE p SET id=3 WHERE id=2"
						}
						if kind != "exclusion" && kind != "other-fk" {
							d = foreignActionsContext(d, foreignActionsSQL("c", "pid", action, direction))
						}
						run(foreignKeyFail(q, d, prepared))
						returning := q + " RETURNING id"
						s := foreignKeyFail(returning, d, prepared)
						s.SelectRows = true
						run(s)
						run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
						rows := [][]any{{int64(10), int64(2), "kept"}}
						if kind == "unique" || kind == "exclusion" {
							rows = append(rows, []any{int64(11), int64(1), "other"})
						}
						run(fixture.Select("SELECT id,pid,note FROM c ORDER BY id", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, rows))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsConstraintsFull(t *testing.T) {
	for _, action := range []string{"SET NULL", "SET DEFAULT"} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
			run(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH FULL ON DELETE "+action+" (b))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(7,2)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(7,2)", 1))
			d := foreignKeyChild("c", "fk", "MATCH FULL does not allow mixing of null and nonnull key values.")
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail("DELETE FROM p", d, prepared))
			}
			run(fixture.Select("SELECT a,b FROM p", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}}))
			run(fixture.Select("SELECT a,b FROM c", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}}))
		})
	}
}

func TestReferenceForeignActionsConstraintsConversion(t *testing.T) {
	for _, generated := range []bool{false, true} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			parentType := "BIGINT"
			extra := ""
			next := "2147483648"
			if generated {
				parentType = "INT"
				extra = ",doubled INT GENERATED ALWAYS AS (pid*2) STORED"
				next = "1073741824"
			}
			run(fixture.Exec("CREATE TABLE p(id "+parentType+" PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON UPDATE CASCADE"+extra+")", 0))
			run(fixture.Exec("INSERT INTO p VALUES(2)", 1))
			run(fixture.Exec("INSERT INTO c(pid) VALUES(2)", 1))
			if generated {
				run(fixture.Exec("UPDATE p SET id=3", 1))
				run(fixture.Select("SELECT pid,doubled FROM c", []string{"pid", "doubled"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(6)}}))
			}
			d := foreignActionsContext(foreignKeyError("22003", "integer out of range", "", "", ""), foreignActionsSQL("c", "pid", "CASCADE", "UPDATE"))
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail("UPDATE p SET id="+next, d, prepared))
			}
			old := int64(2)
			if generated {
				old = 3
			}
			run(foreignKeyIntRows("SELECT pid FROM c", "pid", old))
			s := fixture.Select("SELECT id FROM p", []string{"id"}, []string{"INT8"}, [][]any{{old}})
			if generated {
				s.Types = []string{"INT4"}
			}
			run(s)
		})
	}
}

func TestReferenceForeignActionsConstraintsDomain(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		q := "CREATE DOMAIN positive AS INT CONSTRAINT positive_check CHECK(VALUE>0)"
		if !b.PG {
			d := foreignKeyError("0A000", "domain constraints and defaults are unsupported", "", "", "")
			d.Position = "57"
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail(q, d, prepared))
			}
		} else {
			run(fixture.Exec(q, 0))
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid positive CONSTRAINT fk REFERENCES p ON UPDATE CASCADE)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			d := foreignKeyError("23514", `value for domain positive violates check constraint "positive_check"`, "", "", "positive_check")
			d.Schema, d.DataType = "public", "positive"
			d = foreignActionsContext(d, foreignActionsSQL("c", "pid", "CASCADE", "UPDATE")+"::pg_catalog.int4")
			for _, prepared := range []bool{false, true} {
				run(foreignKeyFail("UPDATE p SET id=-1", d, prepared))
			}
			run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
			run(foreignKeyIntRows("SELECT pid::int AS pid FROM c", "pid", 1))
		}
		// Unconstrained domains are a real shared assignment provider. A table
		// CHECK independently validates the action-produced domain value.
		run(fixture.Exec("CREATE DOMAIN action_integer AS INT", 0))
		run(fixture.Exec("CREATE TABLE domain_parent(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE domain_child(pid action_integer CONSTRAINT action_check CHECK(pid>0) CONSTRAINT domain_fk REFERENCES domain_parent ON UPDATE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO domain_parent VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO domain_child VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE domain_parent SET id=2", 1)))
		run(foreignKeyIntRows("SELECT pid::int AS pid FROM domain_child", "pid", 2))
		local := foreignKeyError("23514", `new row for relation "domain_child" violates check constraint "action_check"`, "Failing row contains (-1).", "domain_child", "action_check")
		local = foreignActionsContext(local, `UPDATE ONLY "public"."domain_child" SET "pid" = $1 WHERE $2 OPERATOR(pg_catalog.=) "pid"::pg_catalog.int4`)
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("UPDATE domain_parent SET id=-1", local, prepared))
		}
		run(foreignKeyIntRows("SELECT id FROM domain_parent", "id", 2))
		run(foreignKeyIntRows("SELECT pid::int AS pid FROM domain_child", "pid", 2))
	})
}
