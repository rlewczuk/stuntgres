package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
	"time"
)

func timestampBranchSmoke(t *testing.T, ctx context.Context, db *sql.DB,
	exec func(string, int64, ...any), check func(string, []string, []string, [][]any, ...any)) {
	t.Helper()
	timestampTimeFractionSmoke(t, ctx, db, exec, check)
	exec("CREATE TABLE branch_forms(id INTEGER,v TIMESTAMP(6) WITHOUT TIME ZONE)", 0)
	param, err := db.PrepareContext(ctx, "INSERT INTO branch_forms VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer param.Close()
	want := [][]any{}
	for index, tc := range []struct {
		input string
		want  any
	}{
		{"4714-11-23 250000 BC", time.Date(-4713, 11, 24, 1, 0, 0, 0, time.UTC)},
		{"J .1", time.Date(-4713, 11, 24, 2, 24, 0, 0, time.UTC)},
		{"J .", time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)},
		{"1999 January 04:05:06 8.5", time.Date(1999, 1, 8, 4, 5, 6, 500000000, time.UTC)},
		{"January 8 04:05:06 99.1", time.Date(99, 1, 8, 4, 5, 6, 100000000, time.UTC)},
		{"1999-01-08 119999 PM", time.Date(1999, 1, 9, 0, 40, 39, 0, time.UTC)},
		{"PST dst January 8 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"EDT January 8 1999", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"epoch BC PM Friday dst", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"- InFiNiTy BC PM Friday dst", []byte("-infinity")},
		{"January 8 04:05:06 120000 PST", time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999 01 8.1", time.Date(1999, 1, 8, 0, 0, 0, 100000000, time.UTC)},
		{"1999 January 8.1", time.Date(1999, 1, 8, 0, 0, 0, 100000000, time.UTC)},
		{"January 8 99.1", time.Date(99, 1, 8, 0, 0, 0, 100000000, time.UTC)},
		{"on epoch", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"epoch at", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"on infinity", []byte("infinity")},
		{"infinity at", []byte("infinity")},
		{"on +InFiNiTy", []byte("infinity")},
		{"+InFiNiTy at", []byte("infinity")},
		{"on - InFiNiTy", []byte("-infinity")},
		{"- InFiNiTy at", []byte("-infinity")},
		{"1999 1 8.5", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
		{"1999 1.50 8", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
		{"1999 1.00 8", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"1999 January 8.5", time.Date(1999, 1, 8, 0, 0, 0, 500000000, time.UTC)},
		{"1999 1 8.5 04:05:06", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999 1 8.", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"1999-01-08 250000", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
		{"1999-01-08 2500", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
		{"1999-01-08 250000-8", time.Date(1999, 1, 9, 1, 0, 0, 0, time.UTC)},
		{"1999-01-08 049900", time.Date(1999, 1, 8, 5, 39, 0, 0, time.UTC)},
		{"1999-01-08 040599", time.Date(1999, 1, 8, 4, 6, 39, 0, time.UTC)},
		{"1999-01-08 250000.5", time.Date(1999, 1, 9, 1, 0, 0, 500000000, time.UTC)},
		{"1999-12-31 250000", time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)},
		{"J on 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"J UTC 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"J +02 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"J Friday 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"J BC 2451187", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"J PM 2451187", time.Date(1999, 1, 8, 12, 0, 0, 0, time.UTC)},
		{"1999-01-08 T on 040506", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 T 040506-8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 PST dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 dst PST", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 UTC dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 dst UTC", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 +02 dst", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"1999-01-08 04:05:06 dst +02", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08 ", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"1999-01-08!", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"PST 1999-01-08", time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)},
		{"1999-01-08 04:05:06 + 8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+ InFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 +\t8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+\tInFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 +\n8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+\nInFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 +\r8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+\rInFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 +\f8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+\fInFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 +\u000b8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"+\u000bInFiNiTy", []byte("infinity")},
		{"1999-01-08 04:05:06 - 8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"- InFiNiTy", []byte("-infinity")},
		{"1999-01-08 04:05:06 -\t8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"-\tInFiNiTy", []byte("-infinity")},
		{"1999-01-08 04:05:06 -\n8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"-\nInFiNiTy", []byte("-infinity")},
		{"1999-01-08 04:05:06 -\r8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"-\rInFiNiTy", []byte("-infinity")},
		{"1999-01-08 04:05:06 -\f8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"-\fInFiNiTy", []byte("-infinity")},
		{"1999-01-08 04:05:06 -\u000b8", time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC)},
		{"-\u000bInFiNiTy", []byte("-infinity")},
	} {
		base := index*4 + 1
		exec(fmt.Sprintf("INSERT INTO branch_forms VALUES(%d,'%s')", base, tc.input), 1)
		stmt, err := db.PrepareContext(ctx, fmt.Sprintf("INSERT INTO branch_forms VALUES(%d,TIMESTAMP(6) WITHOUT TIME ZONE '%s')", base+1, tc.input))
		if err != nil {
			t.Fatal(tc.input, err)
		}
		result, err := stmt.ExecContext(ctx)
		stmt.Close()
		if err != nil {
			t.Fatal(tc.input, err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		exec("INSERT INTO branch_forms VALUES($1,$2)", 1, base+2, tc.input)
		result, err = param.ExecContext(ctx, base+3, tc.input)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		for j := range 4 {
			want = append(want, []any{int64(base + j), tc.want})
		}
	}
	param.Close()
	check("SELECT id,v FROM branch_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want)
	check("SELECT v FROM branch_forms WHERE v=$1", []string{"v"}, []string{"TIMESTAMP"}, nil, nil)
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
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
	check("SELECT id,v FROM branch_forms ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, want)
	check("SELECT v FROM branch_forms WHERE v='120000-01-08 04:05:06'", []string{"v"}, []string{"TIMESTAMP"}, [][]any{
		{time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)},
		{time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)}, {time.Date(120000, 1, 8, 4, 5, 6, 0, time.UTC)},
	})
	var infinity []byte
	if err := db.QueryRowContext(ctx, "SELECT v FROM branch_forms WHERE v='infinity' ORDER BY id").Scan(&infinity); err != nil {
		t.Fatal(err)
	}
	infinity[0] = 'X'
	check("SELECT TIMESTAMP 'on infinity at' FROM branch_forms WHERE id=1", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{[]byte("infinity")}})
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmt, err := conn.PrepareContext(ctx, "SELECT TIMESTAMP 'on NoW at' FROM branch_forms WHERE id=1")
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
	if err = tx.QueryRowContext(ctx, "SELECT TIMESTAMP 'now',TIMESTAMP 'on NoW at' FROM branch_forms WHERE id=1").Scan(&ordinary, &noisy); err != nil || ordinary != noisy || !ordinary.After(first) {
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

func timestampTimeFractionSmoke(t *testing.T, ctx context.Context, db *sql.DB,
	exec func(string, int64, ...any), check func(string, []string, []string, [][]any, ...any)) {
	t.Helper()
	exec("CREATE TABLE fraction_params(id INTEGER,v TIMESTAMP,w TIMESTAMP(6) WITHOUT TIME ZONE)", 0)
	insert := "INSERT INTO fraction_params VALUES($1,$2,$2)"
	stmt, err := db.PrepareContext(ctx, insert)
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
		t.Logf("fraction-%s: text and GoTime, direct and prepared, omitted and p6", tc.name)
		for _, input := range []any{tc.text, tc.raw} {
			for _, prepared := range []bool{false, true} {
				id++
				if prepared {
					result, err := stmt.ExecContext(ctx, id, input)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				} else {
					exec(insert, 1, id, input)
				}
				check("SELECT v,w FROM fraction_params WHERE id=$1", []string{"v", "w"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{tc.want, tc.want}}, id)
				matches[tc.want] = append(matches[tc.want], []any{int64(id)})
				for _, column := range []string{"v", "w"} {
					check("SELECT id FROM fraction_params WHERE "+column+"=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, matches[tc.want], tc.want)
				}
			}
		}
	}
}
