package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func newTimestampReferenceScenario(t *testing.T) *fixture.Pair {
	t.Helper()
	p := fixture.NewScenario(t)
	if p.Postgres != nil {
		p.Postgres.SetMaxOpenConns(1)
		if _, err := p.Postgres.ExecContext(p.Ctx, "SET TIME ZONE 'UTC'; SET DateStyle TO 'ISO, MDY'"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestReferenceTimestampDocumentedInputs(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE anchor(n INTEGER)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			run(fixture.Exec("CREATE TABLE t(id INTEGER,v TIMESTAMP)", 0))
			insertQuery := "INSERT INTO t VALUES($1,$2)"
			stmt, err := b.DB.PrepareContext(p.Ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			heldInsert := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: insertQuery}
			for i, tc := range timestampReferenceInputs {
				t.Run(tc.input, func(t *testing.T) {
					literal := fixture.Select("SELECT TIMESTAMP WITHOUT TIME ZONE '"+tc.input+"' FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})
					run(literal)
					run(fixture.Prepared(literal))
					insert := fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, i+1, tc.input)
					run(insert)
					fixture.TemporalRunStep(t, p.Ctx, heldInsert, b.Recorder, insert)
					run(fixture.Prepared(fixture.Select("SELECT v FROM t WHERE id=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{tc.want}, {tc.want}}, i+1)))
				})
			}
		})
	}
}

func TestReferenceTimestampHeldCorpusComparisons(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(v TIMESTAMP)", 0))
			insertQuery, query := "INSERT INTO t VALUES($1)", "SELECT v FROM t WHERE v=$1"
			insert, err := b.DB.PrepareContext(p.Ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insert.Close(); err != nil {
					t.Error(err)
				}
			}()
			held, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := fixture.HeldStatement{Executor: b.DB, Statement: insert, SQL: insertQuery}
			for _, tc := range timestampReferenceInputs {
				t.Run(tc.input, func(t *testing.T) {
					run(fixture.Exec(insertQuery, 1, tc.input))
					fixture.TemporalRunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(insertQuery, 1, tc.input))
					rows, err := held.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), tc.input)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMP" {
						t.Fatal(meta, err)
					}
					fixture.TemporalMetadata(t, b.PG, meta)
					var expected [][]any
					for rows.Next() {
						var got any
						if err := rows.Scan(&got); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, "TIMESTAMP", got)
						if stamp, ok := got.(time.Time); ok {
							_, offset := stamp.Zone()
							if offset != 0 {
								t.Fatal(stamp)
							}
						}
						if !reflect.DeepEqual(fixture.Normalize(got), fixture.Normalize(tc.want)) {
							t.Fatal(got, tc.want)
						}
						expected = append(expected, []any{tc.want})
					}
					if err := rows.Err(); err != nil || len(expected) == 0 {
						t.Fatal("no held matches", err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.TemporalMetadata(t, b.PG, meta)
					fixture.TemporalRecorded(t, b.Recorder, fixture.Select(query, []string{"v"}, []string{"TIMESTAMP"}, expected))
				})
			}
		})
	}
}
