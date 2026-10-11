package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeMixedControls(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE source(id integer NOT NULL CHECK(id>0),note text)`, `CREATE TYPE wrapper AS (r source,t varchar(3) COLLATE "C",a integer[],rc regclass,rt regtype)`, `CREATE DOMAIN wrapped AS wrapper`, `CREATE TABLE holder(k integer,v wrapped DEFAULT '("(,)","",,,)'::wrapper)`, `CREATE VIEW saved AS SELECT k,v FROM holder`)
		input := `("(-1,O'K)","","{1,2}",source,wrapper)`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,$1),(2,NULL)`, 2, input))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder(k) VALUES (3)`, 1))
		want := [][]any{{`("(-1,O'K)","","{1,2}",source,wrapper)`}, {nil}, {`("(,)","",,,)`}}
		check := func() { fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, want, false) }
		check()
		if b.PG {
			return
		}
		rawConn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rawConn.Close()
		err = rawConn.Raw(func(raw any) error {
			held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, `SELECT v FROM holder ORDER BY k`, nil)
			if err != nil {
				return err
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				return err
			}
			text := dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				return err
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				return err
			}
			check()
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				return err
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Errorf("logical redump changed:\noriginal=%s\nrestored=%s", dump.String(), redump.String())
			}
			dst := make([]driver.Value, 1)
			for _, row := range want {
				if err := held.Next(dst); err != nil || dst[0] != row[0] {
					t.Errorf("held composite row got=%#v want=%#v err=%v", dst, row, err)
				}
			}
			if err := held.Next(dst); err != io.EOF {
				t.Errorf("held cursor tail %v", err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					return err
				}
				defer func() {
					if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
						t.Error(err)
					}
				}()
				if _, err := p.Postgres.ExecContext(p.Ctx, text); err != nil {
					return err
				}
				pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
				fixture.CompositeReferenceRows(t, p, pg, p.Postgres, `SELECT v FROM holder ORDER BY k`, want, false)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		wantError := fixture.CompositeReferenceDiagnostic("23514", `new row for relation "source" violates check constraint "source_id_check"`)
		wantError.Detail = "Failing row contains (-1, x)."
		wantError.Schema = "public"
		wantError.Table = "source"
		wantError.Constraint = "source_id_check"
		fixture.CompositeReferenceError(t, p, c, `INSERT INTO source VALUES (-1,'x')`, "execute", wantError)
		for _, script := range []string{
			`CREATE TYPE replacement AS (i integer); CREATE TABLE fresh(v replacement); INSERT INTO fresh VALUES ('(bad)');`,
			`CREATE TYPE replacement AS (i integer); CREATE TABLE fresh(v replacement); INSERT INTO fresh VALUES ('(1)'),('(1,2)');`,
			`CREATE TYPE replacement AS (i integer); CREATE TABLE fresh(v replacement); SELECT $1;`,
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Error("invalid composite Restore succeeded", script)
			}
			check()
		}
	})
}
