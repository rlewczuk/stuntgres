//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// Independently expect pq's Jan1 anchor and Jan2 endpoint, rather than dropping
// dates from arbitrary time.Time cells or changing timestamp normalization.
func referenceTimeRows(t *testing.T, postgres bool, types []string, rows [][]any) [][]any {
	t.Helper()
	if !postgres {
		return rows
	}
	result := make([][]any, len(rows))
	for i, row := range rows {
		result[i] = append([]any(nil), row...)
		for j, typ := range types {
			if typ != "TIME" || row[j] == nil {
				continue
			}
			clock := row[j].(string)
			if clock == "24:00:00" {
				result[i][j] = time.Date(0, time.January, 2, 0, 0, 0, 0, time.UTC)
				continue
			}
			value, err := time.Parse("15:04:05.999999", clock)
			if err != nil {
				t.Fatal("invalid independent clock expectation", clock, err)
			}
			result[i][j] = value
		}
	}
	return result
}

func TestReferenceTimeValuesParametersAndMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			query := func(q string, names, types []string, rows [][]any, args ...any) {
				s := referenceSelect(q, names, types, referenceTimeRows(t, backend.postgres, types, rows), args...)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)", 4))
			query("SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})
			query("SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})
			rows, err := backend.db.QueryContext(p.ctx, "SELECT v,fine FROM t")
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, m := range metadata {
				want := reflect.TypeFor[string]()
				if backend.postgres {
					want = reflect.TypeFor[time.Time]()
				}
				if m.DatabaseTypeName() != "TIME" || m.ScanType() != want {
					t.Fatal(m, want)
				}
			}
			run(referenceExec("CREATE TABLE params(id INTEGER,v TIME(0),fine TIME(6))", 0))
			ins, err := backend.db.PrepareContext(p.ctx, "INSERT INTO params VALUES($1,$2,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer ins.Close()
			for _, tc := range []struct {
				id    int
				value any
			}{{1, "00:00"}, {2, "24:00"}, {3, nil}, {4, []byte("04:05:06.5")}, {5, time.Date(2003, 7, 7, 15, 36, 39, 123456000, time.FixedZone("offset", 7*3600))}} {
				r, err := ins.ExecContext(p.ctx, tc.id, tc.value)
				if err != nil {
					t.Fatal(err)
				}
				n, err := r.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			query("SELECT id,v,fine FROM params ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), nil, nil}, {int64(4), "04:05:07", "04:05:06.5"}, {int64(5), "15:36:39", "15:36:39.123456"}})
			held, err := backend.db.PrepareContext(p.ctx, "SELECT id FROM params WHERE v=$1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				arg any
				id  int64
			}{{"00:00", 1}, {"24:00", 2}, {nil, 0}, {"00:00", 1}} {
				rows, err := held.QueryContext(p.ctx, tc.arg)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var id int64
					if err = rows.Scan(&id); err != nil || id != tc.id {
						t.Fatal(id, err)
					}
					count++
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				want := 1
				if tc.id == 0 {
					want = 0
				}
				if count != want {
					t.Fatal(count, want)
				}
			}
		})
	}
}

func TestReferenceTimeClocksAndAtomicity(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		postgres bool
		db       *sql.DB
		record   *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("CREATE TABLE t(v TIME)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("INSERT INTO t VALUES('00:00')", 1))
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before := time.Now().UTC()
			held, err := conn.PrepareContext(p.ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			after := time.Now().UTC()
			var clock any
			var stamp time.Time
			if err = held.QueryRowContext(p.ctx).Scan(&clock, &stamp); err != nil {
				t.Fatal(err)
			}
			if stamp.Before(before.Add(-time.Microsecond)) || stamp.After(after.Add(time.Microsecond)) {
				t.Fatal(stamp, before, after)
			}
			want := referenceTimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{stamp.UTC().Format("15:04:05.999999")}})[0][0]
			if !reflect.DeepEqual(referenceNormalize(clock), want) {
				t.Fatal(clock, want)
			}
			tx, err := conn.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for range 2 {
				r, err := tx.ExecContext(p.ctx, "INSERT INTO t VALUES($1)", "now")
				if err != nil {
					t.Fatal(err)
				}
				n, err := r.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			var a, b any
			var txStamp time.Time
			if err = tx.QueryRowContext(p.ctx, "SELECT TIME 'now',TIMESTAMP 'now' FROM t WHERE v='00:00'").Scan(&a, &txStamp); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.ctx, "SELECT v FROM t WHERE v=$1", txStamp.UTC().Format("15:04:05.999999"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				if err = rows.Scan(&b); err != nil || !reflect.DeepEqual(referenceNormalize(a), referenceNormalize(b)) {
					t.Fatal(a, b, err)
				}
				count++
			}
			if err = rows.Err(); err != nil || count < 2 {
				t.Fatal(count, err)
			}
			rows.Close()
			var frozen any
			var frozenStamp time.Time
			if err = held.QueryRowContext(p.ctx).Scan(&frozen, &frozenStamp); err != nil || !reflect.DeepEqual(referenceNormalize(frozen), referenceNormalize(clock)) || !frozenStamp.Equal(stamp) {
				t.Fatal(frozen, frozenStamp, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			// Native admission checks an already-canceled context on a held Conn.
			// pq's fast Conn execution can win its transport-cancellation race;
			// exercise PostgreSQL through database/sql's pre-admission DB check.
			var canceledErr error
			if backend.postgres {
				_, canceledErr = backend.db.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			} else {
				_, canceledErr = conn.ExecContext(canceled, "INSERT INTO t VALUES('24:00')")
			}
			if canceledErr != context.Canceled {
				t.Fatal(canceledErr)
			}
			referenceRunStep(t, p.ctx, conn, backend.record, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.postgres, []string{"TIME"}, [][]any{{"00:00:00"}})))
		})
	}
}
