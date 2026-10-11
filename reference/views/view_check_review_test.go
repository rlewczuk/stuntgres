package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckReviewVirtualNewRows(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
					run(
						fixture.Exec("CREATE TABLE t(junk INT DEFAULT 8,a INT,g INT GENERATED ALWAYS AS(a+1) VIRTUAL)", 0),
						fixture.Exec("CREATE VIEW v AS SELECT a FROM t WHERE g>0 WITH CHECK OPTION", 0),
					)
					insertSQL := "INSERT INTO v(a) VALUES($1)"
					updateSQL := "UPDATE v SET a=$1 WHERE a=$2 RETURNING a"
					var insert, update fixture.Executor = b.DB, b.DB
					if held {
						insert = fixture.ViewHold(t, p, b, insertSQL)
						update = fixture.ViewHold(t, p, b, updateSQL)
					}
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 1))
					bag := fixture.Select("SELECT junk,a,g FROM t", []string{"junk", "a", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(8), int64(1), int64(2)}})
					run(bag)
					for _, tc := range []struct {
						arg    any
						detail string
					}{{-2, "8, -2, virtual"}, {nil, "8, null, virtual"}} {
						fixture.RunStep(t, p.Ctx, insert, b.Recorder, viewCheckError(insertSQL, "v", tc.detail, tc.arg))
						run(bag)
					}
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Select(updateSQL, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}, 2, 1))
					fail := viewCheckError(updateSQL, "v", "8, -2, virtual", -2, 2)
					fail.SelectRows = true
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fail)
					run(fixture.Select("SELECT junk,a,g FROM t", []string{"junk", "a", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(8), int64(2), int64(3)}}))

					// The same retained handles must follow stable attributes, not the
					// dropped leading slot or the newly reused source-column name.
					run(
						fixture.Exec("ALTER TABLE t DROP COLUMN junk", 0),
						fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0),
						fixture.Exec("ALTER TABLE t ADD COLUMN a INT DEFAULT 99", 0),
					)
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 3))
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Select(updateSQL, []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}, 4, 2))
					liveBag := fixture.Select("SELECT renamed,g,a FROM t ORDER BY renamed", []string{"renamed", "g", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(4), int64(99)}, {int64(4), int64(5), int64(99)}})
					run(liveBag)
					fixture.RunStep(t, p.Ctx, insert, b.Recorder, viewCheckError(insertSQL, "v", "-2, virtual, 99", -2))
					run(liveBag)
					fail = viewCheckError(updateSQL, "v", "-2, virtual, 99", -2, 4)
					fail.SelectRows = true
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fail)
					run(liveBag)
					fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Select(updateSQL, []string{"a"}, []string{"INT4"}, nil, -2, 999))

					// Independent generated source occurrences retain SELECT identity;
					// lowering the destination must not strip the subquery's provenance.
					run(
						fixture.Exec("CREATE TABLE lookup(k INT,g INT GENERATED ALWAYS AS(COALESCE(k,9)) VIRTUAL)", 0),
						fixture.Exec("INSERT INTO lookup(k) VALUES(NULL)", 1),
						fixture.Exec("CREATE VIEW source_v AS SELECT renamed AS a FROM t WHERE t.g>0 AND EXISTS(SELECT 1 FROM lookup WHERE lookup.g=9) WITH CHECK OPTION", 0),
					)
					q := "INSERT INTO source_v(a) VALUES($1)"
					var source fixture.Executor = b.DB
					if held {
						source = fixture.ViewHold(t, p, b, q)
					}
					fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(q, 1, 5))
					run(fixture.Exec("DELETE FROM lookup", 1))
					fixture.RunStep(t, p.Ctx, source, b.Recorder, viewCheckError(q, "source_v", "6, virtual, 99", 6))
					run(
						fixture.Select("SELECT renamed,g,a FROM t ORDER BY renamed", []string{"renamed", "g", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(4), int64(99)}, {int64(4), int64(5), int64(99)}, {int64(5), int64(6), int64(99)}}),
						fixture.Select("SELECT k,g FROM lookup", []string{"k", "g"}, []string{"INT4", "INT4"}, nil),
					)
				})
			}
		})
	}
}

func TestReferenceViewCheckReviewQuotedNames(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					for _, tc := range []struct{ sqlName, message string }{
						{`"a""b"`, `new row violates check option for view "a"b"`},
						{`"a\b"`, `new row violates check option for view "a\b"`},
					} {
						run(fixture.Exec("CREATE VIEW "+tc.sqlName+" AS SELECT a FROM t WHERE a>0 WITH CHECK OPTION", 0))
						q := "INSERT INTO " + tc.sqlName + " VALUES($1)"
						var insert fixture.Executor = b.DB
						if held {
							insert = fixture.ViewHold(t, p, b, q)
						}
						// Literal PostgreSQL text, deliberately independent of viewCheckError.
						failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "44000", Message: tc.message, Detail: "Failing row contains (-1)."}, -1)
						failure.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, insert, b.Recorder, failure)
						run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, nil))
						fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(q, 1, 1))
						updateSQL := "UPDATE " + tc.sqlName + " SET a=$1 RETURNING a"
						var update fixture.Executor = b.DB
						if held {
							update = fixture.ViewHold(t, p, b, updateSQL)
						}
						failure.Query = updateSQL
						failure.SelectRows = true
						fixture.RunStep(t, p.Ctx, update, b.Recorder, failure)
						run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Select(updateSQL, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}, 2))
						run(fixture.Exec("DROP VIEW "+tc.sqlName, 0), fixture.Exec("DELETE FROM t", 1))
					}
				})
			}
		})
	}
}
