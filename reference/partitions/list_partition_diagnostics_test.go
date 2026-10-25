package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceListPartitionOwnership(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE lp_own(k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_first PARTITION OF lp_own FOR VALUES IN(1,1,NULL,NULL)", 0))
		run(fixture.Exec("CREATE TABLE lp_second PARTITION OF lp_own FOR VALUES IN(2)", 0))
		for _, c := range []struct{ values, owner, token string }{{"1", "lp_first", "1)"}, {"NULL", "lp_first", "NULL"}, {"2,1", "lp_second", "2,1"}, {"0+1", "lp_first", "0+1"}} {
			q := "CREATE TABLE bad PARTITION OF lp_own FOR VALUES IN(" + c.values + ")"
			run(referenceRangeError(q, "42P17", `partition "bad" would overlap partition "`+c.owner+`"`, fixture.Diagnostic{Position: referenceRangePosition(q, c.token)}))
			referenceListAbsent(run, "bad")
		}
		q := "CREATE TABLE bad PARTITION OF lp_own FOR VALUES IN(1,'no')"
		run(referenceRangeError(q, "22P02", `invalid input syntax for type integer: "no"`, fixture.Diagnostic{Position: referenceRangePosition(q, "'no'")}))
		run(fixture.Exec("CREATE SEQUENCE lp_draw START 10", 0))
		q = "CREATE TABLE bad PARTITION OF lp_own FOR VALUES IN(1,nextval('lp_draw'))"
		run(referenceRangeError(q, "42P17", `partition "bad" would overlap partition "lp_first"`, fixture.Diagnostic{Position: referenceRangePosition(q, "1,")}))
		run(fixture.Select("SELECT last_value FROM lp_draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
		referenceListAbsent(run, "bad")
		run(fixture.Exec("CREATE TABLE bad PARTITION OF lp_own FOR VALUES IN(10)", 0))
		run(fixture.Exec("INSERT INTO lp_own VALUES(1),(NULL),(10)", 3))
		run(fixture.Select("SELECT k FROM lp_first ORDER BY k NULLS LAST", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
		run(fixture.Exec("CREATE TABLE lp_own_def PARTITION OF lp_own DEFAULT", 0))
		q = "CREATE TABLE bad_def PARTITION OF lp_own DEFAULT"
		run(referenceRangeError(q, "42P17", `partition "bad_def" conflicts with existing default partition "lp_own_def"`, fixture.Diagnostic{Position: referenceRangePosition(q, "DEFAULT")}))
		referenceListAbsent(run, "bad_def")
	})
}

func TestReferenceListPartitionDiagnostics(t *testing.T) {
	cases := []struct {
		name, sql, code, message, token string
		syntax                          bool
	}{
		{"multikey", "CREATE TABLE bad(k INT,payload TEXT) PARTITION BY LIST(k,payload)", "42P17", `cannot use "list" partition strategy with more than one column`, "", false},
		{"multikey_before_binding", "CREATE TABLE bad(k INT) PARTITION BY LIST(missing,k)", "42P17", `cannot use "list" partition strategy with more than one column`, "", false},
		{"wrong_range", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES FROM(1) TO(3)", "42P16", "invalid bound specification for a list partition", "FROM", false},
		{"wrong_hash", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES WITH(MODULUS 1,REMAINDER 0)", "42P16", "invalid bound specification for a list partition", "WITH", false},
		{"cast", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(TRUE)", "42804", `specified value cannot be cast to type integer for column "k"`, "TRUE", false},
		{"cast_expression", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(TRUE::BOOL)", "42804", `specified value cannot be cast to type integer for column "k"`, "TRUE", false},
		{"invalid_input", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN('no')", "22P02", `invalid input syntax for type integer: "no"`, "'no'", false},
		{"parameter", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN($1)", "42P02", "there is no parameter $1", "$1", false},
		{"column", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(k)", "0A000", "cannot use column reference in partition bound expression", "k)", false},
		{"minvalue", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(MINVALUE)", "0A000", "cannot use column reference in partition bound expression", "MINVALUE", false},
		{"maxvalue", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(MAXVALUE)", "0A000", "cannot use column reference in partition bound expression", "MAXVALUE", false},
		{"subquery", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN((SELECT 1))", "0A000", "cannot use subquery in partition bound", "(SELECT", false},
		{"aggregate", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(max(1))", "42803", "aggregate functions are not allowed in partition bound", "max", false},
		{"window", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN(row_number() OVER())", "42P20", "window functions are not allowed in partition bound", "row_number", false},
		{"empty", "CREATE TABLE bad PARTITION OF lp_diag FOR VALUES IN()", "42601", `syntax error at or near ")"`, ")", true},
		{"trailing", "CREATE TABLE bad(k INT) PARTITION BY LIST(k) garbage", "42601", `syntax error at or near "garbage"`, "garbage", true},
		{"missing", `CREATE TABLE bad("é" INT) PARTITION BY LIST("失")`, "42703", `column "失" named in partition key does not exist`, `"失"`, false},
		{"mutable", "CREATE TABLE bad(k INT) PARTITION BY LIST((random()))", "42P17", "functions in partition key expression must be marked IMMUTABLE", "", false},
		{"constant", "CREATE TABLE bad(k INT) PARTITION BY LIST((1))", "42P17", "cannot use constant expression as partition key", "", false},
		{"system", "CREATE TABLE bad(k INT) PARTITION BY LIST(xmin)", "42P17", `cannot use system column "xmin" in partition key`, "xmin", false},
		{"opclass", "CREATE TABLE bad(k INT) PARTITION BY LIST(k absent_ops)", "42704", `operator class "absent_ops" does not exist for access method "btree"`, "", false},
		{"collation", "CREATE TABLE bad(k TEXT) PARTITION BY LIST(k COLLATE absent)", "42704", `collation "absent" for encoding "UTF8" does not exist`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := partitionConstraintRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE lp_diag(k INT) PARTITION BY LIST(k)", 0))
				fields := fixture.Diagnostic{}
				if c.token != "" {
					fields.Position = referenceRangePosition(c.sql, c.token)
				}
				s := referenceRangeError(c.sql, c.code, c.message, fields)
				if c.syntax && prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				referenceListAbsent(run, "bad")
				run(referenceRangeRows("SELECT k FROM lp_diag"))
			})
		})
	}
	for _, strategy := range []string{"RANGE", "HASH"} {
		t.Run(fmt.Sprint(strategy), func(t *testing.T) {
			referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := partitionConstraintRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE lp_wrong(k INT) PARTITION BY "+strategy+"(k)", 0))
				q := "CREATE TABLE bad PARTITION OF lp_wrong FOR VALUES IN(1)"
				word := "range"
				if strategy == "HASH" {
					word = "hash"
				}
				run(referenceRangeError(q, "42P16", "invalid bound specification for a "+word+" partition", fixture.Diagnostic{Position: referenceRangePosition(q, "IN(")}))
				referenceListAbsent(run, "bad")
			})
		})
	}
}
