package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLNameTruncationPersistence(t *testing.T) {
	triggerNameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA tn_saved", 0))
		run(fixture.Exec("CREATE TABLE tn_saved.data(id integer PRIMARY KEY,v integer,mark text)", 0))
		run(fixture.Exec("CREATE FUNCTION tn_saved.add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; NEW.mark:=TG_NAME; RETURN NEW; END$$", 0))
		run(fixture.Exec("CREATE TRIGGER "+triggerLongSource+" BEFORE INSERT ON tn_saved.data FOR EACH ROW EXECUTE FUNCTION tn_saved.add('3')", 0))
		multi := strings.Repeat("é", 32)
		canonical := strings.Repeat("é", 31)
		run(fixture.Exec(`CREATE TRIGGER "`+multi+`" BEFORE UPDATE OF v ON tn_saved.data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tn_saved.add('7')`, 0))
		run(fixture.Exec("INSERT INTO tn_saved.data VALUES(1,5,NULL)", 1))
		insertQ := "INSERT INTO tn_saved.data VALUES($1,5,NULL) RETURNING id,v,mark"
		updateQ := "UPDATE tn_saved.data SET v=5 WHERE id=1 RETURNING id,v,mark"
		insert := (eventDDLCase{t: t, p: p, b: b, c: c}).held(insertQ)
		defer insert.Statement.Close()
		update := (eventDDLCase{t: t, p: p, b: b, c: c}).held(updateQ)
		defer update.Statement.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			r(triggerNameRows("SELECT id,v,mark FROM tn_saved.data ORDER BY id", []any{int64(1), int64(8), triggerLongCanonical}))
			r(fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='tn_saved.data'::regclass ORDER BY tgname", []string{"tgname"}, []string{"NAME"}, [][]any{{triggerLongCanonical}, {canonical}}))
			r(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			s := triggerNameRows(insertQ, []any{int64(2), int64(8), triggerLongCanonical})
			s.Args = []any{2}
			if conn == c {
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, s)
			} else {
				r(fixture.Prepared(s))
			}
			s = triggerNameRows(updateQ, []any{int64(1), int64(12), canonical})
			if conn == c {
				fixture.RunStep(t, p.Ctx, update, b.Recorder, s)
			} else {
				r(fixture.Prepared(s))
			}
			r(triggerNameRows("INSERT INTO tn_saved.data VALUES(3,5,NULL) RETURNING id,v,mark", []any{int64(3), int64(8), triggerLongCanonical}))
			r(fixture.Prepared(triggerNameRows("UPDATE tn_saved.data SET v=5 WHERE id=3 RETURNING id,v,mark", []any{int64(3), int64(12), canonical})))
			r(fixture.Exec("ROLLBACK", 0))
		}
		triggerNameReplay(t, p, b, c, "tn_saved", check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+`; ALTER TRIGGER absent ON tn_saved.data RENAME TO missing;`)); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		check(c, b)
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(dump.Bytes()), &fixture.ReferenceRangeErrorReader{Err: io.ErrUnexpectedEOF})); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		check(c, b)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check(c, b)
	}, "tn_saved")
}

func triggerNameReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	check(c, b)
	if b.PG {
		return
	}
	dropSchema := "DROP SCHEMA IF EXISTS " + pq.QuoteIdentifier(schema) + " CASCADE"
	for range 2 {
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
		check(c, b)
		if p.Postgres != nil {
			func() {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				defer func() {
					if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; "+dropSchema); err != nil {
						t.Error(err)
					}
				}()
				if _, err := pg.ExecContext(p.Ctx, dropSchema); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("unchanged trigger name dump replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			}()
		}
	}
}
