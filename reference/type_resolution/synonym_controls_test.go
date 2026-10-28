package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func synonymSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.synonym_saved','POSTGRES') AS lexemes", "{pgsql}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.synonym_saved','indices') AS lexemes", "{index}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_sensitive','POSTGRES') AS lexemes", nil)
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_sensitive','postgres') AS lexemes", "{pgsql}")
	regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned,dicttemplate=3730 AS genuine FROM pg_ts_dict WHERE oid='dictionary_right.synonym_saved'::regdictionary", []string{"dictinitoption", "owned", "genuine"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"synonyms = 'synonym_sample'", true, true}})
	regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='synonym_sensitive'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"synonyms = 'synonym_sample', casesensitive = 'true'"}})
	regtsSelect(t, p, b, c, "SELECT oid::bigint,tmplnamespace='dictionary_right'::regnamespace AS moved,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='synonym_seed'", []string{"oid", "moved", "tmplinit", "tmpllexize"}, []string{"INT8", "BOOL", "INT8", "INT8"}, [][]any{{int64(3730), true, int64(3728), int64(3729)}})
	synonymConverted(t, p, b, c, "SELECT to_tsquery('synonym_saved_config','indices') AS normalized", "TSQUERY", "'index':*")
	synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_saved_config','postgres WORD') AS normalized", "TSVECTOR", "'pgsql':1 'word':2")
	for _, q := range []string{"SELECT d='dictionary_right.synonym_saved'::regdictionary AS same FROM synonym_rows", "SELECT d='dictionary_right.synonym_saved'::regdictionary AS same FROM synonym_view", "SELECT a[1]='dictionary_right.synonym_saved'::regdictionary AS same FROM synonym_rows"} {
		regtsSelect(t, p, b, c, q, []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	}
	regtsExec(t, p, b, c, "INSERT INTO synonym_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d='dictionary_right.synonym_saved'::regdictionary AS same,a[1]='dictionary_right.synonym_saved'::regdictionary AS array_same FROM synonym_rows WHERE id=2", []string{"same", "array_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
	regtsExec(t, p, b, c, "DELETE FROM synonym_rows WHERE id=2", 1)
	_ = synonymSnapshot(t, p, c, "dictionary_right.synonym_saved")
}

func TestReferenceSynonymControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG {
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.synonym_source (TEMPLATE=simple)", 0)
		}
		for _, q := range []string{
			"CREATE ROLE dictionary_creator", "CREATE SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY synonym_original (TEMPLATE=synonym,Synonyms=synonym_sample,CaseSensitive=true)",
			"ALTER TEXT SEARCH DICTIONARY synonym_original (casesensitive)",
			"ALTER TEXT SEARCH DICTIONARY synonym_original RENAME TO synonym_saved",
			"ALTER TEXT SEARCH DICTIONARY synonym_saved SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.synonym_saved OWNER TO dictionary_creator",
			"CREATE TEXT SEARCH DICTIONARY synonym_sensitive (TEMPLATE=synonym,Synonyms=synonym_sample,CaseSensitive=true)",
			"CREATE TABLE synonym_rows(id int,d regdictionary DEFAULT 'dictionary_right.synonym_saved'::regdictionary,a regdictionary[] DEFAULT ARRAY['dictionary_right.synonym_saved'::regdictionary])",
			"CREATE VIEW synonym_view AS SELECT d FROM synonym_rows",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		synonymConfig(t, p, b, c, "synonym_saved_config", "dictionary_right.synonym_saved")
		regtsExec(t, p, b, c, "INSERT INTO synonym_rows(id) VALUES(1)", 1)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH TEMPLATE pg_catalog.synonym RENAME TO synonym_seed", 0)
		defer func() {
			// Return a real shared PostgreSQL seed to its canonical placement,
			// including when an assertion stops the replay phase early.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.ExecContext(ctx, "ALTER TEXT SEARCH TEMPLATE dictionary_right.synonym_seed SET SCHEMA pg_catalog")
			_, _ = c.ExecContext(ctx, "ALTER TEXT SEARCH TEMPLATE pg_catalog.synonym_seed RENAME TO synonym")
		}()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH TEMPLATE pg_catalog.synonym_seed SET SCHEMA dictionary_right", 0)
		synonymSaved(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native synonym stream")
			}
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH TEMPLATE dictionary_right.synonym_seed SET SCHEMA pg_catalog", 0)
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH TEMPLATE pg_catalog.synonym_seed RENAME TO synonym", 0)
			for _, q := range []string{"DROP VIEW synonym_view", "DROP TABLE synonym_rows", "DROP TEXT SEARCH CONFIGURATION synonym_saved_config", "DROP TEXT SEARCH DICTIONARY synonym_sensitive", "DROP SCHEMA dictionary_right CASCADE"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted synonym replay", err)
			}
			synonymSaved(t, p, b, c)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				regtsSelect(t, p, b, c, "SELECT oid::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='synonym' AND tmplnamespace='pg_catalog'::regnamespace", []string{"oid", "tmplinit", "tmpllexize"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3730), int64(3728), int64(3729)}})
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			synonymSaved(t, p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic synonym redump", err)
			}
		}
		synonymFailedRestore(t, p, b, c, emitted)
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_right.synonym_saved'::regdictionary::oid::bigint").Scan(&source); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		regtsExec(t, fresh, fb, fc, "CREATE ROLE dictionary_creator", 0)
		var collision string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.synonym_collision_%d", i)
			regtsExec(t, fresh, fb, fc, "CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=synonym,Synonyms=synonym_sample,CaseSensitive=true)", 0)
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regdictionary::oid::bigint", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id > source {
				t.Fatal("same-class synonym collision overshot", id, source)
			}
			if id == source {
				collision = name
				break
			}
		}
		collisionCheck := func() {
			regtsSelect(t, fresh, fb, fc, "SELECT d.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary,d.dictinitoption,d.dicttemplate::bigint FROM pg_ts_dict d JOIN pg_namespace n ON n.oid=d.dictnamespace WHERE d.oid=$1::oid AND d.oid=$2::regdictionary::oid", []string{"oid", "temporary", "dictinitoption", "dicttemplate"}, []string{"INT8", "BOOL", "TEXT", "INT8"}, [][]any{{source, true, "synonyms = 'synonym_sample', casesensitive = 'true'", int64(3730)}}, source, collision)
			dictionaryResult(t, fresh, fb, fc, "SELECT ts_lexize($1::regdictionary,'POSTGRES') AS lexemes", nil, collision)
			dictionaryResult(t, fresh, fb, fc, "SELECT ts_lexize($1::regdictionary,'postgres') AS lexemes", "{pgsql}", collision)
		}
		collisionCheck()
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		held := synonymHeld(t, fresh, fc, q)
		defer held.Statement.Close()
		old, err := fc.QueryContext(fresh.Ctx, q, collision, "postgres")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil || meta[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(meta, err)
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		var target int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'dictionary_right.synonym_saved'::regdictionary::oid::bigint").Scan(&target); err != nil || target == source {
			t.Fatal("synonym import did not relocate", target, source, err)
		}
		collisionCheck()
		dictionaryResult(t, fresh, fb, held, q, nil, collision, "POSTGRES")
		dictionaryResult(t, fresh, fb, held, q, "{pgsql}", "dictionary_right.synonym_saved", "POSTGRES")
		synonymSaved(t, fresh, fb, fc)
		synonymFailedRestore(t, fresh, fb, fc, emitted)
		collisionCheck()
		if !old.Next() {
			t.Fatal("retained collision row", old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "{pgsql}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
	})
}

func synonymFailedRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TABLE synonym_sentinel(v int)", 0)
	regtsExec(t, p, b, c, "INSERT INTO synonym_sentinel VALUES(42)", 1)
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	state := synonymSnapshot(t, p, c, "dictionary_right.synonym_saved")
	old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_right.synonym_saved','POSTGRES') AS lexemes")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil || meta[0].DatabaseTypeName() != "_TEXT" {
		t.Fatal(meta, err)
	}
	preserved := func() {
		synonymSaved(t, p, b, c)
		synonymUnchanged(t, p, c, "dictionary_right.synonym_saved", state)
		regtsSelect(t, p, b, c, "SELECT v FROM synonym_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed control changed complete synonym destination", err)
		}
	}
	for _, tc := range []struct{ suffix, code, message string }{
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.synonym_saved (casesensitive=true,unknown=1);", "22023", `unrecognized synonym parameter: "unknown"`},
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.synonym_saved (synonyms=synonym_missing);", "F0000", `could not open synonym file "synonym_missing.syn": No such file or directory`},
		{"SET SESSION AUTHORIZATION dictionary_creator; ALTER TEXT SEARCH DICTIONARY synonym_sensitive (casesensitive);", "42501", "must be owner of text search dictionary synonym_sensitive"},
	} {
		d := dictionaryDiagnostic(tc.code, tc.message)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+tc.suffix)), &d)
		preserved()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	preserved()
	reading, stop := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop})
	stop()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	preserved()
	if err := stuntgres.Dump(ctx, p.Native, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	preserved()
	failure := errors.New("synonym dump writer failed")
	if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	preserved()
	if !old.Next() {
		t.Fatal("retained synonym result missing", old.Err())
	}
	var value string
	if err := old.Scan(&value); err != nil || value != "{pgsql}" {
		t.Fatal(value, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
}
