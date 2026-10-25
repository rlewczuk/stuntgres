package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignKeyClauseStructure(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE clause_parent(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO clause_parent VALUES(1)", 1))
		for _, tc := range []struct {
			query, token string
		}{
			{"CREATE TABLE clause_bad(pid INT REFERENCES clause_parent MATCH FULL MATCH SIMPLE)", "MATCH"},
			{"CREATE TABLE clause_bad(pid INT REFERENCES clause_parent ON DELETE NO ACTION ON DELETE NO ACTION)", "DELETE"},
			{"CREATE TABLE clause_bad(pid INT REFERENCES clause_parent ON UPDATE NO ACTION ON UPDATE NO ACTION)", "UPDATE"},
			{"CREATE TABLE clause_bad(pid INT REFERENCES clause_parent ON DELETE NO ACTION MATCH FULL)", "MATCH"},
			{"CREATE TABLE clause_bad(pid INT REFERENCES clause_parent ON UPDATE NO ACTION MATCH SIMPLE)", "MATCH"},
			{"CREATE TABLE clause_bad(pid INT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES clause_parent MATCH SIMPLE MATCH FULL)", "MATCH"},
			{"CREATE TABLE clause_bad(pid INT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES clause_parent ON UPDATE NO ACTION ON DELETE NO ACTION ON UPDATE NO ACTION)", "ON"},
			{"CREATE TABLE clause_bad(pid INT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES clause_parent NOT DEFERRABLE MATCH FULL)", "MATCH"},
		} {
			d := fixture.ForeignKeyError("42601", `syntax error at or near "`+tc.token+`"`, "", "", "")
			start := strings.LastIndex(tc.query, " "+tc.token+" ") + 1
			d.Position = fixture.Position(utf8.RuneCountInString(tc.query[:start]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ForeignKeyFail(tc.query, d, prepared)
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='clause_bad'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT f.conname FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='clause_bad'", []string{"conname"}, []string{"NAME"}, nil))
				run(foreignKeyIntRows("SELECT id FROM clause_parent", "id", 1))
			}
		}
		for _, tc := range []struct {
			name, clauses string
		}{
			{"update_first", "ON UPDATE NO ACTION ON DELETE NO ACTION NOT DEFERRABLE INITIALLY IMMEDIATE"},
			{"delete_first", "ON DELETE NO ACTION ON UPDATE NO ACTION INITIALLY IMMEDIATE NOT DEFERRABLE"},
			{"repeated_attributes", "MATCH FULL ON DELETE NO ACTION ON UPDATE NO ACTION NOT DEFERRABLE NOT DEFERRABLE INITIALLY IMMEDIATE INITIALLY IMMEDIATE"},
		} {
			name := "clause_" + tc.name
			constraint := name + "_fk"
			run(fixture.Exec("CREATE TABLE "+name+"(pid INT,CONSTRAINT "+constraint+" FOREIGN KEY(pid) REFERENCES clause_parent "+tc.clauses+")", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1)
				s.Prepared = prepared
				run(s)
				run(fixture.ForeignKeyFail("INSERT INTO "+name+" VALUES(9)", fixture.ForeignKeyChild(name, constraint, `Key (pid)=(9) is not present in table "clause_parent".`), prepared))
			}
			run(foreignKeyIntRows("SELECT pid FROM "+name, "pid", 1, 1))
			definition := "FOREIGN KEY (pid) REFERENCES clause_parent(id)"
			if strings.Contains(tc.clauses, "MATCH FULL") {
				definition += " MATCH FULL"
			}
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{definition}}, constraint))
		}
	})
}

func TestReferenceForeignKeyNotNullNameCollision(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE collision_parent(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE collision_child(pid INT CONSTRAINT collision_fk REFERENCES collision_parent,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO collision_parent VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO collision_child VALUES(1,'first')", 1))
		var original int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='collision_fk'").Scan(&original); err != nil || original <= 0 {
			t.Fatal(original, err)
		}
		for _, prepared := range []bool{false, true} {
			run(fixture.ForeignKeyFail("ALTER TABLE collision_child ADD CONSTRAINT collision_fk NOT NULL pid", fixture.ForeignKeyError("42710", `constraint "collision_fk" for relation "collision_child" already exists`, "", "", ""), prepared))
			var current int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='collision_fk'").Scan(&current); err != nil || current != original {
				t.Fatal("name collision changed FK identity", original, current, err)
			}
			run(fixture.Select("SELECT f.conname,f.contype FROM pg_constraint f JOIN pg_class r ON f.conrelid=r.oid WHERE r.relname='collision_child' ORDER BY f.conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"collision_fk", "f"}}))
			run(fixture.Select("SELECT pid,note FROM collision_child", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "first"}}))
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO collision_child VALUES(NULL,'null')", 1)))
		run(fixture.Prepared(fixture.Exec("INSERT INTO collision_child VALUES(1,'recovery')", 1)))
		run(fixture.ForeignKeyFail("INSERT INTO collision_child VALUES(9,'missing')", fixture.ForeignKeyChild("collision_child", "collision_fk", `Key (pid)=(9) is not present in table "collision_parent".`), true))
		run(fixture.Select("SELECT pid,note FROM collision_child ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "first"}, {nil, "null"}, {int64(1), "recovery"}}))
		run(foreignKeyIntRows("SELECT id FROM collision_parent", "id", 1))
	})
}
