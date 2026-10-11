package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredControlsSmoke(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`CREATE TABLE only_generated(b INT GENERATED ALWAYS AS(1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2))
		run(fixture.Exec(`INSERT INTO only_generated DEFAULT VALUES`, 1))
		if !backend.PG {
			held, err := p.Native.QueryContext(p.Ctx, `SELECT * FROM g WHERE a=2`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			before, err := held.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if !strings.Contains(nativeDump, "GENERATED ALWAYS AS") || !strings.Contains(nativeDump, "STORED") || !strings.Contains(nativeDump, "DEFAULT") {
				t.Fatal(nativeDump)
			}
			for _, script := range []string{`CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); INSERT INTO bad VALUES(1,2);`, `CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(b+1) STORED);`, `CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) STORED); INSERT INTO bad(a) VALUES(1),(2147483647);`} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid restore accepted", script)
				}
				run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			}
			failure := errors.New("generated control I/O failure")
			if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(nativeDump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if result, err := p.Native.ExecContext(canceled, `INSERT INTO g(a) VALUES(99)`); !errors.Is(err, context.Canceled) || result != nil {
				t.Fatal(result, err)
			}
			run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump)); err != nil {
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
			run(fixture.Exec(`DROP TABLE g,only_generated`, 0))
			if _, err := backend.DB.ExecContext(p.Ctx, nativeDump); err != nil {
				t.Fatal("PostgreSQL native dump replay", err)
			}
		}
		run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Select(`SELECT * FROM only_generated`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(fixture.Exec(`UPDATE g SET a=8 WHERE a=2`, 1))
		run(fixture.Select(`SELECT * FROM g WHERE a=8`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}))
	})
}
