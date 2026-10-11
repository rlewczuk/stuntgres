package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"

	"github.com/ngrok/sqlmw"
)

func TestReferenceGeneratedVirtualControls(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`CREATE TABLE only_generated(b INT GENERATED ALWAYS AS(1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2))
		run(fixture.Exec(`INSERT INTO only_generated DEFAULT VALUES`, 1))
		run(fixture.Exec(`CREATE TABLE lazy(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO lazy(a) VALUES(0)`, 1))
		run(fixture.Exec(`CREATE TABLE mixed(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,c INT GENERATED ALWAYS AS(a*2) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO mixed(a) VALUES(2)`, 1))
		run(fixture.Exec(`CREATE TABLE clone(LIKE lazy INCLUDING GENERATED)`, 0))
		run(fixture.Exec(`INSERT INTO clone(a) VALUES(0)`, 1))
		run(fixture.Exec(`CREATE TABLE parent(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
		run(fixture.Exec(`CREATE TABLE child() INHERITS(parent)`, 0))
		run(fixture.Exec(`INSERT INTO child(a) VALUES(0)`, 1))
		run(fixture.Exec(`CREATE TABLE oidg(a INT,b oid GENERATED ALWAYS AS(tableoid) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO oidg(a) VALUES(2)`, 1))
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
			// Fresh driver and DSN, two genuine replacement/redump cycles. The
			// stream is never edited before the release backend replay.
			connector, err := stuntgres.NewDriver().OpenConnector("virtual-control-fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
			configured, err := wrapped.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			recorded := sql.OpenDB(configured)
			defer recorded.Close()
			record := &fixture.Recorder{RawBackend: fixture.RawNative}
			stream := nativeDump
			for cycle := range 2 {
				if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(stream)); err != nil {
					t.Fatal(cycle, err)
				}
				var owner, value int64
				if err := fresh.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
					t.Fatal(owner, err)
				}
				if err := fresh.QueryRowContext(p.Ctx, `SELECT b FROM oidg`).Scan(&value); err != nil || value != owner {
					t.Fatal(value, owner, err)
				}
				fixture.RunStep(t, p.Ctx, recorded, record, fixture.Select(`SELECT * FROM mixed`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}}))
				fixture.RunStep(t, p.Ctx, recorded, record, fixture.Select(`SELECT a FROM lazy`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
				bad := fixture.ImplicitError(`SELECT b FROM lazy`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				bad.SelectRows = true
				fixture.RunStep(t, p.Ctx, recorded, record, bad)
				var next bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, fresh, &next); err != nil {
					t.Fatal(err)
				}
				stream = next.String()
				if !strings.Contains(stream, "VIRTUAL") || !strings.Contains(stream, "STORED") || !strings.Contains(stream, "DEFAULT") {
					t.Fatal(stream)
				}
			}
			if !strings.Contains(nativeDump, "GENERATED ALWAYS AS") || !strings.Contains(nativeDump, "VIRTUAL") || !strings.Contains(nativeDump, "DEFAULT") || !strings.Contains(nativeDump, "STORED") {
				t.Fatal(nativeDump)
			}
			for _, script := range []string{`CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL); INSERT INTO bad VALUES(1,2);`, `CREATE TABLE bad(a INT,b INT GENERATED ALWAYS AS(b+1) VIRTUAL);`, `CREATE DOMAIN vd AS INT; CREATE TABLE bad(a INT,b vd GENERATED ALWAYS AS(a+1) VIRTUAL);`} {
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
			run(fixture.Exec(`DROP TABLE child,parent,clone,lazy,mixed,g,only_generated,oidg`, 0))
			if _, err := backend.DB.ExecContext(p.Ctx, nativeDump); err != nil {
				t.Fatal("PostgreSQL native dump replay", err)
			}
		}
		run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Select(`SELECT * FROM only_generated`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select(`SELECT * FROM mixed`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}}))
		var owner, value int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b FROM oidg`).Scan(&value); err != nil || value != owner {
			t.Fatal(value, owner, err)
		}
		for _, q := range []string{`SELECT a FROM lazy`, `SELECT a FROM clone`, `SELECT a FROM child`} {
			run(fixture.Select(q, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		}
		bad := fixture.ImplicitError(`SELECT b FROM child`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		bad.SelectRows = true
		run(bad)
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(fixture.Exec(`UPDATE g SET a=8 WHERE a=2`, 1))
		run(fixture.Select(`SELECT * FROM g WHERE a=8`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}))
	})
}
