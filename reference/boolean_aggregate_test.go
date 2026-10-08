//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func booleanAggregateRegressionSteps() []referenceStep {
	labels := []string{"bool_and", "bool_or", "every"}
	types := []string{"BOOL", "BOOL", "BOOL"}
	steps := []referenceStep{
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='T'", labels, types, [][]any{{true, true, true}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='F'", labels, types, [][]any{{false, false, false}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='M'", labels, types, [][]any{{false, true, false}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='N'", labels, types, [][]any{{nil, nil, nil}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM empty", labels, types, [][]any{{nil, nil, nil}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE FALSE", labels, types, [][]any{{nil, nil, nil}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE NULL", labels, types, [][]any{{nil, nil, nil}}),
		referenceSelect("SELECT k,bool_and(b),bool_or(b),every(b) FROM t GROUP BY k", []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"T", true, true, true}, {"F", false, false, false}, {"M", false, true, false}, {"N", nil, nil, nil}}),
		referenceSelect("SELECT k,bool_and(b),bool_or(b),every(b) FROM empty GROUP BY k", []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, nil),
		referenceSelect("SELECT count(b),bool_and(b),bool_or(b),every(b),bool_and(b) FROM m", []string{"count", "bool_and", "bool_or", "every", "bool_and"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(2), false, true, false, false}}),
		referenceSelect("SELECT bool_and(b),bool_or(b),every(b),count(b) FROM nn", []string{"bool_and", "bool_or", "every", "count"}, []string{"BOOL", "BOOL", "BOOL", "INT8"}, [][]any{{nil, nil, nil, int64(0)}}),
		referenceSelect("SELECT bool_and('true'),bool_or(NULL) FROM m", []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}}),
	}
	for _, arg := range []any{true, false, nil} {
		steps = append(steps, referenceSelect("SELECT every($1) FROM m", []string{"every"}, []string{"BOOL"}, [][]any{{arg}}, arg))
	}
	for i := range steps {
		steps[i].bag = true
	}
	// Source-grounded records pending the root's pinned PostgreSQL 18.0 observations.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"bool_and", "bool_or", "every"} {
		for _, tc := range []struct {
			query, signature, position string
		}{
			{"SELECT " + name + "(1) FROM t", name + "(integer)", "8"},
			{"SELECT " + name + "('true'::text) FROM t", name + "(text)", "8"},
			{"SELECT " + name + "(txt) FROM t", name + "(text)", "8"},
			{"SELECT /*é*/ " + name + "(1) FROM empty WHERE FALSE", name + "(integer)", "14"},
			{"SELECT pg_catalog." + name + "(1) FROM t", "pg_catalog." + name + "(integer)", "8"},
			{"SELECT " + name + "() FROM t", name + "()", "8"},
			{"SELECT " + name + "(*) FROM t", name + "()", "8"},
		} {
			step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.position})
			step.selectRows = true
			steps = append(steps, step)
		}
	}
	for _, tc := range []struct {
		query string
		want  referenceDiagnostic
	}{
		{"SELECT bool_and('x') FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "x"`, Position: "17"}},
		{"SELECT bool_and(bool_or(b)) FROM t", referenceDiagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "17"}},
		{"SELECT bool_and(count(*)) FROM t", referenceDiagnostic{Code: "42883", Message: "function bool_and(bigint) does not exist", Hint: hint, Position: "8"}},
		{"SELECT b FROM t WHERE bool_and(b)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "23"}},
		{"SELECT b FROM t GROUP BY bool_and(b)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "26"}},
		{"SELECT b,bool_and(b) FROM t", referenceDiagnostic{Code: "42803", Message: `column "t.b" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"}},
	} {
		step := referenceImplicitError(tc.query, tc.want)
		step.selectRows = true
		steps = append(steps, step)
	}
	return steps
}

// The portable RowDescription fields are checked by referenceRunStep. lib/pq
// does not expose nullability; assert native nullability independently instead.
func booleanAggregateReferenceMetadata(t *testing.T, p *referencePair, db *sql.DB, native, prepared bool) {
	t.Helper()
	const query = "SELECT bool_and(b),bool_or(b),every(b),count(b) FROM nn"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := db.PrepareContext(p.ctx, query)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.ctx)
	} else {
		rows, err = db.QueryContext(p.ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 4 {
		t.Fatalf("column count = %d, want 4", len(types))
	}
	for i, typ := range types {
		if i < 3 && typ.ScanType() != reflect.TypeOf(false) {
			t.Errorf("column %d ScanType = %v, want bool", i, typ.ScanType())
		}
		if native {
			if nullable, known := typ.Nullable(); !known || nullable != (i < 3) {
				t.Errorf("column %d Nullable = %v,%v, want %v,true", i, nullable, known, i < 3)
			}
		}
	}
}

func TestReferenceBooleanAggregateRegression(t *testing.T) {
	p := newReferencePair(t)
	// Setup deliberately lives outside backend subtest selectors.
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(k TEXT,b BOOL,v INT,txt TEXT)", 0),
		referenceExec("INSERT INTO t VALUES('T',TRUE,1,'true'),('T',TRUE,1,'true'),('T',NULL,1,'true'),('F',FALSE,1,'true'),('F',FALSE,1,'true'),('F',NULL,1,'true'),('M',TRUE,1,'true'),('M',FALSE,1,'true'),('M',NULL,1,'true'),('N',NULL,1,'true'),('N',NULL,1,'true')", 11),
		referenceExec("CREATE TABLE empty(k TEXT,b BOOL,v INT,txt TEXT)", 0),
		referenceExec("CREATE TABLE m(b BOOL)", 0),
		referenceExec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3),
		referenceExec("CREATE TABLE nn(b BOOL NOT NULL)", 0),
	} {
		p.step(t, step)
	}
	t.Run("postgres_first", func(t *testing.T) {
		for _, step := range booleanAggregateRegressionSteps() {
			t.Run(step.query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) {
					if step.diagnostic != nil {
						step.errorStage = "execute"
					}
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				})
				t.Run("prepared", func(t *testing.T) {
					step = referencePrepared(step)
					if step.diagnostic != nil {
						step.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				})
			})
		}
		t.Run("metadata_direct", func(t *testing.T) { booleanAggregateReferenceMetadata(t, p, p.postgres, false, false) })
		t.Run("metadata_prepared", func(t *testing.T) { booleanAggregateReferenceMetadata(t, p, p.postgres, false, true) })
	})
	t.Run("paired", func(t *testing.T) {
		for _, step := range booleanAggregateRegressionSteps() {
			t.Run(step.query, func(t *testing.T) {
				t.Run("direct", func(t *testing.T) {
					if step.diagnostic != nil {
						step.errorStage = "execute"
					}
					p.step(t, step)
				})
				t.Run("prepared", func(t *testing.T) {
					step = referencePrepared(step)
					if step.diagnostic != nil {
						step.errorStage = "prepare"
					}
					p.step(t, step)
				})
			})
		}
		t.Run("native_metadata_direct", func(t *testing.T) { booleanAggregateReferenceMetadata(t, p, p.memory, true, false) })
		t.Run("native_metadata_prepared", func(t *testing.T) { booleanAggregateReferenceMetadata(t, p, p.memory, true, true) })
	})
}
