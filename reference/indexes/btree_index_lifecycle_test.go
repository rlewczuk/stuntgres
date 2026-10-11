package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexNaming(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
		longTable := strings.Repeat("t", 40)
		keys := a + "," + b + "," + c
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(a INT,"+a+" INT,"+b+" INT,"+c+" INT)", 0),
			fixture.Exec("CREATE INDEX ON t(a,a)", 0),
			fixture.Exec("CREATE INDEX ON t(a,a)", 0),
			fixture.Exec("CREATE INDEX ON t("+keys+")", 0), fixture.Exec("CREATE INDEX ON t("+keys+")", 0),
			fixture.Exec("CREATE TABLE "+longTable+"("+a+" INT,"+b+" INT,"+c+" INT)", 0),
			fixture.Exec("CREATE INDEX ON "+longTable+"("+keys+")", 0),
			fixture.Exec("CREATE INDEX ON "+longTable+"("+keys+")", 0),
			fixture.Exec(`CREATE TABLE "éTable"("列" INT,index TEXT)`, 0),
			fixture.Exec(`CREATE INDEX "éIndex" ON "éTable"("列",index DESC)`, 0),
		} {
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		}
		for _, name := range []string{"t_a_a1_idx", "t_a_a1_idx1", "t_" + a + "_" + strings.Repeat("b", 16) + "_idx", "t_" + a + "_" + strings.Repeat("b", 15) + "_idx1", strings.Repeat("t", 29) + "_" + strings.Repeat("a", 29) + "_idx", strings.Repeat("t", 29) + "_" + strings.Repeat("a", 28) + "_idx1", "éIndex"} {
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
		}
	})
}
func TestReferenceBtreeIndexLifecycle(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		t.Run(map[bool]string{false: "original catalog", true: "incoming maintained rows"}[mutate], func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "moved")
			p := fixture.NewScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
				want := [][]any{{"1"}, {"10"}, {"2"}}
				if mutate {
					want = [][]any{{"10"}, {"3"}}
				}
				for _, step := range []fixture.Step{
					fixture.Exec("CREATE TABLE t(extra INT,n INT,s TEXT)", 0),
					fixture.Exec("INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')", 3),
					fixture.Exec("CREATE INDEX ni ON t(n)", 0), fixture.Exec("CREATE INDEX si ON t(s)", 0),
					fixture.Exec("ALTER TABLE t RENAME n TO key", 0), fixture.Exec("ALTER TABLE t RENAME TO renamed", 0),
					fixture.Exec("ALTER TABLE renamed DROP extra", 0),
					fixture.Exec("ALTER TABLE renamed ALTER key TYPE TEXT USING key::text", 0),
					fixture.Select("SELECT key FROM renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, want),
					fixture.Exec("CREATE SCHEMA moved", 0), fixture.Exec("CREATE TABLE moved.ni(n INT)", 0),
					fixture.ImplicitError("ALTER TABLE renamed SET SCHEMA moved", fixture.Diagnostic{Code: "42P07", Message: `relation "ni" already exists in schema "moved"`}),
					fixture.Select("SELECT key FROM renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, want),
					fixture.Exec("DROP TABLE moved.ni", 0), fixture.Exec("ALTER TABLE renamed SET SCHEMA moved", 0),
					fixture.Select("SELECT key FROM moved.renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, want),
					fixture.Exec("ALTER TABLE moved.renamed DROP s", 0),
					fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='si'", []string{"relname"}, []string{"NAME"}, nil),
					fixture.Exec("DROP TABLE moved.renamed RESTRICT", 0),
					fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='ni'", []string{"relname"}, []string{"NAME"}, nil),
					fixture.Exec("CREATE TABLE moved.ni(n INT)", 0),
					fixture.Exec("DROP SCHEMA moved CASCADE", 0),
					fixture.Exec("CREATE TABLE ni(n INT)", 0),
				} {
					if mutate && step.Query == "ALTER TABLE t RENAME n TO key" {
						fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("UPDATE t SET n=3 WHERE n=2", 1))
						fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("DELETE FROM t WHERE n=1", 1))
					}
					fixture.RunStep(t, p.Ctx, db, recorder, step)
				}
			})
		})
	}
}

func TestReferenceBtreeIndexUtf8AndRepeatedLabels(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		first, second := strings.Repeat("é", 30), strings.Repeat("b", 40)
		for _, step := range []fixture.Step{
			fixture.Exec(`CREATE TABLE t("`+first+`" INT,`+second+` INT)`, 0),
			fixture.Exec(`CREATE INDEX ON t("`+first+`",`+second+`)`, 0),
			fixture.Exec("CREATE TABLE dup(a INT,a1 INT)", 0),
			fixture.Exec("CREATE INDEX ON dup(a,a,a1)", 0),
		} {
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		}
		for _, name := range []string{"t_" + strings.Repeat("é", 28) + "_idx", "dup_a_a1_a11_idx"} {
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
		}
	})
}
