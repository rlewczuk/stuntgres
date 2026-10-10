package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

var txTimesNames = []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"}

const txTimesProjection = "now(),transaction_timestamp(),statement_timestamp(),clock_timestamp(),timeofday()"

func txTimesScenario(t *testing.T, body func(*testing.T, *referencePair, *sql.Conn, bool, *referenceRecorder)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			datetimeCurrentExec(t, p.ctx, conn, "SET TIME ZONE 'UTC'", 0)
			originalContext := p.ctx
			p.ctx = context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			defer func() { p.ctx = originalContext }()
			body(t, p, conn, backend.pg, backend.recorder)
		})
	}
}

type txTimesSample struct {
	transaction, statement, wall time.Time
	text                         string
}

func txTimesReadStored(t *testing.T, ctx context.Context, x referenceExecutor, pg bool, q string, count, east int, simple bool) txTimesSample {
	t.Helper()
	return txTimesReadResult(t, ctx, x, pg, q, count, east, simple, true)
}
func txTimesRead(t *testing.T, ctx context.Context, x referenceExecutor, pg bool, q string, count, east int, simple bool, args ...any) txTimesSample {
	t.Helper()
	return txTimesReadResult(t, ctx, x, pg, q, count, east, simple, false, args...)
}

func txTimesReadResult(t *testing.T, ctx context.Context, x referenceExecutor, pg bool, q string, count, east int, simple, stored bool, args ...any) txTimesSample {
	t.Helper()
	before := time.Now()
	rows, err := x.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 5 {
		t.Fatalf("metadata=%v", meta)
	}
	for i, c := range meta {
		typ, scan := "TIMESTAMPTZ", reflect.TypeFor[time.Time]()
		if i == 4 {
			typ, scan = "TEXT", reflect.TypeFor[string]()
		}
		length, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		nullable, nk := c.Nullable()
		wantLength, wantLengthKnown := int64(0), i == 4
		if wantLengthKnown {
			wantLength = math.MaxInt64
		}
		wantNullable := stored && !pg
		if c.Name() != txTimesNames[i] || c.DatabaseTypeName() != typ || c.ScanType() != scan || lk != wantLengthKnown || dk || nk != wantNullable || length != wantLength || precision != 0 || scale != 0 || nullable != wantNullable {
			t.Errorf("descriptor %d: name=%q type=%q scan=%v length=%d/%v decimal=%d,%d/%v nullable=%v/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lk, precision, scale, dk, nullable, nk)
		}
	}
	var first txTimesSample
	n := 0
	for rows.Next() {
		var raw [5]any
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4]); err != nil {
			t.Fatal(err)
		}
		for i := range 4 {
			v, ok := raw[i].(time.Time)
			if !ok {
				t.Fatalf("raw[%d]=%T %#v", i, raw[i], raw[i])
			}
			_, off := v.Zone()
			if off != east || v.Nanosecond()%1000 != 0 {
				t.Errorf("instant[%d]=%v offset=%d want=%d microseconds", i, v, off, east)
			}
		}
		if _, ok := raw[4].(string); !ok {
			t.Errorf("TEXT raw=%T %#v want string", raw[4], raw[4])
		}
		var a, b, s, w time.Time
		var text string
		if err := rows.Scan(&a, &b, &s, &w, &text); err != nil {
			t.Fatal(err)
		}
		if !a.Equal(b) || a.After(s) || (simple && !a.Equal(s)) {
			t.Errorf("stable alias/boundary: now=%v transaction=%v statement=%v simple=%v", a, b, s, simple)
		}
		if w.Before(before.Add(-time.Microsecond)) || w.After(time.Now().Add(time.Microsecond)) {
			t.Errorf("wall=%v outside request bounds", w)
		}
		txTimesText(t, text, east, before, time.Now())
		if n == 0 {
			first = txTimesSample{a, s, w, text}
		} else if !a.Equal(first.transaction) || !s.Equal(first.statement) {
			t.Error("stable clocks changed within statement")
		}
		n++
	}
	if rows.Err() != nil || n != count {
		t.Fatalf("rows=%d want=%d error=%v", n, count, rows.Err())
	}
	return first
}

func txTimesText(t *testing.T, text string, east int, before, after time.Time) {
	t.Helper()
	split := strings.LastIndexByte(text, ' ')
	if split < 0 {
		t.Fatalf("timeofday=%q", text)
	}
	body := text[:split]
	parsed, err := time.ParseInLocation("Mon Jan 02 15:04:05.000000 2006", body, time.FixedZone("expected", east))
	if err != nil || len(body) != 31 {
		t.Fatalf("timeofday layout=%q error=%v", text, err)
	}
	if parsed.Before(before.Add(-time.Microsecond)) || parsed.After(after.Add(time.Microsecond)) {
		t.Errorf("timeofday=%q outside [%v,%v]", text, before, after)
	}
	if east == 0 && text[split+1:] != "UTC" {
		t.Errorf("timeofday suffix=%q want UTC", text[split+1:])
	}
}

func TestReferenceDatetimeTxTimesFormsMetadata(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, c *sql.Conn, pg bool, r *referenceRecorder) {
		for _, form := range []string{txTimesProjection, strings.ToUpper(txTimesProjection), `"now"(),"transaction_timestamp"(),"statement_timestamp"(),"clock_timestamp"(),"timeofday"()`, "pg_catalog.now(),pg_catalog.transaction_timestamp(),pg_catalog.statement_timestamp(),pg_catalog.clock_timestamp(),pg_catalog.timeofday()", `"pg_catalog"."now"(),"pg_catalog"."transaction_timestamp"(),"pg_catalog"."statement_timestamp"(),"pg_catalog"."clock_timestamp"(),"pg_catalog"."timeofday"()`} {
			q := "SELECT " + form
			txTimesRead(t, p.ctx, c, pg, q, 1, 0, true)
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			a := txTimesRead(t, p.ctx, held, pg, q, 1, 0, !pg)
			referenceTemporalAdvanceClock(t, p.ctx, time.Now())
			b := txTimesRead(t, p.ctx, held, pg, q, 1, 0, !pg)
			if !b.transaction.After(a.transaction) {
				t.Error("held autocommit retained previous birth")
			}
			stmt.Close()
		}
		txTimesRead(t, p.ctx, c, pg, "SELECT "+txTimesProjection+" LIMIT 0", 0, 0, true)
		empty := "SELECT " + txTimesProjection + " LIMIT 0"
		stmt, err := c.PrepareContext(p.ctx, empty)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		txTimesRead(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: empty}, pg, empty, 0, 0, !pg)
	})
}

func TestReferenceDatetimeTxTimesTransactionClock(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, c *sql.Conn, pg bool, r *referenceRecorder) {
		q := "SELECT " + txTimesProjection
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, driverBegin := range []bool{false, true} {
			before := time.Now()
			var tx *sql.Tx
			if driverBegin {
				tx, err = c.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
			} else {
				datetimeCurrentExec(t, p.ctx, c, "BEGIN", 0)
			}
			after := time.Now()
			var x referenceExecutor = c
			if tx != nil {
				x = tx
			}
			a := txTimesRead(t, p.ctx, x, pg, q, 1, 0, false)
			referenceRunStep(t, p.ctx, x, r, referenceSelect("SELECT now()=CURRENT_TIMESTAMP AS keyword_same,transaction_timestamp()=TIMESTAMPTZ 'now' AS literal_same", []string{"keyword_same", "literal_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			if a.transaction.Before(before.Add(-time.Microsecond)) || a.transaction.After(after.Add(time.Microsecond)) {
				t.Error("transaction birth is not BEGIN time", a.transaction, before, after)
			}
			for _, control := range []string{"SAVEPOINT tx_time_s", "ROLLBACK TO tx_time_s", "RELEASE tx_time_s"} {
				referenceTemporalAdvanceClock(t, p.ctx, time.Now())
				datetimeCurrentExec(t, p.ctx, x, control, 0)
				b := txTimesRead(t, p.ctx, x, pg, q, 1, 0, false)
				if !a.transaction.Equal(b.transaction) || !b.statement.After(a.statement) {
					t.Error("transaction/statement ownership", a, b)
				}
				a = b
			}
			if tx != nil {
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else {
				datetimeCurrentExec(t, p.ctx, c, "COMMIT", 0)
			}
			b := txTimesRead(t, p.ctx, held, pg, q, 1, 0, !pg)
			if !b.transaction.After(a.transaction) {
				t.Error("held execution retained ended transaction")
			}
		}
	})
}

func TestReferenceDatetimeTxTimesContexts(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, a *sql.Conn, pg bool, r *referenceRecorder) {
		var db *sql.DB = p.memory
		if pg {
			db = p.postgres
		}
		b, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		defer b.ExecContext(p.ctx, "ROLLBACK")
		datetimeCurrentExec(t, p.ctx, b, "SET TIME ZONE 'UTC'", 0)
		datetimeCurrentExec(t, p.ctx, a, "BEGIN", 0)
		q := "SELECT " + txTimesProjection
		old := txTimesRead(t, p.ctx, a, pg, q, 1, 0, false)
		referenceTemporalAdvanceClock(t, p.ctx, time.Now())
		datetimeCurrentExec(t, p.ctx, b, "BEGIN", 0)
		peer := txTimesRead(t, p.ctx, b, pg, q, 1, 0, false)
		if !peer.transaction.After(old.transaction) {
			t.Error("peer birth not independent")
		}
		datetimeCurrentExec(t, p.ctx, b, "COMMIT", 0)
		fresh := txTimesRead(t, p.ctx, b, pg, q, 1, 0, true)
		if !fresh.transaction.After(peer.transaction) {
			t.Error("peer implicit birth not fresh")
		}
		same := txTimesRead(t, p.ctx, a, pg, q, 1, 0, false)
		if !same.transaction.Equal(old.transaction) {
			t.Error("peer changed first owner")
		}
		datetimeCurrentExec(t, p.ctx, a, "COMMIT", 0)
		parameterized := txTimesRead(t, p.ctx, a, pg, q+" WHERE $1::INTEGER=1", 1, 0, !pg, 1)
		if !parameterized.transaction.After(old.transaction) {
			t.Error("parameterized autocommit retained ended birth")
		}
	})
}

func TestReferenceDatetimeTxTimesChain(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, c *sql.Conn, pg bool, r *referenceRecorder) {
		q := "SELECT " + txTimesProjection
		for _, end := range []string{"COMMIT", "ROLLBACK", "ABORTED_COMMIT"} {
			datetimeCurrentExec(t, p.ctx, c, "BEGIN", 0)
			a := txTimesRead(t, p.ctx, c, pg, q, 1, 0, false)
			if end == "ABORTED_COMMIT" {
				referenceState(t, execTxTimesError(p.ctx, c, "SELECT now(1)"), "42883")
				end = "COMMIT"
			}
			referenceTemporalAdvanceClock(t, p.ctx, time.Now())
			before := time.Now()
			datetimeCurrentExec(t, p.ctx, c, end+" AND CHAIN", 0)
			after := time.Now()
			b := txTimesRead(t, p.ctx, c, pg, q, 1, 0, false)
			if !b.transaction.After(a.transaction) || b.transaction.Before(before.Add(-time.Microsecond)) || b.transaction.After(after.Add(time.Microsecond)) {
				t.Error("CHAIN birth", a, b, before, after)
			}
			same := txTimesRead(t, p.ctx, c, pg, q, 1, 0, false)
			if !same.transaction.Equal(b.transaction) {
				t.Error("CHAIN unstable")
			}
			datetimeCurrentExec(t, p.ctx, c, "ROLLBACK", 0)
		}
	})
}
func execTxTimesError(ctx context.Context, c *sql.Conn, q string) error {
	_, err := c.ExecContext(ctx, q)
	return err
}

func TestReferenceDatetimeTxTimesEvaluationTiming(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, c *sql.Conn, pg bool, r *referenceRecorder) {
		datetimeCurrentExec(t, p.ctx, c, "CREATE TABLE tx_time_rows(n INTEGER)", 0)
		values := make([]string, 4096)
		for i := range values {
			values[i] = "(1)"
		}
		datetimeCurrentExec(t, p.ctx, c, "INSERT INTO tx_time_rows VALUES"+strings.Join(values, ","), 4096)
		rows, err := c.QueryContext(p.ctx, "SELECT "+txTimesProjection+" FROM tx_time_rows")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		clocks, texts := map[time.Time]bool{}, map[string]bool{}
		var stable, statement time.Time
		n := 0
		for rows.Next() {
			var a, b, s, w time.Time
			var text string
			if err := rows.Scan(&a, &b, &s, &w, &text); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				stable, statement = a, s
			}
			if !a.Equal(b) || !a.Equal(stable) || !s.Equal(statement) {
				t.Error("stable repeated rows changed")
			}
			clocks[w.UTC()] = true
			texts[text] = true
			n++
		}
		if rows.Err() != nil || n != 4096 || len(clocks) < 2 || len(texts) < 2 {
			t.Fatalf("demand rows=%d clocks=%d texts=%d err=%v", n, len(clocks), len(texts), rows.Err())
		}
		rows.Close()
		txTimesRead(t, p.ctx, c, pg, "SELECT "+txTimesProjection+" FROM tx_time_rows WHERE clock_timestamp()>=now() ORDER BY \"clock_timestamp\" LIMIT 1 OFFSET 4095", 1, 0, true)
		for _, q := range []string{"SELECT CASE WHEN true THEN 'safe' ELSE timeofday() END AS value", "SELECT lower(timeofday()) AS value WHERE false"} {
			rows, err := c.QueryContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				if s != "safe" {
					t.Error(s)
				}
			}
			if rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		}
	})
}

func TestReferenceDatetimeTxTimesTimeZone(t *testing.T) {
	txTimesScenario(t, func(t *testing.T, p *referencePair, c *sql.Conn, pg bool, r *referenceRecorder) {
		datetimeCurrentExec(t, p.ctx, c, "BEGIN", 0)
		q := "SELECT " + txTimesProjection
		a := txTimesRead(t, p.ctx, c, pg, q, 1, 0, false)
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		// Independent modern US rule: second Sunday March 07:00 UTC through
		// first Sunday November 06:00 UTC; no host timezone database oracle.
		now := time.Now().UTC()
		march := time.Date(now.Year(), 3, 1, 7, 0, 0, 0, time.UTC)
		november := time.Date(now.Year(), 11, 1, 6, 0, 0, 0, time.UTC)
		start := march.AddDate(0, 0, (7-int(march.Weekday()))%7+7)
		end := november.AddDate(0, 0, (7-int(november.Weekday()))%7)
		nyEast, nySuffix := -18000, "EST"
		if !now.Before(start) && now.Before(end) {
			nyEast, nySuffix = -14400, "EDT"
		}
		for _, zone := range []struct {
			name   string
			east   int
			suffix string
		}{{"'UTC'", 0, "UTC"}, {"'America/New_York'", nyEast, nySuffix}, {"5.5", 19800, "+05:30"}, {"'+05:30'", -19800, ""}, {"'-03'", -10800, "-03"}} {
			datetimeCurrentExec(t, p.ctx, c, "SET TIME ZONE "+zone.name, 0)
			datetimeCurrentExec(t, p.ctx, c, "SET DateStyle='ISO,MDY'", 0)
			b := txTimesRead(t, p.ctx, held, pg, q, 1, zone.east, false)
			if !a.transaction.Equal(b.transaction) || !strings.HasSuffix(b.text, " "+zone.suffix) {
				t.Errorf("zone=%s sample=%+v suffix want=%q", zone.name, b, zone.suffix)
			}
			for _, style := range []string{"ISO,MDY", "SQL,DMY", "Postgres,MDY"} {
				datetimeCurrentExec(t, p.ctx, c, "SET DateStyle='"+style+"'", 0)
				before := time.Now()
				var text, nowText, transactionText, witnessText string
				if err := c.QueryRowContext(p.ctx, "SELECT timeofday(),now()::TEXT,transaction_timestamp()::TEXT,CURRENT_TIMESTAMP::TEXT").Scan(&text, &nowText, &transactionText, &witnessText); err != nil {
					t.Fatal(err)
				}
				txTimesText(t, text, zone.east, before, time.Now())
				if nowText != witnessText || transactionText != witnessText || !strings.HasSuffix(text, " "+zone.suffix) {
					t.Errorf("DateStyle=%s text=%q aliases=%q/%q witness=%q", style, text, nowText, transactionText, witnessText)
				}
			}
		}
	})
}
