package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionDiagnostics(t *testing.T) {
	cases := []struct {
		name, setup, sql, code, message, token string
		fields                                 fixture.Diagnostic
	}{
		{"lower_null", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(NULL) TO(3)", "42P17", "cannot specify NULL in range bound", "", fixture.Diagnostic{}},
		{"upper_null", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(1) TO(NULL)", "42P17", "cannot specify NULL in range bound", "", fixture.Diagnostic{}},
		{"from_arity", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(1,2) TO(3)", "42P16", "FROM must specify exactly one value per partitioning column", "", fixture.Diagnostic{}},
		{"to_arity", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(1) TO(3,4)", "42P16", "TO must specify exactly one value per partitioning column", "", fixture.Diagnostic{}},
		{"equal", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(1) TO(1)", "42P17", `empty range bound specified for partition "bad"`, "1)", fixture.Diagnostic{Detail: "Specified lower bound (1) is greater than or equal to upper bound (1)."}},
		{"reversed", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(4) TO(2)", "42P17", `empty range bound specified for partition "bad"`, "4)", fixture.Diagnostic{Detail: "Specified lower bound (4) is greater than or equal to upper bound (2)."}},
		{"overlap_lower", "CREATE TABLE rp_diag_a PARTITION OF rp_diag FOR VALUES FROM(1) TO(3)", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(2) TO(4)", "42P17", `partition "bad" would overlap partition "rp_diag_a"`, "2)", fixture.Diagnostic{}},
		{"overlap_upper", "CREATE TABLE rp_diag_a PARTITION OF rp_diag FOR VALUES FROM(1) TO(3)", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(0) TO(2)", "42P17", `partition "bad" would overlap partition "rp_diag_a"`, "2)", fixture.Diagnostic{}},
		{"second_default", "CREATE TABLE rp_diag_def PARTITION OF rp_diag DEFAULT", "CREATE TABLE bad PARTITION OF rp_diag DEFAULT", "42P17", `partition "bad" conflicts with existing default partition "rp_diag_def"`, "DEFAULT", fixture.Diagnostic{}},
		{"ordinary", "CREATE TABLE ordinary(k INT)", "CREATE TABLE bad PARTITION OF ordinary FOR VALUES FROM(1) TO(3)", "42P17", `"ordinary" is not partitioned`, "", fixture.Diagnostic{}},
		{"missing_parent", "", "CREATE TABLE bad PARTITION OF absent FOR VALUES FROM(1) TO(3)", "42P01", `relation "absent" does not exist`, "", fixture.Diagnostic{}},
		{"cast_type", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(TRUE) TO(3)", "42804", `specified value cannot be cast to type integer for column "k"`, "TRUE", fixture.Diagnostic{}},
		{"invalid_input", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM('no') TO(3)", "22P02", `invalid input syntax for type integer: "no"`, "'no'", fixture.Diagnostic{}},
		{"overflow", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM('2147483648') TO(MAXVALUE)", "22003", `value "2147483648" is out of range for type integer`, "'2147483648'", fixture.Diagnostic{}},
		{"bound_column", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(k) TO(3)", "0A000", "cannot use column reference in partition bound expression", "k)", fixture.Diagnostic{}},
		{"bound_subquery", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM((SELECT 1)) TO(3)", "0A000", "cannot use subquery in partition bound", "(SELECT", fixture.Diagnostic{}},
		{"bound_aggregate", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(max(1)) TO(3)", "42803", "aggregate functions are not allowed in partition bound", "max", fixture.Diagnostic{}},
		{"bound_window", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(row_number() OVER()) TO(3)", "42P20", "window functions are not allowed in partition bound", "row_number", fixture.Diagnostic{}},
		{"bound_column_precedence", "", "CREATE TABLE bad PARTITION OF rp_diag FOR VALUES FROM(max(k)) TO(3)", "0A000", "cannot use column reference in partition bound expression", "k))", fixture.Diagnostic{}},
		{"key_subquery", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE(((SELECT k)))", "0A000", "cannot use subquery in partition key expression", "", fixture.Diagnostic{}},
		{"key_aggregate", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE((max(k)))", "42803", "aggregate functions are not allowed in partition key expressions", "", fixture.Diagnostic{}},
		{"key_window", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE((row_number() OVER()))", "42P20", "window functions are not allowed in partition key expressions", "", fixture.Diagnostic{}},
		{"missing_collation", "", "CREATE TABLE bad(k TEXT) PARTITION BY RANGE(k COLLATE absent)", "42704", `collation "absent" for encoding "UTF8" does not exist`, "", fixture.Diagnostic{}},
		{"missing_class", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE(k absent_ops)", "42704", `operator class "absent_ops" does not exist for access method "btree"`, "", fixture.Diagnostic{}},
		{"missing_key", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE(missing)", "42703", `column "missing" named in partition key does not exist`, "missing", fixture.Diagnostic{}},
		{"system_key", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE(xmin)", "42P17", `cannot use system column "xmin" in partition key`, "xmin", fixture.Diagnostic{}},
		{"constant_key", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE((1))", "42P17", "cannot use constant expression as partition key", "", fixture.Diagnostic{}},
		{"mutable_key", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE((random()))", "42P17", "functions in partition key expression must be marked IMMUTABLE", "", fixture.Diagnostic{}},
		{"generated_key", "", "CREATE TABLE bad(k INT,g INT GENERATED ALWAYS AS(k+1) STORED) PARTITION BY RANGE(g)", "42P17", "cannot use generated column in partition key", "g)", fixture.Diagnostic{Detail: `Column "g" is a generated column.`}},
		{"no_inherit", "", "CREATE TABLE bad(k INT,CHECK(k>0) NO INHERIT) PARTITION BY RANGE(k)", "42P16", `cannot add NO INHERIT constraint to partitioned table "bad"`, "", fixture.Diagnostic{}},
		{"utf8_missing", "", `CREATE TABLE bad("é" INT) PARTITION BY RANGE("失")`, "42703", `column "失" named in partition key does not exist`, `"失"`, fixture.Diagnostic{}},
		{"trailing", "", "CREATE TABLE bad(k INT) PARTITION BY RANGE(k) garbage", "42601", `syntax error at or near "garbage"`, "garbage", fixture.Diagnostic{}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.name+"/"+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE rp_diag(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
					if c.setup != "" {
						run(fixture.Exec(c.setup, 0))
					}
					fields := c.fields
					if c.token != "" {
						fields.Position = referenceRangePosition(c.sql, c.token)
					}
					s := referenceRangeError(c.sql, c.code, c.message, fields)
					s.Prepared = prepared
					if prepared && c.name == "trailing" {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT * FROM rp_diag", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
				})
			})
		}
	}
}

func TestReferenceRangePartitionSentinelDiagnostics(t *testing.T) {
	for _, sentinel := range []string{"MINVALUE", "MAXVALUE"} {
		for _, prepared := range []bool{false, true} {
			t.Run(sentinel+fmt.Sprint(prepared), func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE rp_pair(x INT,y INT) PARTITION BY RANGE(x,y)", 0))
					q := "CREATE TABLE bad PARTITION OF rp_pair FOR VALUES FROM(" + sentinel + ",1) TO(MAXVALUE,MAXVALUE)"
					s := referenceRangeError(q, "42804", "every bound following "+sentinel+" must also be "+sentinel, fixture.Diagnostic{Position: referenceRangePosition(q, "1)")})
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			})
		}
	}
}

func TestReferenceRangePartitionUtilityParameters(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE rp_param(k INT) PARTITION BY RANGE(k)", 0))
				q := "CREATE TABLE bad PARTITION OF rp_param FOR VALUES FROM($1) TO(3)"
				// Utility expressions have no bind parameter namespace. PG Parse is
				// predicted to accept this utility, with the failure at Execute.
				s := referenceRangeError(q, "42P02", "there is no parameter $1", fixture.Diagnostic{Position: referenceRangePosition(q, "$1")})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}

func TestReferenceRangePartitionUtilityBoundArguments(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE rp_param(k INT) PARTITION BY RANGE(k)", 0))
				q := "CREATE TABLE bad PARTITION OF rp_param FOR VALUES FROM($1) TO(3)"
				// Pinned18 utility Describe supplies zero bind slots. Direct
				// pq execution and database/sql's held-statement argument
				// check expose distinct ordinary adapter errors.
				var err error
				if prepared {
					stmt, e := b.DB.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal("utility Prepare unexpectedly failed", e)
					}
					defer stmt.Close()
					result, e := stmt.ExecContext(p.Ctx, 1)
					err = e
					if result != nil {
						t.Fatal("failed utility bind exposed result")
					}
				} else {
					result, e := b.DB.ExecContext(p.Ctx, q, 1)
					err = e
					if result != nil {
						t.Fatal("failed utility bind exposed result")
					}
				}
				wantMessage := "pq: got 1 parameters but the statement requires 0"
				if !b.PG {
					wantMessage = "stuntgres: got 1 arguments, want 0"
				}
				if prepared {
					wantMessage = "sql: expected 0 arguments, got 1"
				}
				if err == nil || err.Error() != wantMessage {
					t.Fatalf("utility argument boundary: %T %v; want %q", err, err, wantMessage)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("transport argument error fabricated SQLSTATE")
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}
