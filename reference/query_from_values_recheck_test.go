package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceQueryFromValuesPhysicalReplacement(t *testing.T) {
	const values = "(VALUES(7,nextval('s')),(7,nextval('s')),(9,nextval('s')) ORDER BY column2 DESC OFFSET 1 LIMIT 2) v(n,token)"
	for _, source := range []string{"physical_left", "physical_right", "stored_view"} {
		for _, prepared := range []bool{false, true} {
			for _, rejected := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/rejected=%t", source, prepared, rejected), func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							a, b := referenceLockConnections(t, p.ctx, backend.db)
							referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
							referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,30)", 2)
							referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s START 1 CACHE 1", 0)
							from := "t CROSS JOIN " + values
							if source != "physical_left" {
								from = values + " CROSS JOIN t"
							}
							query := "SELECT t.v+1 AS val,v.n,v.token FROM " + from + " WHERE t.id=1"
							if rejected {
								query += " AND t.v=10"
							}
							if source == "stored_view" {
								referenceLockExec(t, p.ctx, a, "CREATE VIEW w AS SELECT t.id,t.v+1 AS val,v.n,v.token FROM "+from, 0)
								query = "SELECT val,n,token FROM w WHERE id=1"
								if rejected {
									query += " AND val=11"
								}
							}
							query += " ORDER BY token FOR UPDATE"
							var held *sql.Stmt
							var reader referenceExecutor = b
							if prepared {
								var err error
								held, err = b.PrepareContext(p.ctx, query)
								if err != nil {
									t.Fatal(err)
								}
								defer held.Close()
								reader = referenceHeldStatement{b, held, query}
							}
							referenceRunStep(t, p.ctx, a, backend.recorder, referenceSelect("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
							ap, bp := 0, 0
							if backend.pg {
								ap, bp = referenceRowPIDs(t, p.ctx, a, b)
							}
							holder, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							referenceLockExec(t, p.ctx, holder, "UPDATE t SET v=20 WHERE id=1", 1)
							done := referenceRowAsync(t, p.ctx, reader, query, false, true, backend.recorder.rawBackend)
							if backend.pg {
								referenceRowWait(t, p.ctx, bp, ap)
							} else {
								referenceLimitSequenceCapture(t, p.ctx, holder)
							}
							if err := holder.Commit(); err != nil {
								t.Fatal(err)
							}
							outcome := referenceLockResult(t, done)
							tokens, sequence := []int64{1, 2}, int64(3)
							if backend.pg {
								tokens, sequence = []int64{4, 7}, 9
							}
							if rejected {
								tokens, sequence = nil, 3
							}
							referenceValuesReplacementRows(t, outcome, tokens...)
							checkSequence := func(want int64) {
								referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{want, true}}))
							}
							checkSequence(sequence)
							if prepared {
								tokens, sequence = []int64{4, 5}, 6
								if backend.pg {
									tokens, sequence = []int64{10, 11}, 12
								}
								if rejected {
									tokens = nil
									if backend.pg {
										sequence = 3
									}
								}
								referenceValuesReplacementRows(t, referenceRowQuery(p.ctx, reader, query, false, backend.recorder.rawBackend), tokens...)
								checkSequence(sequence)
							}
							referenceRunStep(t, p.ctx, a, backend.recorder, referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}, {int64(2), int64(30)}}))
						})
					}
				})
			}
		}
	}
}

// PostgreSQL EPQ replays this volatile operand; native rechecks retain its
// eagerly captured occurrence. Both outcomes are prescribed independently.
func referenceValuesReplacementRows(t *testing.T, outcome relationOutcome, tokens ...int64) {
	t.Helper()
	if outcome.stmt != nil {
		defer outcome.stmt.Close()
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.rows == nil {
		t.Fatal("missing rows")
	}
	defer outcome.rows.Close()
	columns, err := outcome.rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := outcome.rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(metadata))
	for i := range metadata {
		types[i] = metadata[i].DatabaseTypeName()
	}
	if !reflect.DeepEqual(columns, []string{"val", "n", "token"}) || !reflect.DeepEqual(types, []string{"INT4", "INT4", "INT8"}) {
		t.Fatal(columns, types)
	}
	rows := [][]any{}
	for outcome.rows.Next() {
		row, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			pointers[i] = &row[i]
		}
		if err := outcome.rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = referenceNormalize(row[i])
		}
		rows = append(rows, row)
	}
	if err := outcome.rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := outcome.rows.Close(); err != nil {
		t.Fatal(err)
	}
	want := make([][]any, len(tokens))
	for i, token := range tokens {
		want[i] = []any{int64(21), int64(7), token}
	}
	if _, matches := referenceCompareRows(rows, want, false); !matches {
		t.Fatal("replacement rows", rows, "want", want)
	}
}
