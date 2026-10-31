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

func yiddishSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, pair := range [][2]string{{"ONLY", "{only}"}, {"קינדער", "{קינד}"}, {"RATS", "{rats}"}} {
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.yiddish_saved',$1::text) AS lexemes", pair[1], pair[0])
	}
	regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='dictionary_right.yiddish_saved'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'yiddish'", true}})
	regtsSelect(t, p, b, c, "SELECT cfgowner='dictionary_creator'::regrole::oid AS owned,cfgparser::bigint FROM pg_ts_config WHERE oid='dictionary_right.yiddish_config'::regconfig", []string{"owned", "cfgparser"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(3722)}})
	regtsSelect(t, p, b, c, "SELECT to_tsvector('dictionary_right.yiddish_config','ONLY קינדער RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'rats':3 'קינד':2"}})
	for _, q := range []string{
		"SELECT d='dictionary_right.yiddish_saved'::regdictionary AS same FROM yiddish_rows",
		"SELECT a[1]='dictionary_right.yiddish_saved'::regdictionary AS same FROM yiddish_rows",
		"SELECT cfg='dictionary_right.yiddish_config'::regconfig AS same FROM yiddish_rows",
		"SELECT ca[1]='dictionary_right.yiddish_config'::regconfig AS same FROM yiddish_rows",
		"SELECT d='dictionary_right.yiddish_saved'::regdictionary AS same FROM yiddish_view",
		"SELECT cfg='dictionary_right.yiddish_config'::regconfig AS same FROM yiddish_view",
	} { regtsSelect(t, p, b, c, q, []string{"same"}, []string{"BOOL"}, [][]any{{true}}) }
	regtsSelect(t, p, b, c, "SELECT d::regdictionary='dictionary_right.yiddish_saved'::regdictionary AS domain_same,(p).d::regdictionary='dictionary_right.yiddish_saved'::regdictionary AS nested_dict,(p).c::regconfig='dictionary_right.yiddish_config'::regconfig AS nested_config FROM yiddish_nested", []string{"domain_same", "nested_dict", "nested_config"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
	regtsExec(t, p, b, c, "INSERT INTO yiddish_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d='dictionary_right.yiddish_saved'::regdictionary AS dictionary,cfg='dictionary_right.yiddish_config'::regconfig AS configuration FROM yiddish_rows WHERE id=2", []string{"dictionary", "configuration"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
	regtsExec(t, p, b, c, "DELETE FROM yiddish_rows WHERE id=2", 1)
}

func TestReferenceYiddishSnowballControls(t *testing.T) {
	t.Run("stock-options", yiddishStockOptions)
	t.Run("stock-identities", yiddishStockIdentities)
	t.Run("extension-membership", yiddishMembershipControls)
	t.Run("stock-drops", yiddishStockDrops)
	t.Run("absent-identity-replay", yiddishAbsentIdentityControls)
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG { regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.yiddish_source (TEMPLATE=simple)", 0) }
		for _, q := range []string{
			"CREATE ROLE dictionary_creator", "CREATE SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY yiddish_original (TEMPLATE=snowball,Language=yiddish,StopWords=english)",
			"ALTER TEXT SEARCH DICTIONARY yiddish_original (stopwords)",
			"ALTER TEXT SEARCH DICTIONARY yiddish_original RENAME TO yiddish_saved",
			"ALTER TEXT SEARCH DICTIONARY yiddish_saved SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.yiddish_saved OWNER TO dictionary_creator",
			"CREATE TEXT SEARCH CONFIGURATION yiddish_config (COPY=yiddish)",
			"ALTER TEXT SEARCH CONFIGURATION yiddish_config ALTER MAPPING FOR asciiword,word WITH dictionary_right.yiddish_saved",
			"ALTER TEXT SEARCH CONFIGURATION yiddish_config SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH CONFIGURATION dictionary_right.yiddish_config OWNER TO dictionary_creator",
			"CREATE DOMAIN yiddish_dict_domain AS regdictionary", "CREATE DOMAIN yiddish_config_domain AS regconfig",
			"CREATE TYPE yiddish_pair AS (d yiddish_dict_domain,c yiddish_config_domain)",
			"CREATE TABLE yiddish_nested(d yiddish_dict_domain,p yiddish_pair)",
			"CREATE TABLE yiddish_rows(id int,d regdictionary DEFAULT 'dictionary_right.yiddish_saved'::regdictionary,a regdictionary[] DEFAULT ARRAY['dictionary_right.yiddish_saved'::regdictionary],cfg regconfig DEFAULT 'dictionary_right.yiddish_config'::regconfig,ca regconfig[] DEFAULT ARRAY['dictionary_right.yiddish_config'::regconfig])",
			"CREATE VIEW yiddish_view AS SELECT d,cfg FROM yiddish_rows",
		} { regtsExec(t, p, b, c, q, 0) }
		regtsExec(t, p, b, c, "INSERT INTO yiddish_rows(id) VALUES(1)", 1)
		regtsExec(t, p, b, c, "INSERT INTO yiddish_nested VALUES('dictionary_right.yiddish_saved',ROW('dictionary_right.yiddish_saved'::yiddish_dict_domain,'dictionary_right.yiddish_config'::yiddish_config_domain)::yiddish_pair)", 1)
		yiddishSaved(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 { t.Fatal("missing actual native stream") }
			for _, q := range []string{"DROP VIEW yiddish_view", "DROP TABLE yiddish_rows", "DROP TABLE yiddish_nested", "DROP TYPE yiddish_pair", "DROP DOMAIN yiddish_dict_domain", "DROP DOMAIN yiddish_config_domain", "DROP SCHEMA dictionary_right CASCADE"} { regtsExec(t, p, b, c, q, 0) }
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal("actual emitted replay", err) }
			yiddishSaved(t, p, b, c)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
		emitted = bytes.Clone(out.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','ONLY') AS lexemes", "{only}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
			yiddishSaved(t, p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) { t.Fatal("deterministic redump", err) }
		}
		yiddishFailedRestore(t, p, b, c, emitted)
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_right.yiddish_saved'::regdictionary::oid::bigint").Scan(&source); err != nil { t.Fatal(err) }
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil { t.Fatal(err) }
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		regtsExec(t, fresh, fb, fc, "CREATE ROLE dictionary_creator", 0)
		var collision string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.yiddish_collision_%d", i)
			regtsExec(t, fresh, fb, fc, "CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=simple,accept=false)", 0)
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regdictionary::oid::bigint", name).Scan(&id); err != nil { t.Fatal(err) }
			if id > source { t.Fatal("same-class collision overshot", id, source) }
			if id == source { collision = name; break }
		}
		collisionCheck := func() {
			regtsSelect(t, fresh, fb, fc, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::oid AND oid=$2::regdictionary::oid", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{source, "accept = 'false'"}}, source, collision)
			dictionaryResult(t, fresh, fb, fc, "SELECT ts_lexize($1::regdictionary,'קינדער') AS lexemes", nil, collision)
		}
		collisionCheck()
		retained, err := fc.QueryContext(fresh.Ctx, "SELECT ts_lexize($1::regdictionary,'קינדער') AS lexemes", collision)
		if err != nil { t.Fatal(err) }
		defer retained.Close()
		lithuanianLexizeMetadata(t, fb, retained)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
		var target int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'dictionary_right.yiddish_saved'::regdictionary::oid::bigint").Scan(&target); err != nil || target == source { t.Fatal("no class-qualified remint", source, target, err) }
		collisionCheck()
		yiddishSaved(t, fresh, fb, fc)
		yiddishFailedRestore(t, fresh, fb, fc, emitted)
		collisionCheck()
		if !retained.Next() { t.Fatal(retained.Err()) }
		var value any
		if err := retained.Scan(&value); err != nil || value != nil { t.Fatal("retained collision output", value, err) }
		if retained.Next() || retained.Err() != nil { t.Fatal(retained.Err()) }
	})
}

func yiddishFailedRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TABLE yiddish_sentinel(v int)", 0)
	regtsExec(t, p, b, c, "INSERT INTO yiddish_sentinel VALUES(42)", 1)
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil { t.Fatal(err) }
	old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_right.yiddish_saved','ONLY') AS lexemes")
	if err != nil { t.Fatal(err) }
	defer old.Close()
	lithuanianLexizeMetadata(t, b, old)
	preserved := func() {
		yiddishSaved(t, p, b, c)
		regtsSelect(t, p, b, c, "SELECT v FROM yiddish_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) { t.Fatal("failed Restore changed whole destination", err) }
	}
	for _, tc := range []struct { suffix, code, message string }{
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.yiddish_saved (stopwords=english,accept=true);", "22023", `unrecognized Snowball parameter: "accept"`},
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.yiddish_saved (stopwords=yiddish);", "F0000", `could not open stop-word file "yiddish.stop": No such file or directory`},
		{"SET SESSION AUTHORIZATION dictionary_creator; ALTER TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem (stopwords);", "42501", "must be owner of text search dictionary pg_catalog.yiddish_stem"},
	} {
		d := dictionaryDiagnostic(tc.code, tc.message)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+tc.suffix)), &d)
		preserved()
	}
	canceled, cancel := context.WithCancel(p.Ctx); cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	preserved()
	expired, stop := context.WithDeadline(p.Ctx, time.Unix(1, 0)); defer stop()
	if err := stuntgres.Restore(expired, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.DeadlineExceeded) { t.Fatal(err) }
	preserved()
	reading, end := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: end})
	end()
	if !errors.Is(err, context.Canceled) { t.Fatal(err) }
	preserved()
	if !old.Next() { t.Fatal(old.Err()) }
	var value string
	if err := old.Scan(&value); err != nil || value != "{only}" { t.Fatal(value, err) }
	if old.Next() || old.Err() != nil { t.Fatal(old.Err()) }
}
