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

func TestReferencePosixControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE t(id INT,v TEXT,p TEXT DEFAULT '(b)',sentinel INT DEFAULT 7,d TEXT DEFAULT substring('foobar','o(.)b'),CONSTRAINT valid CHECK(v ~ '^a'))`, 0))
			run(fixture.Exec(`INSERT INTO t(id,v) VALUES(1,'abc'),(2,NULL)`, 2))
			run(fixture.Exec(`CREATE VIEW matched AS SELECT id,v ~ p AS s,v ~* p AS i,v !~ p AS n,v !~* p AS ni,substring(v FROM p) AS piece,substring(v,'a#"%#"','#') AS sim FROM t`, 0))
			want := fixture.Select(`SELECT s,i,n,ni,piece,sim FROM matched ORDER BY id`, []string{"s", "i", "n", "ni", "piece", "sim"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, false, false, "b", "bc"}, {nil, nil, nil, nil, nil, nil}})
			want.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
			similarReferenceRoutes(t, p, b, want)
			if b.PG {
				return
			} // PostgreSQL has no public Reset/Restore API.
			old, err := p.Native.QueryContext(p.Ctx, want.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
			run(want)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("unstable roundtrip", err)
			}
			for _, c := range []struct {
				suffix     string
				diagnostic fixture.Diagnostic
			}{
				{`INSERT INTO t(id,v) VALUES(3,'abc'),(4,'bad');`, fixture.CheckViolation("t", "valid", "4, bad, (b), 7, o")},
				{`CREATE TABLE replacement(v TEXT DEFAULT substring('a','[')); INSERT INTO replacement VALUES('created'); INSERT INTO replacement DEFAULT VALUES;`, fixture.Diagnostic{Code: "2201B", Message: "invalid regular expression: brackets [] not balanced"}},
			} {
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+c.suffix))
				if err == nil {
					t.Fatal("failed Restore published")
				}
				c.diagnostic.Severity = "ERROR"
				c.diagnostic.SeverityUnlocalized = "ERROR"
				fixture.AssertDiagnostic(t, err, &c.diagnostic)
				run(want)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(redump.Bytes(), after.Bytes()) {
					t.Fatal("failed Restore changed catalog", err)
				}
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			duringRead, cancelRead := context.WithCancel(p.Ctx)
			reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(dump.Bytes()), Cancel: cancelRead}
			err = stuntgres.Restore(duringRead, p.Native, reader)
			cancelRead()
			if reader.Len() != 0 || !errors.Is(err, context.Canceled) {
				t.Fatal("in-flight Restore cancellation", reader.Len(), err)
			}
			var afterCancel bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &afterCancel); err != nil || !bytes.Equal(redump.Bytes(), afterCancel.Bytes()) {
				t.Fatal("canceled Restore changed catalog", err)
			}
			run(want)
			if p.Postgres != nil {
				for _, q := range []string{`DROP VIEW IF EXISTS matched`, `DROP TABLE IF EXISTS t`, dump.String()} {
					if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				replay := want
				replay.CheckMetadata = similarReferenceDeclaredMetadata(true)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, replay)
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP VIEW matched; DROP TABLE t`); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
