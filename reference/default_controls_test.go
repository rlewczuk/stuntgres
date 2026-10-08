//go:build reference

package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceDefaultNativeControls(t *testing.T) {
	p := newReferencePair(t)
	check := func(step referenceStep) {
		t.Helper()
		for _, postgres := range []bool{false, true} {
			db, record := p.memory, p.memoryRecord
			if postgres {
				db, record = p.postgres, p.postgresRecord
			}
			expected := step
			expected.rows = referenceTimeRows(t, postgres, step.types, step.rows)
			referenceRunStep(t, p.ctx, db, record, expected)
		}
	}
	p.step(t, referenceExec(`CREATE TABLE "d.dot"(id INT,"v.dot" TEXT DEFAULT E'O\'Brien\\tail',n NUMERIC(3,1) DEFAULT 12.34,b BOOL DEFAULT (1 < 2 AND NOT FALSE),ts TIMESTAMP DEFAULT TIMESTAMP '2020-02-03 04:05:06',tm TIME DEFAULT TIME '04:05:06')`, 0))
	p.step(t, referenceExec(`CREATE TABLE deferred(i INT DEFAULT 2147483648)`, 0))
	var empty bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &empty); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.String(), "DEFAULT") || !strings.Contains(empty.String(), " AND ") {
		t.Fatal("default structure lost", empty.String())
	}
	p.step(t, referenceExec(`INSERT INTO "d.dot"(id) VALUES(1)`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "d.dot"(id,"v.dot") VALUES(2,NULL)`, 1)))
	want := referenceSelect(`SELECT * FROM "d.dot" ORDER BY id`, []string{"id", "v.dot", "n", "b", "ts", "tm"}, []string{"INT4", "TEXT", "NUMERIC", "BOOL", "TIMESTAMP", "TIME"}, [][]any{{int64(1), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}, {int64(2), nil, "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}})
	check(want)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`CREATE TABLE bad(i INT DEFAULT 'oops')`, `CREATE TABLE bad(i INT DEFAULT 1 DEFAULT 2)`, `CREATE TABLE bad(i INT NOT NULL); INSERT INTO bad DEFAULT VALUES;`} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		check(want)
		var same bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &same); err != nil || same.String() != dump.String() {
			t.Fatal("failure changed defaults/data", err, same.String())
		}
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, `DROP TABLE "d.dot",deferred`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL rejected native defaults dump", err)
	}
	check(want)
	check(referencePrepared(want))
	var same bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &same); err != nil || same.String() != dump.String() {
		t.Fatal("nondeterministic dump", err, same.String())
	}
	p.step(t, referenceImplicitError(`INSERT INTO deferred DEFAULT VALUES`, referenceDiagnostic{Code: "22003", Message: "integer out of range"}))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "d.dot"(id) VALUES(3)`, 1)))
	check(referenceSelect(`SELECT * FROM "d.dot" WHERE id=3`, want.columns, want.types, [][]any{{int64(3), "O'Brien\\tail", "12.3", true, time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC), "04:05:06"}}))
}

func TestReferenceDefaultTemporalEndpointControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/populated=%v", prepared, populated), func(t *testing.T) {
				p := newReferencePair(t)
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(referenceExec(`CREATE TABLE endpoint(id INT,v TIMESTAMP(6) DEFAULT TIMESTAMP(0) '294276-12-31 23:59:59.999999',b BOOL DEFAULT (TIMESTAMP(0) '294276-12-31 23:59:59.999999' = TIMESTAMP(0) '294276-12-31 23:59:59.999999'))`, 0))
				endpoint := time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)
				want := referenceSelect(`SELECT * FROM endpoint ORDER BY id`, []string{"id", "v", "b"}, []string{"INT4", "TIMESTAMP", "BOOL"}, nil)
				if populated {
					run(referenceExec(`INSERT INTO endpoint(id) VALUES(1),(2)`, 2))
					run(referenceExec(`INSERT INTO endpoint VALUES(3,DEFAULT,DEFAULT)`, 1))
					want.rows = [][]any{{int64(1), endpoint, true}, {int64(2), endpoint, true}, {int64(3), endpoint, true}}
				}
				run(want)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				// Real PostgreSQL SQL replay is distinct from native host Restore.
				if _, err := p.postgres.ExecContext(p.ctx, `DROP TABLE endpoint`); err != nil {
					t.Fatal(err)
				}
				_, replayErr := p.postgres.ExecContext(p.ctx, dump.String())
				if replayErr != nil {
					t.Error("PostgreSQL endpoint dump replay", replayErr)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				restoreErr := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes()))
				if restoreErr != nil {
					t.Error("native endpoint default restore", restoreErr)
				}
				if replayErr != nil || restoreErr != nil {
					return
				}
				run(want)
				var again bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
					t.Fatal("nondeterministic endpoint redump", err, again.String())
				}
				run(referenceExec(`INSERT INTO endpoint(id) VALUES(4)`, 1))
				run(referenceExec(`INSERT INTO endpoint VALUES(5,DEFAULT,DEFAULT)`, 1))
				want.rows = append(want.rows, []any{int64(4), endpoint, true}, []any{int64(5), endpoint, true})
				run(want)
			})
		}
	}
}
