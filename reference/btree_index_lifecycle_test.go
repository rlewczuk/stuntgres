//go:build reference

package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceBtreeIndexNaming(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
		longTable := strings.Repeat("t", 40)
		keys := a + "," + b + "," + c
		for _, step := range []referenceStep{
			referenceExec("CREATE TABLE t(a INT,"+a+" INT,"+b+" INT,"+c+" INT)", 0),
			referenceExec("CREATE INDEX ON t(a,a)", 0),
			referenceExec("CREATE INDEX ON t(a,a)", 0),
			referenceExec("CREATE INDEX ON t("+keys+")", 0), referenceExec("CREATE INDEX ON t("+keys+")", 0),
			referenceExec("CREATE TABLE "+longTable+"("+a+" INT,"+b+" INT,"+c+" INT)", 0),
			referenceExec("CREATE INDEX ON "+longTable+"("+keys+")", 0),
			referenceExec("CREATE INDEX ON "+longTable+"("+keys+")", 0),
			referenceExec(`CREATE TABLE "éTable"("列" INT,index TEXT)`, 0),
			referenceExec(`CREATE INDEX "éIndex" ON "éTable"("列",index DESC)`, 0),
		} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		for _, name := range []string{"t_a_a1_idx", "t_a_a1_idx1", "t_" + a + "_" + strings.Repeat("b", 16) + "_idx", "t_" + a + "_" + strings.Repeat("b", 15) + "_idx1", strings.Repeat("t", 29) + "_" + strings.Repeat("a", 29) + "_idx", strings.Repeat("t", 29) + "_" + strings.Repeat("a", 28) + "_idx1", "éIndex"} {
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
		}
	})
}
func TestReferenceBtreeIndexLifecycle(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		for _, step := range []referenceStep{
			referenceExec("CREATE TABLE t(extra INT,n INT,s TEXT)", 0),
			referenceExec("INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')", 3),
			referenceExec("CREATE INDEX ni ON t(n)", 0), referenceExec("CREATE INDEX si ON t(s)", 0),
			referenceExec("ALTER TABLE t RENAME n TO key", 0), referenceExec("ALTER TABLE t RENAME TO renamed", 0),
			referenceExec("ALTER TABLE renamed DROP extra", 0),
			referenceExec("ALTER TABLE renamed ALTER key TYPE TEXT USING key::text", 0),
			referenceSelect("SELECT key FROM renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"1"}, {"10"}, {"2"}}),
			referenceExec("CREATE SCHEMA moved", 0), referenceExec("CREATE TABLE moved.ni(n INT)", 0),
			referenceImplicitError("ALTER TABLE renamed SET SCHEMA moved", referenceDiagnostic{Code: "42P07", Message: `relation "ni" already exists in schema "moved"`}),
			referenceSelect("SELECT key FROM renamed ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"1"}, {"10"}, {"2"}}),
			referenceExec("DROP TABLE moved.ni", 0), referenceExec("ALTER TABLE renamed SET SCHEMA moved", 0),
			referenceExec("ALTER TABLE moved.renamed DROP s", 0),
			referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='si'", []string{"relname"}, []string{"NAME"}, nil),
			referenceExec("DROP TABLE moved.renamed RESTRICT", 0),
			referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='ni'", []string{"relname"}, []string{"NAME"}, nil),
			referenceExec("CREATE TABLE moved.ni(n INT)", 0),
			referenceExec("DROP SCHEMA moved CASCADE", 0),
			referenceExec("CREATE TABLE ni(n INT)", 0),
		} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
	})
}

func TestReferenceBtreeIndexUtf8AndRepeatedLabels(t *testing.T) {
	p := newReferencePair(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		first, second := strings.Repeat("é", 30), strings.Repeat("b", 40)
		for _, step := range []referenceStep{
			referenceExec(`CREATE TABLE t("`+first+`" INT,`+second+` INT)`, 0),
			referenceExec(`CREATE INDEX ON t("`+first+`",`+second+`)`, 0),
			referenceExec("CREATE TABLE dup(a INT,a1 INT)", 0),
			referenceExec("CREATE INDEX ON dup(a,a,a1)", 0),
		} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		for _, name := range []string{"t_" + strings.Repeat("é", 28) + "_idx", "dup_a_a1_a11_idx"} {
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
		}
	})
}
