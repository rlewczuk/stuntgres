package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryFromValuesPhysicalReplacement(t *testing.T) {
	const values = "(VALUES(7,nextval('s')),(7,nextval('s')),(9,nextval('s')) ORDER BY column2 DESC OFFSET 1 LIMIT 2) v(n,token)"
	for _, source := range []string{"physical_left", "physical_right", "stored_view"} {
		for _, prepared := range []bool{false, true} {
			for _, rejected := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/rejected=%t", source, prepared, rejected), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,30)", 2)
							fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s START 1 CACHE 1", 0)
							from := "t CROSS JOIN " + values
							if source != "physical_left" {
								from = values + " CROSS JOIN t"
							}
							query := "SELECT t.v+1 AS val,v.n,v.token FROM " + from + " WHERE t.id=1"
							if rejected {
								query += " AND t.v=10"
							}
							if source == "stored_view" {
								fixture.LockExec(t, p.Ctx, a, "CREATE VIEW w AS SELECT t.id,t.v+1 AS val,v.n,v.token FROM "+from, 0)
								query = "SELECT val,n,token FROM w WHERE id=1"
								if rejected {
									query += " AND val=11"
								}
							}
							query += " ORDER BY token FOR UPDATE"
							var held *sql.Stmt
							var reader fixture.Executor = b
							if prepared {
								var err error
								held, err = b.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatal(err)
								}
								defer held.Close()
								reader = fixture.HeldStatement{b, held, query}
							}
							fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
							ap, bp := 0, 0
							if backend.PG {
								ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
							}
							holder, err := a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=20 WHERE id=1", 1)
							done := fixture.RowAsync(t, p.Ctx, reader, query, false, true, backend.Recorder.RawBackend)
							if backend.PG {
								fixture.RowWait(t, p.Ctx, bp, ap)
							} else {
								fixture.LimitSequenceCapture(t, p.Ctx, holder)
							}
							if err := holder.Commit(); err != nil {
								t.Fatal(err)
							}
							outcome := fixture.LockResult(t, done)
							tokens, sequence := []int64{1, 2}, int64(3)
							if backend.PG {
								tokens, sequence = []int64{4, 7}, 9
							}
							if rejected {
								tokens, sequence = nil, 3
							}
							referenceValuesReplacementRows(t, outcome, tokens...)
							checkSequence := func(want int64) {
								fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT last_value,is_called FROM s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{want, true}}))
							}
							checkSequence(sequence)
							if prepared {
								tokens, sequence = []int64{4, 5}, 6
								if backend.PG {
									tokens, sequence = []int64{10, 11}, 12
								}
								if rejected {
									tokens = nil
									if backend.PG {
										sequence = 3
									}
								}
								referenceValuesReplacementRows(t, fixture.RowQuery(p.Ctx, reader, query, false, backend.Recorder.RawBackend), tokens...)
								checkSequence(sequence)
							}
							fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}, {int64(2), int64(30)}}))
						})
					}
				})
			}
		}
	}
}

// PostgreSQL EPQ replays this volatile operand; native rechecks retain its
// eagerly captured occurrence. Both outcomes are prescribed independently.
func referenceValuesReplacementRows(t *testing.T, outcome fixture.RelationOutcome, tokens ...int64) {
	t.Helper()
	if outcome.Stmt != nil {
		defer outcome.Stmt.Close()
	}
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if outcome.Rows == nil {
		t.Fatal("missing rows")
	}
	defer outcome.Rows.Close()
	columns, err := outcome.Rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := outcome.Rows.ColumnTypes()
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
	for outcome.Rows.Next() {
		row, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			pointers[i] = &row[i]
		}
		if err := outcome.Rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = fixture.Normalize(row[i])
		}
		rows = append(rows, row)
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	want := make([][]any, len(tokens))
	for i, token := range tokens {
		want[i] = []any{int64(21), int64(7), token}
	}
	if _, matches := fixture.CompareRows(rows, want, false); !matches {
		t.Fatal("replacement rows", rows, "want", want)
	}
}
