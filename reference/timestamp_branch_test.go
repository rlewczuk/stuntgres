//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceTimestampBranchErrorsAbortTransaction(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	for backend, db := range []*sql.DB{p.memory, p.postgres} {
		recorder := p.memoryRecord
		if backend == 1 {
			recorder = p.postgresRecord
		}
		for _, tc := range timestampBadInputs {
			t.Run(fmt.Sprintf("%d/%s", backend, tc.input), func(t *testing.T) {
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
				step := referenceTransactionError(false, "INSERT INTO t VALUES('2000-01-01'),($1)", want)
				step.args = []any{tc.input}
				pg := want
				pg.Context = "unnamed portal parameter $1 = '...'"
				step.diagnostic = &pg
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, tx, recorder, step)
				for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES('epoch')"} {
					referenceRunStep(t, p.ctx, tx, recorder, referenceTransactionError(false, q, referenceTransactionAborted()))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}))
			})
		}
	}
}

func TestReferenceTimestampBranchCanonicalControlsAndComparison(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TIMESTAMP(6))", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)", 6))
	want := [][]any{{int64(1), "-infinity"}, {int64(2), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {int64(3), time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)}, {int64(4), time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {int64(5), "infinity"}, {int64(6), nil}}
	p.step(t, referenceSelect("SELECT id,v FROM t ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, "120000-01-08 04:05:06")))
	p.step(t, referenceSelect("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil))
	// Native controls are tested through the public API; dumped canonical SQL
	// is also an ordinary input to PostgreSQL, not a fictional control parity.
	saved := "CREATE TABLE saved_branch(id INTEGER,v TIMESTAMP(6)); INSERT INTO saved_branch VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)"
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString(saved)); err != nil {
		t.Fatal(err)
	}
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(again.String(), err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT id,v FROM saved_branch ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want))
}

func TestReferenceTimestampNoisyNowPhysicalPrepare(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	for backend, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run(fmt.Sprint(backend), func(t *testing.T) {
			recorder := p.memoryRecord
			if backend == 1 {
				recorder = p.postgresRecord
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
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
			time.Sleep(3 * time.Millisecond)
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
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE fraction_params(id INTEGER,v TIMESTAMP,w TIMESTAMP(6) WITHOUT TIME ZONE)", 0))
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
					insert := referenceExec("INSERT INTO fraction_params VALUES($1,$2,$2)", 1, id, input)
					selectValues := referenceSelect("SELECT v,w FROM fraction_params WHERE id=$1", []string{"v", "w"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{tc.want, tc.want}}, id)
					if prepared {
						insert = referencePrepared(insert)
						selectValues = referencePrepared(selectValues)
					}
					p.step(t, insert)
					p.step(t, selectValues)
					matches[tc.want] = append(matches[tc.want], []any{int64(id)})
					for _, column := range []string{"v", "w"} {
						equality := referenceSelect("SELECT id FROM fraction_params WHERE "+column+"=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, matches[tc.want], tc.want)
						if prepared {
							equality = referencePrepared(equality)
						}
						p.step(t, equality)
					}
				}
			}
		})
	}
}
