package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampBranchErrorsAbortTransaction(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampStep(t, p, fixture.Exec("CREATE TABLE t(v TIMESTAMP)", 0))
	referenceTimestampStep(t, p, fixture.Exec("INSERT INTO t VALUES('epoch')", 1))
	for _, backend := range p.Backends() {
		db := backend.DB
		recorder := backend.Recorder
		for _, tc := range timestampBadInputs {
			t.Run(fmt.Sprintf("%s/%s", backend.Name, tc.input), func(t *testing.T) {
				tx, err := db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
				step := fixture.TransactionError(false, "INSERT INTO t VALUES('2000-01-01'),($1)", want)
				step.Args = []any{tc.input}
				pg := want
				pg.Context = "unnamed portal parameter $1 = '...'"
				step.Diagnostic = &pg
				step.ErrorStage = "execute"
				fixture.TemporalRunStep(t, p.Ctx, tx, recorder, step)
				for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES('epoch')"} {
					fixture.TemporalRunStep(t, p.Ctx, tx, recorder, fixture.TransactionError(false, q, fixture.TransactionAborted()))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.TemporalRunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}))
			})
		}
	}
}

func TestReferenceTimestampBranchCanonicalControlsAndComparison(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampStep(t, p, fixture.Exec("CREATE TABLE t(id INTEGER,v TIMESTAMP(6))", 0))
	referenceTimestampStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)", 6))
	want := [][]any{{int64(1), "-infinity"}, {int64(2), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {int64(3), time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)}, {int64(4), time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {int64(5), "infinity"}, {int64(6), nil}}
	referenceTimestampStep(t, p, fixture.Select("SELECT id,v FROM t ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want))
	referenceTimestampStep(t, p, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, "120000-01-08 04:05:06")))
	referenceTimestampStep(t, p, fixture.Select("SELECT id FROM t WHERE v='120000-01-08 04:05:06'", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
	referenceTimestampStep(t, p, fixture.Select("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil))
	// Native controls are tested through the public API; dumped canonical SQL
	// is also an ordinary input to PostgreSQL, not a fictional control parity.
	saved := "CREATE TABLE saved_branch(id INTEGER,v TIMESTAMP(6)); INSERT INTO saved_branch VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)"
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(saved)); err != nil {
		t.Fatal(err)
	}
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"120000-01-08 04:05:06", "1999-01-09 01:00:00", "1970-01-01 00:00:00", "infinity", "NULL"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(again.String(), err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	referenceTimestampStep(t, p, fixture.Select("SELECT id,v FROM saved_branch ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want))
	for _, b := range p.Backends() {
		for _, tc := range []struct{ query, column string }{
			{"SELECT v FROM saved_branch WHERE v='infinity'", "v"},
			{"SELECT TIMESTAMP 'on infinity at' FROM saved_branch WHERE id=1", "timestamp"},
		} {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			rows, err := b.DB.QueryContext(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing owned infinity")
			}
			var raw any
			var owned []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, b.PG, "TIMESTAMP", raw)
			if value, ok := raw.([]byte); !ok || string(value) != "infinity" {
				rows.Close()
				t.Fatalf("raw infinity %T %#v", raw, raw)
			}
			if err := rows.Scan(&owned); err != nil || string(owned) != "infinity" {
				rows.Close()
				t.Fatal(string(owned), err)
			}
			step := fixture.Select(tc.query, []string{tc.column}, []string{"TIMESTAMP"}, [][]any{{"infinity"}})
			referenceTimestampConsume(t, rows, b.PG, fixture.Select(tc.query, step.Columns, step.Types, nil))
			owned[0] = 'X'
			fixture.TemporalRecorded(t, b.Recorder, step)
			fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
	}

}

func TestReferenceTimestampNoisyNowPhysicalPrepare(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampStep(t, p, fixture.Exec("CREATE TABLE t(v TIMESTAMP)", 0))
	referenceTimestampStep(t, p, fixture.Exec("INSERT INTO t VALUES('epoch')", 1))
	for _, backend := range p.Backends() {
		db := backend.DB
		t.Run(backend.Name, func(t *testing.T) {
			recorder := backend.Recorder
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, recorder)
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			stmt, err := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'on NoW at' FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var first, held time.Time
			if err = stmt.QueryRowContext(ctx).Scan(&first); err != nil {
				t.Fatal(err)
			}
			fixture.TemporalAdvanceClock(t, ctx, first)
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || !held.Equal(first) {
				t.Fatal(first, held, err)
			}
			var ordinary, noisy time.Time
			if err = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'on NoW at' FROM t").Scan(&ordinary, &noisy); err != nil || !ordinary.Equal(noisy) || !ordinary.After(first) {
				t.Fatal(first, ordinary, noisy, err)
			}
			fresh := tx.StmtContext(ctx, stmt)
			defer fresh.Close()
			if err = fresh.QueryRowContext(ctx).Scan(&held); err != nil || !held.Equal(ordinary) {
				t.Fatal(held, ordinary, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || !held.Equal(first) {
				t.Fatal(held, first, err)
			}
		})
	}
}

func TestReferenceTimestampTimeFractionConversion(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE fraction_params(id INTEGER,v TIMESTAMP,w TIMESTAMP(6) WITHOUT TIME ZONE)", 0))
			insertQuery := "INSERT INTO fraction_params VALUES($1,$2,$2)"
			insertStmt, err := b.DB.PrepareContext(p.Ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insertStmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			heldInsert := fixture.HeldStatement{Executor: b.DB, Statement: insertStmt, SQL: insertQuery}
			id := 0
			matches := make(map[time.Time][][]any)
			for _, tc := range []struct {
				name, text string
				raw, want  time.Time
			}{
				{"midpoint-down", "2000-01-01 00:00:00.0001255", time.Date(2000, 1, 1, 0, 0, 0, 125500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 125000, time.UTC)},
				{"midpoint-up", "2000-01-01 00:00:00.0001265", time.Date(2000, 1, 1, 0, 0, 0, 126500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 127000, time.UTC)},
				{"ordinary-even", "2000-01-01 00:00:00.0000005", time.Date(2000, 1, 1, 0, 0, 0, 500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"ordinary-odd", "2000-01-01 00:00:00.0000015", time.Date(2000, 1, 1, 0, 0, 0, 1500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 2000, time.UTC)},
				{"zero", "2000-01-01 00:00:00", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"year-carry", "1999-12-31 23:59:59.9999995", time.Date(1999, 12, 31, 23, 59, 59, 999999500, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"negative-epoch-down", "1999-12-31 23:59:59.0001255", time.Date(1999, 12, 31, 23, 59, 59, 125500, time.UTC), time.Date(1999, 12, 31, 23, 59, 59, 125000, time.UTC)},
				{"negative-epoch-up", "1999-12-31 23:59:59.0001265", time.Date(1999, 12, 31, 23, 59, 59, 126500, time.UTC), time.Date(1999, 12, 31, 23, 59, 59, 127000, time.UTC)},
				{"bc-max-offset", "0001-10-19 10:23:54.0001255+15:59:59 BC", time.Date(0, 10, 19, 10, 23, 54, 125500, time.FixedZone("offset", 57599)), time.Date(0, 10, 19, 10, 23, 54, 125000, time.UTC)},
				{"bc-min-offset", "0001-10-19 10:23:54.0001265-15:59:59 BC", time.Date(0, 10, 19, 10, 23, 54, 126500, time.FixedZone("offset", -57599)), time.Date(0, 10, 19, 10, 23, 54, 127000, time.UTC)},
				{"finite-min", "4714-11-24 00:00:00.0001255 BC", time.Date(-4713, 11, 24, 0, 0, 0, 125500, time.UTC), time.Date(-4713, 11, 24, 0, 0, 0, 125000, time.UTC)},
				{"finite-last", "294276-12-31 23:59:59.999999499", time.Date(294276, 12, 31, 23, 59, 59, 999999499, time.UTC), time.Date(294276, 12, 31, 23, 59, 59, 999999000, time.UTC)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, input := range []any{tc.text, tc.raw} {
						for _, prepared := range []bool{false, true} {
							id++
							insert := fixture.Exec(insertQuery, 1, id, input)
							selectValues := fixture.Select("SELECT v,w FROM fraction_params WHERE id=$1", []string{"v", "w"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{tc.want, tc.want}}, id)
							if prepared {
								fixture.TemporalRunStep(t, p.Ctx, heldInsert, b.Recorder, insert)
								selectValues = fixture.Prepared(selectValues)
							}
							if !prepared {
								run(insert)
							}
							run(selectValues)
							matches[tc.want] = append(matches[tc.want], []any{int64(id)})
							for _, column := range []string{"v", "w"} {
								equality := fixture.Select("SELECT id FROM fraction_params WHERE "+column+"=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, matches[tc.want], tc.want)
								if prepared {
									equality = fixture.Prepared(equality)
								}
								run(equality)
							}
						}
					}
				})
			}
		})
	}
}
