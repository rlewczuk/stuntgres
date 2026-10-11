package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// foreignActionsScenario retains the incumbent fixture and recording conventions.
func foreignActionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			body(t, p, b, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
		})
	}
}

func foreignActionsRestrict(parent, child, constraint, detail string) fixture.Diagnostic {
	return foreignKeyError("23001", `update or delete on table "`+parent+`" violates RESTRICT setting of foreign key constraint "`+constraint+`" on table "`+child+`"`, detail, child, constraint)
}

// These queries are independently derived from REL_18_0 ri_triggers.c's
// RI_FKey_cascade_{del,upd}, ri_set and ri_GenerateQual, not native observations.
func foreignActionsSQL(child, column, action, direction string) string {
	qual := ` WHERE $1 OPERATOR(pg_catalog.=) "` + column + `"`
	if action == "CASCADE" && direction == "DELETE" {
		return `DELETE FROM ONLY "public"."` + child + `"` + qual
	}
	value := action
	if action == "SET NULL" {
		value = "NULL"
	}
	if action == "SET DEFAULT" {
		value = "DEFAULT"
	}
	if action == "CASCADE" {
		value = "$1"
		qual = ` WHERE $2 OPERATOR(pg_catalog.=) "` + column + `"`
	}
	return `UPDATE ONLY "public"."` + child + `" SET "` + column + `" = ` + value + qual
}

func foreignActionsContext(d fixture.Diagnostic, queries ...string) fixture.Diagnostic {
	for _, q := range queries {
		if d.Context != "" {
			d.Context += "\n"
		}
		d.Context += `SQL statement "` + q + `"`
	}
	return d
}

func foreignActionsExec(q string, count int64, prepared bool, args ...any) fixture.Step {
	s := fixture.Exec(q, count, args...)
	s.Prepared = prepared
	return s
}

func TestReferenceForeignActionsDefinition(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", action, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
					for _, q := range []string{
						"CREATE TABLE c(a INT,b TEXT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH SIMPLE ON UPDATE " + action + " ON DELETE " + action + " NOT DEFERRABLE)",
						"CREATE TABLE reversed(b TEXT,a INT,CONSTRAINT reversed_fk FOREIGN KEY(b,a) REFERENCES p(b,a) ON DELETE " + action + " ON UPDATE " + action + ")",
					} {
						run(foreignActionsExec(q, 0, prepared))
					}
					def := "FOREIGN KEY (a, b) REFERENCES p(a, b)"
					if action != "NO ACTION" {
						def += " ON UPDATE " + action + " ON DELETE " + action
					}
					run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{def}}))
					run(fixture.Exec("INSERT INTO p VALUES(1,'x')", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1,'x')", 1))
					run(fixture.Exec("INSERT INTO reversed VALUES('x',1)", 1))
				})
			})
		}
	}
}

func TestReferenceForeignActionsDefinitionLists(t *testing.T) {
	for _, action := range []string{"SET NULL", "SET DEFAULT"} {
		for _, list := range []string{"author_id", "author_id,author_id", "author_id,tenant_id,author_id"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", action, list, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(tenant_id INT,author_id INT,PRIMARY KEY(tenant_id,author_id))", 0))
						run(foreignActionsExec("CREATE TABLE c(tenant_id INT DEFAULT 8,author_id INT DEFAULT 1,note TEXT,CONSTRAINT fk FOREIGN KEY(tenant_id,author_id) REFERENCES p ON DELETE "+action+" ("+list+"))", 0, prepared))
						canonical := "author_id"
						both := strings.Contains(list, "tenant_id")
						if both {
							canonical += ", tenant_id"
						}
						run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (tenant_id, author_id) REFERENCES p(tenant_id, author_id) ON DELETE " + action + " (" + canonical + ")"}}))
						run(fixture.Exec("INSERT INTO p VALUES(7,2),(8,2),(7,1),(8,1)", 4))
						run(fixture.Exec("INSERT INTO c VALUES(7,2,'affected'),(8,2,'untouched')", 2))
						run(foreignActionsExec("DELETE FROM p WHERE tenant_id=7 AND author_id=2", 1, prepared))
						var tenant, author any = int64(7), nil
						if both {
							tenant = nil
						}
						if action == "SET DEFAULT" {
							author = int64(1)
							if both {
								tenant = int64(8)
							}
						}
						run(fixture.Select("SELECT tenant_id,author_id,note FROM c ORDER BY note", []string{"tenant_id", "author_id", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{tenant, author, "affected"}, {int64(8), int64(2), "untouched"}}))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsDefinitionDiagnostics(t *testing.T) {
	cases := []struct{ suffix, code, message, token string }{
		{"ON DELETE SET NULL (missing)", "42703", `column "missing" referenced in foreign key constraint does not exist`, ""},
		{"ON DELETE SET DEFAULT (note)", "42P10", `column "note" referenced in ON DELETE SET action must be part of foreign key`, ""},
		{"ON UPDATE SET NULL (pid)", "0A000", "a column list with SET NULL is only supported for ON DELETE actions", "ON UPDATE"},
		{"ON UPDATE SET DEFAULT (pid)", "0A000", "a column list with SET DEFAULT is only supported for ON DELETE actions", "ON UPDATE"},
		{"ON DELETE SET NULL ()", "42601", `syntax error at or near ")"`, ")"},
		{"ON DELETE CASCADE (pid)", "42601", `syntax error at or near "("`, "(pid)"},
		{"ON DELETE RESTRICT (pid)", "42601", `syntax error at or near "("`, "(pid)"},
		{"ON DELETE NO ACTION (pid)", "42601", `syntax error at or near "("`, "(pid)"},
		{"ON DELETE CASCADE ON DELETE SET NULL", "42601", `syntax error at or near "DELETE"`, "DELETE SET"},
		{"ON UPDATE CASCADE ON UPDATE RESTRICT", "42601", `syntax error at or near "UPDATE"`, "UPDATE RESTRICT"},
		{"ON DELETE CASCADE MATCH FULL", "42601", `syntax error at or near "MATCH"`, "MATCH"},
		{"NOT DEFERRABLE ON DELETE CASCADE", "42601", `syntax error at or near "ON"`, "ON DELETE"},
	}
	for _, tc := range cases {
		t.Run(tc.suffix, func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				q := "CREATE TABLE c(\"é\" INT,pid INT,note TEXT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p " + tc.suffix + ")"
				d := foreignKeyError(tc.code, tc.message, "", "", "")
				if tc.token != "" {
					i := strings.LastIndex(q, tc.token)
					if tc.token == ")" {
						i = strings.Index(q, "()") + 1
					}
					d.Position = fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
				}
				for _, prepared := range []bool{false, true} {
					s := foreignKeyFail(q, d, prepared)
					if prepared && (tc.code == "42601" || tc.token != "") {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferenceForeignActionsDefinitionGenerated(t *testing.T) {
	for _, direction := range []string{"UPDATE", "DELETE"} {
		for _, action := range []string{"CASCADE", "SET NULL", "SET DEFAULT"} {
			if direction == "DELETE" && action == "CASCADE" {
				continue
			}
			t.Run(direction+"/"+action, func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
					q := "CREATE TABLE c(a INT,b INT GENERATED ALWAYS AS (a+1) STORED,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON " + direction + " " + action
					if direction == "DELETE" {
						q += " (a)"
					}
					q += ")"
					d := foreignKeyError("42601", "invalid ON "+direction+" action for foreign key constraint containing generated column", "", "", "")
					for _, prepared := range []bool{false, true} {
						run(foreignKeyFail(q, d, prepared))
					}
				})
			})
		}
	}
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(base INT,pid INT GENERATED ALWAYS AS (base+1) STORED CONSTRAINT fk REFERENCES p ON DELETE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(2)", 1))
		run(fixture.Exec("INSERT INTO c(base) VALUES(1)", 1))
		run(fixture.Exec("DELETE FROM p", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
	})
}
