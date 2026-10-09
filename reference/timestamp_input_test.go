package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func newTimestampReferenceScenario(t *testing.T) *referencePair {
	t.Helper()
	p := newReferenceScenario(t)
	if p.postgres != nil {
		p.postgres.SetMaxOpenConns(1)
		if _, err := p.postgres.ExecContext(p.ctx, "SET TIME ZONE 'UTC'; SET DateStyle TO 'ISO, MDY'"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestReferenceTimestampDocumentedInputs(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE anchor(n INTEGER)", 0))
			run(referenceExec("INSERT INTO anchor VALUES(1)", 1))
			run(referenceExec("CREATE TABLE t(id INTEGER,v TIMESTAMP)", 0))
			insertQuery := "INSERT INTO t VALUES($1,$2)"
			stmt, err := b.db.PrepareContext(p.ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			heldInsert := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: insertQuery}
			for i, tc := range timestampReferenceInputs {
				t.Run(tc.input, func(t *testing.T) {
					literal := referenceSelect("SELECT TIMESTAMP WITHOUT TIME ZONE '"+tc.input+"' FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})
					run(literal)
					run(referencePrepared(literal))
					insert := referenceExec("INSERT INTO t VALUES($1,$2)", 1, i+1, tc.input)
					run(insert)
					referenceTemporalRunStep(t, p.ctx, heldInsert, b.recorder, insert)
					run(referencePrepared(referenceSelect("SELECT v FROM t WHERE id=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{tc.want}, {tc.want}}, i+1)))
				})
			}
		})
	}
}

func TestReferenceTimestampHeldCorpusComparisons(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
			insertQuery, query := "INSERT INTO t VALUES($1)", "SELECT v FROM t WHERE v=$1"
			insert, err := b.db.PrepareContext(p.ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insert.Close(); err != nil {
					t.Error(err)
				}
			}()
			held, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := referenceHeldStatement{referenceExecutor: b.db, statement: insert, query: insertQuery}
			for _, tc := range timestampReferenceInputs {
				t.Run(tc.input, func(t *testing.T) {
					run(referenceExec(insertQuery, 1, tc.input))
					referenceTemporalRunStep(t, p.ctx, x, b.recorder, referenceExec(insertQuery, 1, tc.input))
					rows, err := held.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), tc.input)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMP" {
						t.Fatal(meta, err)
					}
					referenceTemporalMetadata(t, b.pg, meta)
					var expected [][]any
					for rows.Next() {
						var got any
						if err := rows.Scan(&got); err != nil {
							t.Fatal(err)
						}
						referenceAssertRawValue(t, b.pg, "TIMESTAMP", got)
						if stamp, ok := got.(time.Time); ok {
							_, offset := stamp.Zone()
							if offset != 0 {
								t.Fatal(stamp)
							}
						}
						if !reflect.DeepEqual(referenceNormalize(got), referenceNormalize(tc.want)) {
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
					referenceTemporalMetadata(t, b.pg, meta)
					referenceTemporalRecorded(t, b.recorder, referenceSelect(query, []string{"v"}, []string{"TIMESTAMP"}, expected))
				})
			}
		})
	}
}
