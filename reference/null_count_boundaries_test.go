package reference_test

import "testing"

func TestReferenceNullCountCapabilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(num_nulls INT,num_nonnulls INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2)", 1))
	p.step(t, referenceSelect("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	p.step(t, referenceSelect("SELECT abs(1) FROM t", []string{"abs"}, []string{"INT4"}, [][]any{{int64(1)}}))
	for _, step := range []referenceStep{
		referenceSelect("SELECT 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT num_nulls(NULL),1", []string{"num_nulls", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		referenceSelect("SELECT $1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"unit"}}, "unit"),
		referenceSelect("SELECT num_nulls(NULL) WHERE TRUE", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT num_nulls(NULL) ORDER BY 1", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT num_nulls(ARRAY[1,NULL])", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}}),
	} {
		for _, prepared := range []bool{false, true} {
			step.prepared = prepared
			p.step(t, step)
		}
	}
	nullCountReferenceError(t, p, "SELECT *", referenceDiagnostic{Code: "42601", Message: "SELECT * with no tables specified is not valid", Position: "8"})
	// Replace obsolete no-FROM rejections with a genuine whole-input failure,
	// retaining nil cursor/statement/result and no-prefix-effects obligations.
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			failure := referenceError("INSERT INTO t VALUES(9,9);SELECT 1 WHERE", "42601")
			failure.prepared, failure.selectRows = prepared, queryRows
			p.step(t, failure)
			p.step(t, referenceSelect("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		}
	}
	for _, backend := range p.backends() {
		rows, err := backend.db.QueryContext(p.ctx, "SELECT $1")
		if rows != nil {
			rows.Close()
			t.Fatal("missing argument exposed cursor")
		}
		if err == nil {
			t.Fatal("missing argument accepted")
		}
		if backend.pg {
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1", Position: "8"})
		}
		stmt, err := backend.db.PrepareContext(p.ctx, "SELECT $1")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("missing prepared argument exposed cursor")
			}
			transactionOrdinary(t, err, "sql: expected 1 arguments, got 0")
		}()
	}
	for _, q := range []string{
		"SELECT num_nulls(NULL)::int FROM t",
		"SELECT num_nulls FROM t WHERE num_nulls(NULL)",
		"INSERT INTO t VALUES(num_nulls(NULL),1)",
		"SELECT num_nulls(DISTINCT 1) FROM t", "SELECT num_nulls(1 ORDER BY 1) FROM t", "SELECT num_nulls(*) FROM t",
		"SELECT num_nulls(1) FILTER (WHERE TRUE) FROM t",
		"SELECT num_nulls(a => 1) FROM t", "SELECT current_setting(num_nulls(NULL))",
	} {
		t.Run(q, func(t *testing.T) {
			rows, err := p.memory.QueryContext(p.ctx, q)
			if rows != nil {
				rows.Close()
				t.Fatal("excluded form exposed cursor", q)
			}
			if err == nil {
				t.Fatal("excluded form accepted", q)
			}
			stmt, err := p.memory.PrepareContext(p.ctx, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("excluded form exposed handle", q)
			}
			if err == nil {
				t.Fatal("excluded prepare accepted", q)
			}
		})
	}
	if p.postgres != nil {
		nullCountPostgresBoundaries(t, p)
	}
}

func nullCountPostgresBoundaries(t *testing.T, p *referencePair) {
	t.Helper()
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	steps := []referenceStep{
		referenceSelect("SELECT num_nulls(NULL)::int FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceImplicitError("SELECT num_nulls FROM t WHERE num_nulls(NULL)", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "31"}),
		referenceImplicitError("SELECT num_nulls(DISTINCT 1) FROM t", referenceDiagnostic{Code: "42809", Message: "DISTINCT specified, but num_nulls is not an aggregate function", Position: "8"}),
		referenceImplicitError("SELECT num_nulls(1 ORDER BY 1) FROM t", referenceDiagnostic{Code: "42809", Message: "ORDER BY specified, but num_nulls is not an aggregate function", Position: "8"}),
		referenceImplicitError("SELECT num_nulls(*) FROM t", referenceDiagnostic{Code: "42883", Message: "function num_nulls() does not exist", Hint: hint, Position: "8"}),
		referenceImplicitError("SELECT num_nulls(1) FILTER (WHERE TRUE) FROM t", referenceDiagnostic{Code: "42809", Message: "FILTER specified, but num_nulls is not an aggregate function", Position: "8"}),
		referenceImplicitError("SELECT num_nulls(a => 1) FROM t", referenceDiagnostic{Code: "42883", Message: "function num_nulls(a => integer) does not exist", Hint: hint, Position: "8"}),
		referenceImplicitError("SELECT current_setting(num_nulls(NULL))", referenceDiagnostic{Code: "42883", Message: "function current_setting(integer) does not exist", Hint: hint, Position: "8"}),
	}
	for _, step := range steps {
		for _, prepared := range []bool{false, true} {
			step.prepared = prepared
			if step.state != "" {
				step.selectRows = true
				step.errorStage = "execute"
				if prepared {
					step.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
		}
	}
	for _, prepared := range []bool{false, true} {
		insert := referenceExec("INSERT INTO t VALUES(num_nulls(NULL),1)", 1)
		insert.prepared = prepared
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, insert)
		bag := referenceSelect("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(1)}})
		bag.bag = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, bag)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DELETE FROM t WHERE num_nonnulls=1", 1))
	}
}

// REL_18_0 misc.c count_nulls counts PG_ARGISNULL for ordinary arguments;
// only explicit VARIADIC counts array members. Expectations are source-derived
// independently of native results.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/misc.c
func TestReferenceNullCountArrayComposition(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceSelect("SELECT num_nulls(ARRAY[1,NULL]) AS n,num_nonnulls(ARRAY[1,NULL]) AS p", []string{"n", "p"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}}),
		referenceSelect("SELECT num_nulls(NULL::int[]) AS n,num_nonnulls(NULL::int[]) AS p,num_nulls(ARRAY[]::int[]) AS en,num_nonnulls(ARRAY[]::int[]) AS ep,num_nulls(ARRAY[NULL::int]) AS cn,num_nonnulls(ARRAY[NULL::int]) AS cp", []string{"n", "p", "en", "ep", "cn", "cp"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(0), int64(1), int64(0), int64(1)}}),
	} {
		for _, prepared := range []bool{false, true} {
			step.prepared = prepared
			p.step(t, step)
		}
	}
	for _, tc := range []struct {
		value           any
		nulls, nonnulls int64
	}{
		{nil, 1, 0}, {"{}", 0, 1}, {"{NULL}", 0, 1}, {"{1,NULL}", 0, 1},
	} {
		for _, prepared := range []bool{false, true} {
			step := referenceSelect("SELECT num_nulls($1::int[]) AS n,num_nonnulls($1::int[]) AS p", []string{"n", "p"}, []string{"INT4", "INT4"}, [][]any{{tc.nulls, tc.nonnulls}}, tc.value)
			step.prepared = prepared
			p.step(t, step)
		}
	}
	for _, backend := range p.backends() {
		for _, fn := range []struct{ name, position string }{{"num_nulls", "27"}, {"num_nonnulls", "30"}} {
			query := "SELECT " + fn.name + "(VARIADIC ARRAY[1,NULL])"
			for _, prepared := range []bool{false, true} {
				step := referenceSelect(query, []string{fn.name}, []string{"INT4"}, [][]any{{int64(1)}})
				if !backend.pg {
					diagnostic := controlsReferenceDiagnostic("0A000", "VARIADIC arrays in null-count arguments are unsupported")
					diagnostic.Position = fn.position
					step = referenceErrorDiagnostic(query, diagnostic)
					step.selectRows = true
					step.errorStage = "execute"
					if prepared {
						step.errorStage = "prepare"
					}
				}
				step.prepared = prepared
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
		}
	}
}
