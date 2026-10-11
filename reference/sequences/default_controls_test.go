package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDefaultNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	check := func(step fixture.Step) {
		t.Helper()
		for _, backend := range p.Backends() {
			postgres, db, record := backend.PG, backend.DB, backend.Recorder
			expected := step
			expected.Rows = fixture.TimeRows(t, postgres, step.Types, step.Rows)
			fixture.RunStep(t, p.Ctx, db, record, expected)
		}
	}
	p.Step(t, fixture.Exec(`CREATE TABLE "d.dot"(id INT,"v.dot" TEXT DEFAULT E'O\'Brien\\tail',n NUMERIC(3,1) DEFAULT 12.34,b BOOL DEFAULT (1 < 2 AND NOT FALSE),ts TIMESTAMP DEFAULT TIMESTAMP '2020-02-03 04:05:06',tm TIME DEFAULT TIME '04:05:06')`, 0))
	p.Step(t, fixture.Exec(`CREATE TABLE deferred(i INT DEFAULT 2147483648)`, 0))
	var empty bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &empty); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"DEFAULT", "2147483648", "12.34", " AND ", "TIMESTAMP '2020-02-03 04:05:06'", "TIME '04:05:06'"} {
		if !strings.Contains(empty.String(), part) {
			t.Fatal("definition not persisted", part, empty.String())
		}
	}
	p.Step(t, fixture.Exec(`INSERT INTO "d.dot"(id) VALUES(1)`, 1))
	p.Step(t, fixture.Prepared(fixture.Exec(`INSERT INTO "d.dot"(id,"v.dot") VALUES(2,NULL)`, 1)))
	want := fixture.Select(`SELECT * FROM "d.dot" ORDER BY id`, []string{"id", "v.dot", "n", "b", "ts", "tm"}, []string{"INT4", "TEXT", "NUMERIC", "BOOL", "TIMESTAMP", "TIME"}, [][]any{{int64(1), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}, {int64(2), nil, "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}})
	check(want)
	held, err := p.Native.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`CREATE TABLE bad(i INT DEFAULT 'oops')`, `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2)`, `CREATE TABLE bad(i INT NOT NULL); INSERT INTO bad DEFAULT VALUES;`} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		check(want)
		var same bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil || same.String() != dump.String() {
			t.Fatal("failure changed defaults/data", err, same.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP TABLE "d.dot",deferred`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejected native defaults dump", err)
		}
	}
	check(want)
	check(fixture.Prepared(want))
	var same bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil || same.String() != dump.String() {
		t.Fatal("nondeterministic dump", err, same.String())
	}
	p.Step(t, fixture.ImplicitError(`INSERT INTO deferred DEFAULT VALUES`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
	p.Step(t, fixture.Prepared(fixture.Exec(`INSERT INTO "d.dot"(id) VALUES(3)`, 1)))
	check(fixture.Select(`SELECT * FROM "d.dot" WHERE id=3`, want.Columns, want.Types, [][]any{{int64(3), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}}))
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
		row, dest := make([]any, 6), make([]any, 6)
		for i := range row {
			dest[i] = &row[i]
		}
		if err := held.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		captured = append(captured, row)
	}
	if err := held.Err(); err != nil || !reflect.DeepEqual(captured, want.Rows) {
		t.Fatal("retained rows changed", captured, err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	_, err = p.Native.ExecContext(canceled, `INSERT INTO "d.dot"(id) VALUES(4)`)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.Step(t, fixture.Select(`SELECT id FROM "d.dot" ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
}

func TestReferenceDefaultTemporalEndpointControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/populated=%v", prepared, populated), func(t *testing.T) {
				p := fixture.NewScenario(t)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.DefaultReferenceStep(t, p, s) }
				run(fixture.Exec(`CREATE TABLE endpoint(id INT,v TIMESTAMP(6) DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999',b BOOL DEFAULT (TIMESTAMP(0) '294276-12-31 23:59:59.999999' = TIMESTAMP(0) '294276-12-31 23:59:59.999999'))`, 0))
				endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
				want := fixture.Select(`SELECT * FROM endpoint ORDER BY id`, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, nil)
				if populated {
					run(fixture.Exec(`INSERT INTO endpoint(id) VALUES(1),(2)`, 2))
					run(fixture.Exec(`INSERT INTO endpoint VALUES(3,DEFAULT,DEFAULT)`, 1))
					want.Rows = [][]any{{int64(1), endpoint, true}, {int64(2), endpoint, true}, {int64(3), endpoint, true}}
				}
				run(want)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				// Real PostgreSQL SQL replay is distinct from native host Restore.
				var replayErr error
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, `DROP TABLE endpoint`); err != nil {
						t.Fatal(err)
					}
					_, replayErr = p.Postgres.ExecContext(p.Ctx, dump.String())
					if replayErr != nil {
						t.Error("PostgreSQL endpoint dump replay", replayErr)
					}
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				restoreErr := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				if restoreErr != nil {
					t.Error("native endpoint default restore", restoreErr)
				}
				if replayErr != nil || restoreErr != nil {
					return
				}
				run(want)
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("nondeterministic endpoint redump", err, again.String())
				}
				run(fixture.Exec(`INSERT INTO endpoint(id) VALUES(4)`, 1))
				run(fixture.Exec(`INSERT INTO endpoint VALUES(5,DEFAULT,DEFAULT)`, 1))
				want.Rows = append(want.Rows, []any{int64(4), endpoint, true}, []any{int64(5), endpoint, true})
				run(want)
			})
		}
	}
}
