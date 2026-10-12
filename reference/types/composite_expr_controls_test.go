package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReferenceCompositeExprControlsAndLifetime(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN short_text AS varchar(3)`, `CREATE TYPE pair AS (i integer,t short_text COLLATE "C")`, `CREATE TYPE outer_pair AS (p pair,n integer)`, `CREATE TABLE holder(k integer,v pair DEFAULT ROW(7,'def')::pair,w outer_pair DEFAULT ROW(ROW(8,'inn'),9)::outer_pair,g integer GENERATED ALWAYS AS ((w).p.i) STORED,CHECK ((v).i>0))`, `CREATE INDEX restored_field ON holder (((v).i))`, `CREATE VIEW expanded AS SELECT k,(v).* FROM holder`)
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE VIEW computed_row AS SELECT k,(ROW((v).i,(v).t)).* FROM holder`,
			`CREATE VIEW computed_cast AS SELECT k,(ROW((v).i,(v).t)::pair).* FROM holder`,
			`CREATE VIEW computed_coalesce AS SELECT k,(COALESCE(v,ROW(77,'fal')::pair)).* FROM holder`,
			`CREATE VIEW nested_field AS SELECT k,(w).p.i AS nested_i FROM holder`,
			`CREATE TYPE collision_pair AS (num_nulls integer,t text)`,
			`CREATE TABLE builtin_capture(k integer,v pair DEFAULT ROW(9,'def')::pair,c collision_pair DEFAULT ROW(7,'collision')::collision_pair,n integer DEFAULT (ROW(1,'x')::pair).num_nulls,s integer DEFAULT (1).num_nulls,nn integer DEFAULT (NULL::integer).num_nonnulls)`,
			`CREATE VIEW builtin_capture_view AS SELECT k,(v).num_nulls AS fallback,(c).num_nulls AS field,num_nulls(c) AS function,(1).num_nulls AS scalar FROM builtin_capture`,
			`CREATE TABLE whole_collision(h integer,t text)`,
			`CREATE VIEW whole_source AS SELECT num_nulls(h.*) AS n,h.*::text AS whole FROM whole_collision h`,
			`CREATE VIEW whole_outer AS SELECT h.h AS k,(SELECT num_nulls(h.*)) AS n FROM whole_collision h`,
			`CREATE VIEW whole_derived AS SELECT h.h AS k,(SELECT h.*::text FROM (VALUES(NULL::integer,NULL::text)) h(i,t)) AS whole FROM whole_collision h`,
			`CREATE VIEW whole_alias_field AS SELECT (h).renamed AS picked,renamed(h) AS fn FROM whole_collision h(renamed,label)`,
			`CREATE TABLE whole_absent_keys(k integer)`,
			`CREATE TABLE whole_absent_value(i integer,t text)`,
			`CREATE VIEW whole_absence AS SELECT a.k,num_nulls(h.*) AS n,h.*::text AS whole FROM whole_absent_keys a LEFT JOIN whole_absent_value h ON a.k=1`,
			`CREATE VIEW whole_zero AS SELECT a.k,num_nulls(z.*) AS n,z.*::text AS whole FROM whole_absent_keys a LEFT JOIN (SELECT (ROW()).*) z ON a.k=1`)
		for _, step := range []fixture.Step{fixture.Exec(`INSERT INTO builtin_capture(k) VALUES(1)`, 1), fixture.Exec(`INSERT INTO whole_collision VALUES(7,'shadow')`, 1), fixture.Exec(`INSERT INTO whole_absent_keys VALUES(1),(2)`, 2), fixture.Exec(`INSERT INTO whole_absent_value VALUES(NULL,NULL)`, 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder(k) VALUES(1),(2)`, 2))
		captures := func(backend fixture.Backend, executor fixture.Executor) {
			for _, sample := range []struct{ view, projection string }{{"computed_row", "k,f1,f2"}, {"computed_cast", "k,i,t"}, {"computed_coalesce", "k,i,t"}} {
				names := []string{"k", "i", "t"}
				if sample.view == "computed_row" {
					names = []string{"k", "f1", "f2"}
				}
				compositeExprBoth(t, p, backend, executor, `SELECT `+sample.projection+` FROM `+sample.view+` ORDER BY k`, names, []string{"INT4", "INT4", "VARCHAR"}, [][]any{{int64(1), int64(7), "def"}, {int64(2), int64(7), "def"}})
			}
			compositeExprBoth(t, p, backend, executor, `SELECT k,nested_i FROM nested_field ORDER BY k`, []string{"k", "nested_i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), int64(8)}})
			compositeExprBoth(t, p, backend, executor, `SELECT n,s,nn FROM builtin_capture`, []string{"n", "s", "nn"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(0), int64(0)}})
			compositeExprBoth(t, p, backend, executor, `SELECT k,fallback,field,function,scalar FROM builtin_capture_view`, []string{"k", "fallback", "field", "function", "scalar"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(7), int64(0), int64(0)}})
			compositeExprBoth(t, p, backend, executor, `SELECT n,whole FROM whole_source`, []string{"n", "whole"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "(7,shadow)"}})
			compositeExprBoth(t, p, backend, executor, `SELECT k,n FROM whole_outer`, []string{"k", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(0)}})
			compositeExprBoth(t, p, backend, executor, `SELECT k,whole FROM whole_derived`, []string{"k", "whole"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "(,)"}})
			compositeExprBoth(t, p, backend, executor, `SELECT picked,fn FROM whole_alias_field`, []string{"picked", "fn"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
			for _, sample := range []struct{ view, whole string }{{"whole_absence", "(,)"}, {"whole_zero", "()"}} {
				compositeExprBoth(t, p, backend, executor, `SELECT k,n,whole FROM `+sample.view+` ORDER BY k`, []string{"k", "n", "whole"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(0), sample.whole}, {int64(2), int64(1), nil}})
			}
		}
		captures(b, c)
		held, err := b.DB.QueryContext(p.Ctx, `SELECT (v).* FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if !held.Next() {
			t.Fatal("missing first held row", held.Err())
		}
		var first int64
		var txt string
		if err := held.Scan(&first, &txt); err != nil || first != 7 || txt != "def" {
			t.Fatalf("first held %d %q %v", first, txt, err)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				compositeExprBoth(t, p, b, c, `SELECT i,t FROM expanded ORDER BY k`, []string{"i", "t"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(7), "def"}, {int64(7), "def"}})
				compositeExprBoth(t, p, b, c, `SELECT g FROM holder ORDER BY k`, []string{"g"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}})
				captures(b, c)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual emitted SQL replay", err, dump.String())
				}
				pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
				compositeExprBoth(t, p, pg, p.Postgres, `SELECT i,t FROM expanded ORDER BY k`, []string{"i", "t"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(7), "def"}, {int64(7), "def"}})
				captures(pg, p.Postgres)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO holder(k) VALUES(3)`, 1))
				compositeExprBoth(t, p, pg, p.Postgres, `SELECT g FROM holder ORDER BY k`, []string{"g"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}, {int64(8)}})
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`UPDATE holder SET v.i=$1,w.p.i=$2 WHERE k=2`, 1, 10, 11)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder(k) VALUES(3)`, 1))
		compositeExprBoth(t, p, b, c, `SELECT (v).i,g FROM holder ORDER BY k`, []string{"i", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}, {int64(10), int64(11)}, {int64(7), int64(8)}})
		compositeExprBoth(t, p, b, c, `SELECT i,t FROM computed_coalesce ORDER BY k`, []string{"i", "t"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(7), "def"}, {int64(10), "def"}, {int64(7), "def"}})
		compositeExprBoth(t, p, b, c, `SELECT nested_i FROM nested_field ORDER BY k`, []string{"nested_i"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(11)}, {int64(8)}})
		fixture.UpdateCoreRetained(t, b.PG, held, []string{"i", "t"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(7), "def"}}, false)
		// User row types retain stable fields through allowed table-shape DDL.
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE shape(i integer,t text)`, `CREATE TABLE captured(k integer,v shape)`)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE VIEW captured_text AS SELECT (v).t AS selected FROM captured`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO captured VALUES(1,ROW(4,'old'))`, 1))
		stmt, err := c.PrepareContext(p.Ctx, `SELECT (v).t FROM captured`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var before string
		if err := stmt.QueryRowContext(p.Ctx).Scan(&before); err != nil || before != "old" {
			t.Fatal(before, err)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TABLE shape DROP COLUMN i`)
		var after string
		if err := stmt.QueryRowContext(p.Ctx).Scan(&after); err != nil || after != "old" {
			t.Fatal("prepared selected wrong slot", after, err)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TABLE shape RENAME COLUMN t TO renamed`)
		compositeExprBoth(t, p, b, c, `SELECT (v).renamed FROM captured`, []string{"renamed"}, []string{"TEXT"}, [][]any{{"old"}})
		// Retained analyzed field access follows the stable attribute, not its name.
		var retained string
		if err := stmt.QueryRowContext(p.Ctx).Scan(&retained); err != nil || retained != "old" {
			t.Fatal("held field rename lost stable attribute", retained, err)
		}
		compositeExprBoth(t, p, b, c, `SELECT selected FROM captured_text`, []string{"selected"}, []string{"TEXT"}, [][]any{{"old"}})
		compositeExprError(t, p, c, `SELECT (v).t FROM captured`, "42703", `column "t" not found in data type shape`, "v")
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE expansion_shape(i integer,t text)`, `CREATE TABLE expansion_holder(k integer,v expansion_shape)`, `CREATE VIEW captured_expansion AS SELECT k,(v).* FROM expansion_holder`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expansion_holder VALUES(1,ROW(4,'old'))`, 1))
		fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TABLE expansion_shape ADD COLUMN extra integer`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expansion_holder VALUES(2,ROW(5,'new',99))`, 1))
		compositeExprBoth(t, p, b, c, `SELECT * FROM captured_expansion ORDER BY k`, []string{"k", "i", "t"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(4), "old"}, {int64(2), int64(5), "new"}})
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM expansion_holder ORDER BY k`, []string{"i", "t", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(4), "old", nil}, {int64(5), "new", int64(99)}})
		if !b.PG {
			var shapeDump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &shapeDump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(shapeDump.Bytes())); err != nil {
				t.Fatal(err)
			}
			compositeExprBoth(t, p, b, c, `SELECT selected FROM captured_text`, []string{"selected"}, []string{"TEXT"}, [][]any{{"old"}})
			compositeExprBoth(t, p, b, c, `SELECT * FROM captured_expansion ORDER BY k`, []string{"k", "i", "t"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(4), "old"}, {int64(2), int64(5), "new"}})
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, shapeDump.String()); err != nil {
					t.Fatal("actual renamed/append capture replay", err, shapeDump.String())
				}
				pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
				compositeExprBoth(t, p, pg, p.Postgres, `SELECT selected FROM captured_text`, []string{"selected"}, []string{"TEXT"}, [][]any{{"old"}})
				compositeExprBoth(t, p, pg, p.Postgres, `SELECT * FROM captured_expansion ORDER BY k`, []string{"k", "i", "t"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(4), "old"}, {int64(2), int64(5), "new"}})
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

type compositeExprGatedReader struct {
	started, release chan struct{}
	once             sync.Once
	reader           io.Reader
}

func (r *compositeExprGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.reader.Read(p)
}

func compositeExprAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("control I/O barrier deadline")
	}
}
func compositeExprDone(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control worker deadline")
	}
}

func TestReferenceCompositeExprControlRejection(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,ROW(7,'sentinel'),NULL)`, 1))
		check := func() {
			compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "sentinel"}})
		}
		check()
		if b.PG {
			return
		} // Native replacement guarantees; SQL witnesses above are shared.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, x := range []struct{ suffix, code, message, base string }{
			{`CREATE TABLE rejected(v pair DEFAULT ROW($1,'hidden')::pair);`, "42P02", "parameters are not allowed in restore", "$1"},
			{`CREATE TABLE rejected(v pair DEFAULT ROW(1,));`, "42601", `syntax error at or near ")"`, "));"},
			{`INSERT INTO holder VALUES(2,ROW(9,'good'),NULL),(3,ROW('bad','late'),NULL);`, "22P02", `invalid input syntax for type integer: "bad"`, "'bad'"},
		} {
			script := dump.String() + x.suffix
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
			if err == nil {
				t.Error("invalid Restore succeeded", x.suffix)
			} else {
				d := fixture.CompositeReferenceDiagnostic(x.code, x.message)
				d.Position = strconv.Itoa(utf8.RuneCountInString(dump.String()) + utf8.RuneCountInString(x.suffix[:strings.Index(x.suffix, x.base)]) + 1)
				if got := fixture.ExtractDiagnostic(t, err); got != d {
					t.Errorf("Restore diagnostic %#v want %#v", got, d)
				}
			}
			check()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Error("rejected staged catalog/data changed")
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check()
		ctx, cancel = context.WithCancel(p.Ctx)
		r := &compositeExprGatedReader{started: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(dump.Bytes())}
		readerDone := make(chan error, 1)
		readerReleased := false
		defer func() {
			cancel()
			if !readerReleased {
				close(r.release)
				select {
				case <-readerDone:
				case <-time.After(5 * time.Second):
					t.Error("Restore cleanup deadline")
				}
			}
		}()
		go func() { readerDone <- stuntgres.Restore(ctx, p.Native, r) }()
		compositeExprAwait(t, r.started)
		cancel()
		close(r.release)
		readerReleased = true
		select {
		case err := <-readerDone:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("canceled Restore did not join")
		}
		check()
		// Block actual emitted output, mutate while blocked, then replay the coherent
		// captured catalog. No user I/O may retain a catalog lock.
		w := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Dump(p.Ctx, p.Native, w) }()
		released := false
		defer func() {
			if !released {
				close(w.Release)
				compositeExprDone(t, done)
			}
		}()
		compositeExprAwait(t, w.Started)
		writeCtx, writeCancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer writeCancel()
		result, err := c.ExecContext(writeCtx, `UPDATE holder SET v.i=8`)
		if err != nil {
			t.Fatal("external writer held catalog lock", err)
		}
		fixture.LockAffected(t, result, 1)
		close(w.Release)
		released = true
		compositeExprDone(t, done)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(w.Output.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
	})
}
