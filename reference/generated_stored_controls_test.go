package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceGeneratedStoredControlsSmoke(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(referenceExec(`CREATE TABLE only_generated(b INT GENERATED ALWAYS AS(1) STORED)`, 0))
		run(referenceExec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2))
		run(referenceExec(`INSERT INTO only_generated DEFAULT VALUES`, 1))
		if !backend.pg {
			held, err := p.native.QueryContext(p.ctx, `SELECT * FROM g WHERE a=2`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			before, err := held.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if !strings.Contains(nativeDump, "GENERATED ALWAYS AS") || !strings.Contains(nativeDump, "STORED") || !strings.Contains(nativeDump, "DEFAULT") {
				t.Fatal(nativeDump)
			}
			for _, script := range []string{`CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); INSERT INTO bad VALUES(1,2);`, `CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(b+1) STORED);`, `CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); INSERT INTO bad(a) VALUES(1),(2147483647);`} {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid restore accepted", script)
				}
				run(referenceSelect(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			}
			failure := errors.New("generated control I/O failure")
			if err := stuntgres.Dump(p.ctx, p.native, catalogControlFailingWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if result, err := p.native.ExecContext(canceled, `INSERT INTO g(a) VALUES(99)`); !errors.Is(err, context.Canceled) || result != nil {
				t.Fatal(result, err)
			}
			run(referenceSelect(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			after, err := held.ColumnTypes()
			if err != nil || len(after) != len(before) {
				t.Fatal(after, err)
			}
			for i := range before {
				if before[i].DatabaseTypeName() != after[i].DatabaseTypeName() {
					t.Fatal("held metadata changed")
				}
			}
			if !held.Next() {
				t.Fatal("held row lost", held.Err())
			}
			var a, b int64
			if err := held.Scan(&a, &b); err != nil || a != 2 || b != 3 {
				t.Fatal(a, b, err)
			}
			if held.Next() || held.Err() != nil {
				t.Fatal("held snapshot changed", held.Err())
			}
		} else if nativeDump != "" {
			run(referenceExec(`DROP TABLE g,only_generated`, 0))
			if _, err := backend.db.ExecContext(p.ctx, nativeDump); err != nil {
				t.Fatal("PostgreSQL native dump replay", err)
			}
		}
		run(referenceSelect(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(referenceSelect(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(referenceSelect(`SELECT * FROM only_generated`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(referenceExec(`UPDATE g SET a=8 WHERE a=2`, 1))
		run(referenceSelect(`SELECT * FROM g WHERE a=8`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
		run(referenceSelect(`SELECT * FROM g WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}))
	})
}
