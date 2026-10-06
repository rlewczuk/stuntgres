package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampBranchErrorsAbortTransaction(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
	for _, tc := range timestampBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.Exec("INSERT INTO t VALUES('2000-01-01'),($1)", tc.input)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
			for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES('epoch')"} {
				_, err = tx.Exec(q)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			timestampQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
		})
	}
}

func TestTimestampBranchCanonicalControlsAndComparison(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIMESTAMP(6))")
	execSQL(t, db, "INSERT INTO t VALUES(1,'on - InFiNiTy at'),(2,'on epoch at'),(3,'1999-01-08 250000'),(4,'January 8 04:05:06 120000 PST'),(5,'+ InFiNiTy at'),(6,NULL)")
	want := [][]any{{int64(1), []byte("-infinity")}, {int64(2), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {int64(3), time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)}, {int64(4), time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {int64(5), []byte("infinity")}, {int64(6), nil}}
	check := func() {
		timestampQuery(t, db, "SELECT id,v FROM t ORDER BY v", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want)
		timestampQuery(t, db, "SELECT id FROM t WHERE v='120000-01-08 04:05:06'", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}})
		timestampQuery(t, db, "SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)
	}
	check()
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"'120000-01-08 04:05:06'", "'1999-01-09 01:00:00'", "'infinity'", "'-infinity'", "TIMESTAMP(6)"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(again.String(), err)
	}
	check()
	var infinity []byte
	if err := db.QueryRowContext(ctx, "SELECT TIMESTAMP 'on infinity at' FROM t WHERE id=1").Scan(&infinity); err != nil {
		t.Fatal(err)
	}
	infinity[0] = 'X'
	var original []byte
	if err := db.QueryRowContext(ctx, "SELECT TIMESTAMP 'on infinity at' FROM t WHERE id=1").Scan(&original); err != nil || string(original) != "infinity" {
		t.Fatal(string(original), err)
	}
}

func TestTimestampNoisyNowPhysicalPrepare(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIMESTAMP)")
	execSQL(t, db, "INSERT INTO t VALUES('epoch')")
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
	if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || held != first {
		t.Fatal(first, held, err)
	}
	var ordinary, noisy time.Time
	if err = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'on NoW at' FROM t").Scan(&ordinary, &noisy); err != nil || ordinary != noisy || !ordinary.After(first) {
		t.Fatal(first, ordinary, noisy, err)
	}
	fresh := tx.StmtContext(ctx, stmt)
	defer fresh.Close()
	if err = fresh.QueryRowContext(ctx).Scan(&held); err != nil || held != ordinary {
		t.Fatal(held, ordinary, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = stmt.QueryRowContext(ctx).Scan(&held); err != nil || held != first {
		t.Fatal(held, first, err)
	}
}

func TestTimestampTimeFractionConversion(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE fraction_params(id INTEGER,v TIMESTAMP,w TIMESTAMP(6) WITHOUT TIME ZONE)")
	insert := "INSERT INTO fraction_params VALUES($1,$2,$2)"
	stmt, err := db.Prepare(insert)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
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
					if prepared {
						result, err := stmt.Exec(id, input)
						if err != nil {
							t.Fatal(err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 1 {
							t.Fatal(n, err)
						}
					} else {
						execSQL(t, db, insert, id, input)
					}
					timestampQuery(t, db, "SELECT v,w FROM fraction_params WHERE id=$1", []string{"v", "w"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{tc.want, tc.want}}, id)
					matches[tc.want] = append(matches[tc.want], []any{int64(id)})
					for _, column := range []string{"v", "w"} {
						timestampQuery(t, db, "SELECT id FROM fraction_params WHERE "+column+"=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, matches[tc.want], tc.want)
					}
				}
			}
		})
	}
}
