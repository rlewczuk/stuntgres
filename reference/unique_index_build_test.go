package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

// Both attachment forms exercise the same consumer assertions, not two checkers.
func uniqueIndexReferenceCreate(run func(referenceStep), standalone bool, constraintSQL, tableSQL, indexSQL string) {
	if !standalone {
		run(referenceExec(constraintSQL, 0))
		return
	}
	run(referenceExec(tableSQL, 0))
	run(referenceExec(indexSQL, 0))
}

func uniqueIndexReferenceBuildDiagnostic(table, index, detail string) referenceDiagnostic {
	d := uniqueReferenceDuplicate(table, index, detail)
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
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT)", 0))
						run(referenceExec("INSERT INTO ui_t VALUES"+key.values, 2))
						run(referenceExec("CREATE INDEX ui_sibling ON ui_t(b)", 0))
						var before bytes.Buffer
						if !b.pg {
							if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
								t.Fatal(err)
							}
						}
						s := referenceErrorDiagnostic("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+key.policy, uniqueIndexReferenceBuildDiagnostic("ui_t", "ui_ab", "Key (a, b)=("+key.detail+") is duplicated."))
						s.errorStage = "execute"
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
						run(referenceSelect("SELECT count(*) AS n FROM ui_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ui_ab' OR relname='ui_sibling' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ui_sibling"}}))
						if !b.pg {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
								t.Fatal("failed build changed dump", err, after.String())
							}
						}
						run(referenceExec("DELETE FROM ui_t", 2))
						run(referenceExec("INSERT INTO ui_t VALUES"+strings.Split(key.values, "),(")[0]+")", 1))
						run(referencePrepared(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+key.policy, 0)))
						run(referenceSelect("SELECT relkind,relnatts FROM pg_class WHERE relname='ui_ab'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}))
						run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='ui_ab'", []string{"conname"}, []string{"NAME"}, nil))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueIndexBuildNullDistinct(t *testing.T) {
	for _, policy := range []string{"", " NULLS DISTINCT"} {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name+policy, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE ui_t(a INT,b INT)", 0))
				run(referenceExec("INSERT INTO ui_t VALUES(NULL,1),(NULL,1),(1,NULL),(1,NULL),(NULL,NULL),(NULL,NULL)", 6))
				run(referencePrepared(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a,b)"+policy, 0)))
				run(referenceExec("INSERT INTO ui_t VALUES(1,1),(1,2),(2,1)", 3))
				run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES(1,1)", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(1, 1) already exists.")))
				run(referenceSelect("SELECT count(*) AS n FROM ui_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
			})
		}
	}
}

func TestReferenceUniqueIndexDecorations(t *testing.T) {
	for _, keys := range []string{"a DESC NULLS FIRST,b ASC NULLS LAST", "a,a,b", "a,b text_pattern_ops", "a,b varchar_ops"} {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name+keys, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE ui_t(a INT,b TEXT,p TEXT)", 0))
				run(referencePrepared(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t("+keys+") INCLUDE(p) NULLS NOT DISTINCT", 0)))
				run(referenceExec("INSERT INTO ui_t VALUES(1,'x','first'),(1,'y','second'),(NULL,'x','null')", 3))
				columns, values := "a, b", "1, x"
				if keys == "a,a,b" {
					columns, values = "a, a, b", "1, 1, x"
				}
				run(referencePrepared(referenceErrorDiagnostic("INSERT INTO ui_t VALUES(1,'x','different payload')", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key ("+columns+")=("+values+") already exists."))))
				run(referenceExec("UPDATE ui_t SET p='changed' WHERE a=1 AND b='x'", 1))
				run(referenceSelect("SELECT a,b,p FROM ui_t ORDER BY a NULLS LAST,b", []string{"a", "b", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "changed"}, {int64(1), "y", "second"}, {nil, "x", "null"}}))
			})
		}
	}
}

func TestReferenceUniqueIndexNonuniqueNullPolicy(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE ui_t(a INT)", 0))
			run(referenceExec("CREATE INDEX ui_ab ON ui_t(a) NULLS NOT DISTINCT", 0))
			run(referenceExec("INSERT INTO ui_t VALUES(1),(1),(NULL),(NULL)", 4))
			run(referenceSelect("SELECT pg_get_indexdef('ui_ab'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ui_ab ON public.ui_t USING btree (a) NULLS NOT DISTINCT"}}))
		})
	}
}
