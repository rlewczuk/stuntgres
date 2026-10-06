package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestDefaultControlsPersistentDefinitionsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "d.dot"(id INT,"v.dot" TEXT DEFAULT E'O\'Brien\\tail',n NUMERIC(3,1) DEFAULT 12.34,b BOOL DEFAULT (1 < 2 AND NOT FALSE),ts TIMESTAMP DEFAULT TIMESTAMP '2020-02-03 04:05:06',tm TIME DEFAULT TIME '04:05:06')`)
	execSQL(t, db, `CREATE TABLE deferred(i INT DEFAULT 2147483648)`)
	var empty bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &empty); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"DEFAULT", "2147483648", "12.34", " AND ", "TIMESTAMP '2020-02-03 04:05:06'", "TIME '04:05:06'"} {
		if !strings.Contains(empty.String(), part) {
			t.Fatal("definition not persisted", part, empty.String())
		}
	}
	execSQL(t, db, `INSERT INTO "d.dot"(id) VALUES(1)`)
	execSQL(t, db, `INSERT INTO "d.dot"(id,"v.dot") VALUES(2,NULL)`)
	want := [][]any{{int64(1), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}, {int64(2), nil, "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}}
	held, err := db.Query(`SELECT * FROM "d.dot" ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`CREATE TABLE bad(i INT DEFAULT 'oops')`, `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2)`, `CREATE TABLE bad(i INT NOT NULL); INSERT INTO bad DEFAULT VALUES;`} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted", script)
		}
		var same bytes.Buffer
		if err = stuntgres.Dump(ctx, db, &same); err != nil || same.String() != dump.String() {
			t.Fatal("failure changed defaults/data", err, same.String())
		}
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, `SELECT * FROM "d.dot" ORDER BY id`, want)
	var same bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &same); err != nil || same.String() != dump.String() {
		t.Fatal("nondeterministic redump", err, same.String())
	}
	_, err = db.Exec(`INSERT INTO deferred DEFAULT VALUES`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
	execSQL(t, db, `INSERT INTO "d.dot"(id) VALUES(3)`)
	wantTuples(t, db, `SELECT * FROM "d.dot" WHERE id=3`, [][]any{{int64(3), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}})
	after, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i := range after {
		if metadata[i].DatabaseTypeName() != after[i].DatabaseTypeName() || metadata[i].ScanType() != after[i].ScanType() {
			t.Fatal("retained metadata changed")
		}
	}
	var captured [][]any
	for held.Next() {
		row := make([]any, 6)
		dest := make([]any, 6)
		for i := range row {
			dest[i] = &row[i]
		}
		if err = held.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, row)
	}
	if err = held.Err(); err != nil || !reflect.DeepEqual(captured, want) {
		t.Fatal("retained rows changed", captured, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(canceled, `INSERT INTO "d.dot"(id) VALUES(4)`)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, `SELECT id FROM "d.dot" ORDER BY id`, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
}

func TestDefaultTemporalEndpointControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/populated=%v", prepared, populated), func(t *testing.T) {
				ctx := context.Background()
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, `CREATE TABLE endpoint(id INT,v TIMESTAMP(6) DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999',b BOOL DEFAULT (TIMESTAMP(0) '294276-12-31 23:59:59.999999' = TIMESTAMP(0) '294276-12-31 23:59:59.999999'))`, 0)
				endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
				var want [][]any
				check := func() {
					t.Helper()
					q := `SELECT * FROM endpoint ORDER BY id`
					if prepared {
						s, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						rows, err := s.Query()
						if err != nil {
							t.Fatal(err)
						}
						timestampCheckRows(t, rows, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, want)
					} else {
						timestampQuery(t, db, q, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, want)
					}
				}
				if populated {
					tableCoreExec(t, db, prepared, `INSERT INTO endpoint(id) VALUES(1),(2)`, 2)
					tableCoreExec(t, db, prepared, `INSERT INTO endpoint VALUES(3,DEFAULT,DEFAULT)`, 1)
					want = [][]any{{int64(1), endpoint, true}, {int64(2), endpoint, true}, {int64(3), endpoint, true}}
				}
				check()
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "TIMESTAMP TIMESTAMP") || !strings.Contains(dump.String(), `DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999'`) {
					t.Error("invalid canonical endpoint default", dump.String())
				}
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal("endpoint default restore", err)
				}
				check()
				var again bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
					t.Fatal("nondeterministic endpoint redump", err, again.String())
				}
				tableCoreExec(t, db, prepared, `INSERT INTO endpoint(id) VALUES(4)`, 1)
				tableCoreExec(t, db, prepared, `INSERT INTO endpoint VALUES(5,DEFAULT,DEFAULT)`, 1)
				want = append(want, []any{int64(4), endpoint, true}, []any{int64(5), endpoint, true})
				check()
			})
		}
	}
}
