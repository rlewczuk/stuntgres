package stuntgres_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityReviewActionMatrix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"ALTER TABLE t ALTER a SET NOT NULL, ALTER a DROP NOT NULL",
			"ALTER TABLE t ALTER a DROP NOT NULL, ALTER a SET NOT NULL",
			"ALTER TABLE t ALTER a SET NOT NULL, DROP COLUMN a",
			"ALTER TABLE t DROP COLUMN a, ALTER a SET NOT NULL",
		} {
			t.Run(fmt.Sprint(prepared)+q, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(a INT,b INT)")
				execSQL(t, db, "INSERT INTO t VALUES(NULL,9)")
				_, err := transactionExec(t.Context(), db, q, prepared)
				if strings.Contains(q, "DROP COLUMN") {
					notNullDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "a" of relation "t" does not exist`})
				} else {
					notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
				}
				alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
				wantTuples(t, db, "SELECT * FROM t", [][]any{{nil, int64(9)}})
			})
		}
	}
}

func TestAlterNullabilityReviewGeneratedCollision(t *testing.T) {
	table := strings.Repeat("t", 40)
	a := strings.Repeat("a", 39) + "x"
	b := strings.Repeat("a", 39) + "y"
	existing := strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"
	first := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null1"
	second := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null2"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE holder(v INT CONSTRAINT "+existing+" NOT NULL)")
			execSQL(t, db, "CREATE TABLE "+table+"("+a+" INT,"+b+" INT)")
			execSQL(t, db, "INSERT INTO "+table+" VALUES(1,2)")
			transactionAffected(t, t.Context(), db, "ALTER TABLE "+table+" ALTER "+a+" SET NOT NULL, ALTER "+b+" SET NOT NULL", prepared, 0)
			alterDropMetadata(t, db, false, "SELECT * FROM "+table, []string{a, b}, []string{"INT4", "INT4"}, []bool{false, false}, 1)
			for _, probe := range []struct{ values, column, detail string }{{"NULL,2", a, "Failing row contains (null, 2)."}, {"1,NULL", b, "Failing row contains (1, null)."}} {
				_, err := transactionExec(t.Context(), db, "INSERT INTO "+table+" VALUES("+probe.values+")", prepared)
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: fmt.Sprintf(`null value in column "%s" of relation "%s" violates not-null constraint`, probe.column, table), Detail: probe.detail, Schema: "public", Table: table, Column: probe.column})
			}
			_, err := db.Exec("ALTER TABLE " + table + " DROP CONSTRAINT " + existing)
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, existing, table)})
			transactionAffected(t, t.Context(), db, "ALTER TABLE "+table+" DROP CONSTRAINT "+first+", DROP CONSTRAINT "+second, prepared, 0)
			transactionAffected(t, t.Context(), db, "INSERT INTO "+table+" VALUES(NULL,NULL)", prepared, 1)
			wantTuples(t, db, "SELECT * FROM "+table+" ORDER BY "+a, [][]any{{int64(1), int64(2)}, {nil, nil}})
			_, err = db.Exec("INSERT INTO holder VALUES(NULL)")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "holder" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "holder", Column: "v"})
		})
	}
}

func TestAlterNullabilityReviewRenameWholeInput(t *testing.T) {
	q := "ALTER TABLE t ALTER a SET NOT NULL, RENAME COLUMN a TO x"
	prefix := "CREATE TABLE prefix(a INT);INSERT INTO prefix VALUES(1);"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7)")
			for _, leading := range []string{"", prefix} {
				var err error
				if prepared {
					s, e := db.Prepare(leading + q)
					err = e
					if s != nil {
						s.Close()
						t.Fatal("malformed list prepared")
					}
				} else {
					_, err = db.Exec(leading + q)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "RENAME"`, Position: strconv.Itoa(len(leading) + 37)})
				alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
				wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
				if leading != "" {
					transactionAffected(t, t.Context(), db, "CREATE TABLE prefix(a INT)", false, 0)
					transactionAffected(t, t.Context(), db, "DROP TABLE prefix", false, 0)
				}
			}
		})
	}
}
