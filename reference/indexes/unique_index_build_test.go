package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Both attachment forms exercise the same consumer assertions, not two checkers.
func uniqueIndexReferenceCreate(run func(fixture.Step), standalone bool, constraintSQL, tableSQL, indexSQL string) {
	if !standalone {
		run(fixture.Exec(constraintSQL, 0))
		return
	}
	run(fixture.Exec(tableSQL, 0))
	run(fixture.Exec(indexSQL, 0))
}

func uniqueIndexReferenceBuildDiagnostic(table, index, detail string) fixture.Diagnostic {
	d := fixture.UniqueReferenceDuplicate(table, index, detail)
	d.Message = `could not create unique index "` + index + `"`
	return d
}

func TestReferenceUniqueIndexBuild(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, key := range []struct{ name, values, policy, detail string }{
			{"nonnull", "(1,'x'),(1,'x')", "", "1, x"},
			{"null_first", "(NULL,'x'),(NULL,'x')", " NULLS NOT DISTINCT", "null, x"},
			{"null_second", "(1,NULL),(1,NULL)", " NULLS NOT DISTINCT", "1, null"},
			{"all_null", "(NULL,NULL),(NULL,NULL)", " NULLS NOT DISTINCT", "null, null"},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%t", key.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
						run(fixture.Exec("INSERT INTO ui_t VALUES"+key.values, 2))
						run(fixture.Exec("CREATE INDEX ui_sibling ON ui_t(b)", 0))
						var before bytes.Buffer
						if !b.PG {
							if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
								t.Fatal(err)
							}
						}
						s := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+key.policy, uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", "Key (a, b)=("+key.detail+") is duplicated."))
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						run(fixture.Select("SELECT count(*) AS n FROM ui_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ui_ab' OR relname='ui_sibling' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ui_sibling"}}))
						if !b.PG {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
								t.Fatal("failed build changed dump", err, after.String())
							}
						}
						run(fixture.Exec("DELETE FROM ui_t", 2))
						run(fixture.Exec("INSERT INTO ui_t VALUES"+strings.Split(key.values, "),(")[0]+")", 1))
						run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+key.policy, 0)))
						run(fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE relname='ui_ab'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}))
						run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ui_ab'", []string{"conname"}, []string{"NAME"}, nil))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueIndexBuildNullDistinct(t *testing.T) {
	for _, policy := range []string{"", " NULLS DISTINCT"} {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name+policy, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE ui_t(a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO ui_t VALUES(NULL,1),(NULL,1),(1,NULL),(1,NULL),(NULL,NULL),(NULL,NULL)", 6))
				run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+policy, 0)))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,1),(1,2),(2,1)", 3))
				run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES(1,1)", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(1, 1) already exists.")))
				run(fixture.Select("SELECT count(*) AS n FROM ui_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
			})
		}
	}
}

func TestReferenceUniqueIndexDecorations(t *testing.T) {
	for _, keys := range []string{"a DESC NULLS FIRST,b ASC NULLS LAST", "a,a,b", "a,b text_pattern_ops", "a,b varchar_ops"} {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name+keys, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE ui_t(a INT,b TEXT,p TEXT)", 0))
				run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t("+keys+") INCLUDE(p) NULLS NOT DISTINCT", 0)))
				run(fixture.Exec("INSERT INTO ui_t VALUES(1,'x','first'),(1,'y','second'),(NULL,'x','null')", 3))
				columns, values := "a, b", "1, x"
				if keys == "a,a,b" {
					columns, values = "a, a, b", "1, 1, x"
				}
				run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES(1,'x','different payload')", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key ("+columns+")=("+values+") already exists."))))
				run(fixture.Exec("UPDATE ui_t SET p='changed' WHERE a=1 AND b='x'", 1))
				run(fixture.Select("SELECT a,b,p FROM ui_t ORDER BY a NULLS LAST,b", []string{"a", "b", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "changed"}, {int64(1), "y", "second"}, {nil, "x", "null"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexNonuniqueNullPolicy(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ui_t(a INT)", 0))
			run(fixture.Exec("CREATE INDEX ui_ab ON ui_t(a) NULLS NOT DISTINCT", 0))
			run(fixture.Exec("INSERT INTO ui_t VALUES(1),(1),(NULL),(NULL)", 4))
			run(fixture.Select("SELECT pg_get_indexdef('ui_ab'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ui_ab ON public.ui_t USING btree (a) NULLS NOT DISTINCT"}}))
		})
	}
}
