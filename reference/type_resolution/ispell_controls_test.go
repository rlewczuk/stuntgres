package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ispellPersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	ispellToken(t, p, b, e, "ispell_saved", "rebookings", "{booking,book}")
	ispellToken(t, p, b, e, "ispell_filtered", "unbook", "{}")
	ispellToken(t, p, b, e, "ispell_filtered", "booking", "{booking}")
	regtsSelect(t, p, b, e, "SELECT d.dictname,t.tmplname,r.rolname,d.dictinitoption FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname IN ('ispell_saved','ispell_filtered') ORDER BY d.dictname", []string{"dictname", "tmplname", "rolname", "dictinitoption"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{
		{"ispell_filtered", "ispell", "ispell_recipient", "dictfile = 'tr_ispell_legacy', afffile = 'tr_ispell_legacy', stopwords = 'tr_ispell_book'"},
		{"ispell_saved", "ispell", "ispell_creator", "dictfile = 'tr_ispell_legacy', afffile = 'tr_ispell_legacy'"},
	})
	regtsSelect(t, p, b, e, "SELECT d='ispell_saved'::regdictionary AS same FROM ispell_saved_rows ORDER BY id", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsSelect(t, p, b, e, "SELECT d='ispell_saved'::regdictionary AS same FROM ispell_saved_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
}

func TestReferenceIspellControls(t *testing.T) {
	var emitted []byte
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE ispell_creator", "CREATE ROLE ispell_recipient"} {
			regtsExec(t, p, b, c, q, 0)
		}
		ispellCreate(t, p, b, c, "ispell_saved", "legacy", "")
		ispellCreate(t, p, b, c, "ispell_filtered", "legacy", ",StopWords=tr_ispell_book")
		for _, q := range []string{
			"ALTER TEXT SEARCH DICTIONARY ispell_saved OWNER TO ispell_creator",
			"ALTER TEXT SEARCH DICTIONARY ispell_filtered OWNER TO ispell_recipient",
			"CREATE TABLE ispell_saved_rows(id int,d regdictionary DEFAULT 'ispell_saved'::regdictionary)",
			"CREATE VIEW ispell_saved_view AS SELECT 'ispell_saved'::regdictionary AS d",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO ispell_saved_rows(id) VALUES(1)", 1)
		ispellPersistentCheck(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted SQL is required for PostgreSQL replay")
			}
			for _, q := range []string{"DROP VIEW ispell_saved_view", "DROP TABLE ispell_saved_rows", "DROP TEXT SEARCH DICTIONARY ispell_saved,ispell_filtered"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("replay actual native dump", err)
			}
			ispellPersistentCheck(t, p, b, c)
			regtsExec(t, p, b, c, "INSERT INTO ispell_saved_rows(id) VALUES(2)", 1)
			regtsSelect(t, p, b, c, "SELECT d='ispell_saved'::regdictionary AS same FROM ispell_saved_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		for _, forbidden := range []string{"CREATE TEXT SEARCH TEMPLATE", "CREATE FUNCTION \"pg_catalog\".\"dispell_", "COMPOUNDFLAG", "book/GJUS"} {
			if bytes.Contains(emitted, []byte(forbidden)) {
				t.Fatal("host resources or builtin helpers serialized", forbidden, string(emitted))
			}
		}
		if !bytes.Contains(emitted, []byte("CREATE TEXT SEARCH DICTIONARY")) || !bytes.Contains(emitted, []byte("ispell_creator")) || !bytes.Contains(emitted, []byte("ispell_recipient")) {
			t.Fatal("missing persistent definition or owner", string(emitted))
		}
		old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('ispell_saved','rebookings') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldTypes, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Reset retains startup resources but not SQL catalog definitions.
		ispellCreate(t, p, b, c, "ispell_after_reset", "legacy", "")
		ispellToken(t, p, b, c, "ispell_after_reset", "skies", "{sky}")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		ispellPersistentCheck(t, p, b, c)
		if !old.Next() {
			t.Fatal("detached row missing", old.Err())
		}
		var value any
		if err := old.Scan(&value); err != nil || value != "{booking,book}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		old.Close()
		if len(oldTypes) != 1 || oldTypes[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("detached array metadata", oldTypes)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(emitted, redump.Bytes()) {
			t.Fatal("nondeterministic dictionary redump")
		}
		regtsExec(t, p, b, c, "CREATE TABLE ispell_restore_sentinel(v int)", 0)
		regtsExec(t, p, b, c, "INSERT INTO ispell_restore_sentinel VALUES(42)", 1)
		for _, bad := range []string{
			string(emitted) + "ALTER TEXT SEARCH DICTIONARY ispell_saved (AffFile);",
			string(emitted) + "ALTER TEXT SEARCH DICTIONARY ispell_saved (AffFile=tr_ispell_syntax);",
			string(emitted) + "ALTER TEXT SEARCH DICTIONARY ispell_saved OWNER TO ispell_absent_owner;",
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid restore published")
			}
			ispellPersistentCheck(t, p, b, c)
			regtsSelect(t, p, b, c, "SELECT v FROM ispell_restore_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		reading, stop := context.WithCancel(p.Ctx)
		reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop}
		err = stuntgres.Restore(reading, p.Native, reader)
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reader cancellation", err)
		}
		regtsSelect(t, p, b, c, "SELECT v FROM ispell_restore_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		ispellPersistentCheck(t, p, b, c)
		for _, mode := range []string{"identical", "different", "missing", "malformed"} {
			t.Run(mode, func(t *testing.T) {
				configure := ispellConfigure
				if mode == "missing" {
					configure = nil
				}
				if mode == "malformed" || mode == "different" {
					configure = func(d *stuntgres.Driver) error {
						for _, file := range ispellFiles {
							if mode == "malformed" && file.Name == "tr_ispell_legacy.affix" {
								file.Data = []byte("FLAG UTF-8\n")
							}
							if mode == "different" && file.Name == "tr_ispell_legacy.dict" {
								file.Data = []byte("destination\n")
							}
							if err := d.RegisterTextSearchFile(file); err != nil {
								return err
							}
						}
						return nil
					}
				}
				pool, raw, db := ispellDestination(t, p, configure)
				for _, q := range []string{"CREATE ROLE ispell_creator", "CREATE ROLE ispell_recipient", "CREATE TABLE ispell_destination_sentinel(v int)"} {
					fixture.RunStep(t, p.Ctx, pool, db.Recorder, fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, pool, db.Recorder, fixture.Exec("INSERT INTO ispell_destination_sentinel VALUES(91)", 1))
				err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted))
				if mode == "identical" {
					if err != nil {
						t.Fatal(err)
					}
					ispellPersistentCheck(t, p, db, pool)
					fixture.RunStep(t, p.Ctx, pool, db.Recorder, fixture.Exec("INSERT INTO ispell_saved_rows(id) VALUES(2)", 1))
					regtsSelect(t, p, db, pool, "SELECT d='ispell_saved'::regdictionary AS same FROM ispell_saved_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
				} else if mode == "different" {
					if err != nil {
						t.Fatal(err)
					}
					ispellToken(t, p, db, pool, "ispell_saved", "destination", "{destination}")
					ispellToken(t, p, db, pool, "ispell_saved", "book", nil)
					ispellToken(t, p, db, pool, "ispell_filtered", "destination", "{destination}")
					regtsSelect(t, p, db, pool, "SELECT d='ispell_saved'::regdictionary AS same FROM ispell_saved_rows", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
				} else {
					fixture.State(t, err, "F0000")
					regtsSelect(t, p, db, pool, "SELECT v FROM ispell_destination_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}})
					regtsSelect(t, p, db, pool, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('ispell_saved','ispell_filtered')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				}
			})
		}
	})
}
