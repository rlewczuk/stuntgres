package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// A dictionary in the session's temporary namespace is retained by Restore.
// Require an exact same-class collision, not an allocation beyond the source.
func dictionaryTemporaryCollision(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, sourceOID int64) func() {
	t.Helper()
	for i := 0; ; i++ {
		name := fmt.Sprintf("dictionary_collision_%d", i)
		qualified := "pg_temp." + name
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+qualified+" (TEMPLATE=pg_catalog.simple,accept=false)", 0)
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT $1::regdictionary::oid::bigint", qualified).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if oid > sourceOID {
			t.Fatalf("temporary dictionary overshot exact collision: got %d want %d", oid, sourceOID)
		}
		if oid != sourceOID {
			continue
		}
		check := func() {
			// pg_my_temp_schema establishes the actual owning namespace, not
			// merely a pg_temp-looking persistent name.
			regtsSelect(t, p, b, c, "SELECT d.oid::bigint AS id,d.dictname,d.dictnamespace=pg_my_temp_schema() AS temporary,d.dicttemplate::bigint AS template,d.dictinitoption,r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.oid=$1::oid", []string{"id", "dictname", "temporary", "template", "dictinitoption", "rolname"}, []string{"INT8", "NAME", "BOOL", "INT8", "TEXT", "NAME"}, [][]any{{sourceOID, name, true, int64(3727), "accept = 'false'", "postgres"}}, sourceOID)
			regtsSelect(t, p, b, c, "SELECT $1::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{sourceOID}}, qualified)
			dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,'YeS') AS lexemes", nil, sourceOID)
			dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::regdictionary,'') AS lexemes", "{}", qualified)
		}
		check()
		return check
	}
}

func dictionaryPersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_saved','The') AS lexemes", "{}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_saved','WORD') AS lexemes", "{word}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_filter','YeS') AS lexemes", nil)
	regtsSelect(t, p, b, c, "SELECT dictname,dictinitoption,r.rolname,t.tmplname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.dictname='dictionary_saved' OR d.dictname='dictionary_filter' ORDER BY dictname", []string{"dictname", "dictinitoption", "rolname", "tmplname"}, []string{"NAME", "TEXT", "NAME", "NAME"}, [][]any{{"dictionary_filter", "stopwords = 'english', accept = 'false'", "dictionary_recipient", "simple"}, {"dictionary_saved", "stopwords = 'english'", "dictionary_creator", "simple"}})
	regtsSelect(t, p, b, c, "SELECT d='dictionary_saved'::regdictionary AS same FROM dictionary_rows", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsSelect(t, p, b, c, "SELECT d='dictionary_saved'::regdictionary AS same FROM dictionary_saved_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsExec(t, p, b, c, "INSERT INTO dictionary_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d='dictionary_saved'::regdictionary AS same FROM dictionary_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsExec(t, p, b, c, "DELETE FROM dictionary_rows WHERE id=2", 1)
}

func TestReferenceDictionaryControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG {
			// Give the source a real temporary namespace before assigning the
			// imported identities; the target independently fills that range.
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.dictionary_source (TEMPLATE=pg_catalog.simple)", 0)
		}
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_recipient", "CREATE TEXT SEARCH DICTIONARY dictionary_saved (TEMPLATE=simple,stopwords=english)", "CREATE TEXT SEARCH DICTIONARY dictionary_filter (TEMPLATE=simple,stopwords=english,accept=false)", "ALTER TEXT SEARCH DICTIONARY dictionary_saved OWNER TO dictionary_creator", "ALTER TEXT SEARCH DICTIONARY dictionary_filter OWNER TO dictionary_recipient", "CREATE TABLE dictionary_rows(id int,d regdictionary DEFAULT 'dictionary_saved'::regdictionary)", "CREATE VIEW dictionary_saved_view AS SELECT 'dictionary_saved'::regdictionary AS d"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO dictionary_rows(id) VALUES(1)", 1)
		dictionaryPersistentCheck(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted SQL is required")
			}
			for _, q := range []string{"DROP VIEW dictionary_saved_view", "DROP TABLE dictionary_rows", "DROP TEXT SEARCH DICTIONARY dictionary_saved,dictionary_filter"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("replay actual emitted dictionary SQL", err)
			}
			dictionaryPersistentCheck(t, p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if !bytes.Contains(emitted, []byte("CREATE TEXT SEARCH DICTIONARY")) || bytes.Contains(emitted, []byte("CREATE TEXT SEARCH DICTIONARY \"pg_catalog\".\"simple\"")) {
			t.Fatal("dictionary dump classification", string(emitted))
		}
		old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_saved','The') AS lexemes")
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
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('simple','WORD') AS lexemes", "{word}")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		dictionaryPersistentCheck(t, p, b, c)
		if !old.Next() {
			t.Fatal("retained lexize row", old.Err())
		}
		var value any
		if err := old.Scan(&value); err != nil || value != "{}" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		if oldTypes[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("retained dictionary metadata")
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(emitted, redump.Bytes()) {
			t.Fatal("dictionary redump changed")
		}
		regtsExec(t, p, b, c, "CREATE TABLE dictionary_sentinel(v int)", 0)
		regtsExec(t, p, b, c, "INSERT INTO dictionary_sentinel VALUES(42)", 1)
		for _, bad := range []string{string(emitted) + "ALTER TEXT SEARCH DICTIONARY dictionary_saved (accept=false,bad=1);", string(emitted) + "ALTER TEXT SEARCH DICTIONARY dictionary_saved (stopwords=missing_dictionary);", string(emitted) + "ALTER TEXT SEARCH DICTIONARY dictionary_saved OWNER TO dictionary_missing_owner;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid dictionary restore published")
			}
			dictionaryPersistentCheck(t, p, b, c)
			regtsSelect(t, p, b, c, "SELECT v FROM dictionary_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled dictionary restore", err)
		}
		reading, stopReading := context.WithCancel(p.Ctx)
		interrupted := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stopReading}
		err = stuntgres.Restore(reading, p.Native, interrupted)
		stopReading()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("dictionary reader cancellation", err)
		}
		regtsSelect(t, p, b, c, "SELECT v FROM dictionary_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		dictionaryPersistentCheck(t, p, b, c)
		// A live retained dictionary occupies exactly the imported identity.
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_saved'::regdictionary::oid::bigint").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_recipient"} {
			regtsExec(t, fresh, fb, fc, q, 0)
		}
		checkCollision := dictionaryTemporaryCollision(t, fresh, fb, fc, sourceOID)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		var targetOID int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'dictionary_saved'::regdictionary::oid::bigint").Scan(&targetOID); err != nil || sourceOID == targetOID {
			t.Fatal("dictionary identity remap", sourceOID, targetOID, err)
		}
		checkCollision()
		dictionaryPersistentCheck(t, fresh, fb, fc)
	})
}

func TestReferenceDictionaryFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY public.dictionary_smoke (TEMPLATE=pg_catalog.simple,StopWords=english)", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('public.dictionary_smoke','WORD') AS lexemes", "{word}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('public.dictionary_smoke','The') AS lexemes", "{}")
		constantSQL := "SELECT ts_lexize('public.dictionary_smoke'::regdictionary,'YeS'::text) AS lexemes"
		constantStatement, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		constant := fixture.HeldStatement{Executor: c, Statement: constantStatement, SQL: constantSQL}
		defer constantStatement.Close()
		dictionaryResult(t, p, b, constant, constantSQL, "{yes}")
		q := "SELECT ts_lexize('public.dictionary_smoke'::regdictionary,$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		defer stmt.Close()
		dictionaryResult(t, p, b, held, q, "{yes}", "YeS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke (accept=false)", 0)
		dictionaryResult(t, p, b, constant, constantSQL, "{yes}")
		dictionaryResult(t, p, b, c, constantSQL, nil)
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, nil)
		fresh.Close()
		dictionaryResult(t, p, b, held, q, nil, "YeS")
		dictionaryResult(t, p, b, held, q, "{}", "The")
		dictionaryResult(t, p, b, held, q, "{}", "")
		dictionaryResult(t, p, b, held, q, nil, nil)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke (accept)", 0)
		dictionaryResult(t, p, b, held, q, "{yes}", "YeS")
		dictionaryResult(t, p, b, constant, constantSQL, "{yes}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke (accept=false,bad=1)", "22023", `unrecognized simple dictionary parameter: "bad"`)
		missingFile := dictionaryDiagnostic("F0000", `could not open stop-word file "missing_dictionary.stop": No such file or directory`)
		fileStep := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke (stopwords=missing_dictionary)", missingFile)
		pgFile := dictionaryDiagnostic("F0000", `could not open stop-word file "/usr/share/postgresql/18/tsearch_data/missing_dictionary.stop": No such file or directory`)
		fileStep.PostgresDiagnostic = &pgFile
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fileStep)
		dictionaryResult(t, p, b, held, q, "{yes}", "YeS")
		stmt.Close()
		dictionaryResult(t, p, b, constant, constantSQL, "{yes}")
		constantStatement.Close()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke RENAME TO dictionary_smoke_renamed", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_smoke_renamed','The') AS lexemes", "{}")
		creator := "postgres"
		if b.PG {
			creator = "stuntgres"
		}
		regtsSelect(t, p, b, c, "SELECT t.tmplname,d.dictinitoption,r.rolname FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='dictionary_smoke_renamed'", []string{"tmplname", "dictinitoption", "rolname"}, []string{"NAME", "TEXT", "NAME"}, [][]any{{"simple", "stopwords = 'english'", creator}})
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		defer regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		var memberOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.unaccent'::regdictionary::oid::bigint").Scan(&memberOID); err != nil {
			t.Fatal(err)
		}
		dictionaryRejectMissingRules(t, p, b, c, "public.unaccent")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_smoke_missing (TEMPLATE=public.unaccent)", "22023", "missing Rules parameter")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='dictionary_smoke_missing'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		dictionaryExtensionState(t, p, b, c, "public", "unaccent", "public", creator, "rules = 'unaccent'")
		for _, sql := range []string{"CREATE SCHEMA dictionary_right", "ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO dictionary_smoke_member", "ALTER TEXT SEARCH DICTIONARY public.dictionary_smoke_member SET SCHEMA dictionary_right"} {
			regtsExec(t, p, b, c, sql, 0)
		}
		dictionaryExtensionState(t, p, b, c, "dictionary_right", "dictionary_smoke_member", "public", creator, "rules = 'unaccent'")
		regtsSelect(t, p, b, c, "SELECT 'dictionary_right.dictionary_smoke_member'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{memberOID}})
		if !b.PG {
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
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_smoke_renamed','The') AS lexemes", "{}")
			dictionaryExtensionState(t, p, b, c, "dictionary_right", "dictionary_smoke_member", "public", creator, "rules = 'unaccent'")
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("smoke redump", err)
			}
		}
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_smoke_renamed", 0)
		missing := dictionaryDiagnostic("42704", `text search dictionary "dictionary_smoke_renamed" does not exist`)
		missing.Position = "18"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT ts_lexize('dictionary_smoke_renamed','word')", missing))
	})
}
