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

func porterSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	porterSavedCarriers(t, p, b, c)
	porterAbsentStock(t, p, b, c)
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('porter_saved_stops','ONLY') AS lexemes", "{}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('porter_saved_stops','SKIES') AS lexemes", "{ski}")
	regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='porter_saved_stops'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'porter', stopwords = 'english'"}})
	regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='porter_saved_stops_config'::regconfig AND mapdict='porter_saved_stops'::regdictionary AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}})
	regtsSelect(t, p, b, c, "SELECT d::regdictionary='dictionary_right.porter_saved'::regdictionary AS domain_same,(p).v='dictionary_right.porter_saved'::regdictionary AS scalar_same,(p).d::regdictionary='dictionary_right.porter_saved'::regdictionary AS nested_same FROM porter_nested", []string{"domain_same", "scalar_same", "nested_same"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.porter_saved','ONLY') AS lexemes", "{onli}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.porter_saved','DYING') AS lexemes", "{dy}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.porter_saved','SKIES') AS lexemes", "{ski}")
	regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='dictionary_right.porter_saved'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'PoRtEr'", true}})
	for _, q := range []string{"SELECT d='dictionary_right.porter_saved'::regdictionary AS same FROM porter_rows", "SELECT d='dictionary_right.porter_saved'::regdictionary AS same FROM porter_view", "SELECT a[1]='dictionary_right.porter_saved'::regdictionary AS same FROM porter_rows"} {
		regtsSelect(t, p, b, c, q, []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	}
	regtsExec(t, p, b, c, "INSERT INTO porter_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d='dictionary_right.porter_saved'::regdictionary AS same FROM porter_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsExec(t, p, b, c, "DELETE FROM porter_rows WHERE id=2", 1)
}

func TestReferencePorterSnowballControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG {
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.porter_source (TEMPLATE=simple)", 0)
		}
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE SCHEMA dictionary_right", "CREATE TEXT SEARCH DICTIONARY porter_original (TEMPLATE=snowball,Language='PoRtEr',StopWords=english)", "ALTER TEXT SEARCH DICTIONARY porter_original (stopwords)", "ALTER TEXT SEARCH DICTIONARY porter_original RENAME TO porter_saved", "ALTER TEXT SEARCH DICTIONARY porter_saved SET SCHEMA dictionary_right", "ALTER TEXT SEARCH DICTIONARY dictionary_right.porter_saved OWNER TO dictionary_creator", "CREATE DOMAIN porter_dict_domain AS regdictionary", "CREATE TYPE porter_dict_pair AS (v regdictionary,d porter_dict_domain)", "CREATE TABLE porter_nested(d porter_dict_domain,p porter_dict_pair)", "CREATE TABLE porter_rows(id int,d regdictionary DEFAULT 'dictionary_right.porter_saved'::regdictionary,a regdictionary[] DEFAULT ARRAY['dictionary_right.porter_saved'::regdictionary])", "CREATE VIEW porter_view AS SELECT d FROM porter_rows"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO porter_rows(id) VALUES(1)", 1)
		regtsExec(t, p, b, c, "INSERT INTO porter_nested VALUES('dictionary_right.porter_saved',ROW('dictionary_right.porter_saved'::regdictionary,'dictionary_right.porter_saved'::porter_dict_domain)::porter_dict_pair)", 1)
		porterCreateCarriers(t, p, b, c)
		porterSetup(t, p, b, c, "porter_saved_stops", "porter_saved_stops_config", true)
		porterSaved(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing native stream")
			}
			for _, q := range []string{"DROP TEXT SEARCH CONFIGURATION porter_saved_stops_config", "DROP TEXT SEARCH DICTIONARY porter_saved_stops", "DROP VIEW porter_view", "DROP TABLE porter_rows", "DROP TABLE porter_nested", "DROP TYPE porter_dict_pair", "DROP DOMAIN porter_dict_domain", "DROP SCHEMA dictionary_right CASCADE"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted replay", err)
			}
			porterSaved(t, p, b, c)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		retainedSource, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_right.porter_saved','SKIES') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer retainedSource.Close()
		porterLexizeMetadata(t, b, retainedSource)
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				porterAbsentStock(t, p, b, c)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			porterSaved(t, p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic redump", err)
			}
		}
		porterLexizeMetadata(t, b, retainedSource)
		if !retainedSource.Next() {
			t.Fatal(retainedSource.Err())
		}
		var retainedSourceValue string
		if err := retainedSource.Scan(&retainedSourceValue); err != nil || retainedSourceValue != "{ski}" {
			t.Fatal(retainedSourceValue, err)
		}
		if retainedSource.Next() || retainedSource.Err() != nil {
			t.Fatal(retainedSource.Err())
		}
		retainedSource.Close()
		porterFailedRestore(t, p, b, c, emitted)
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_right.porter_saved'::regdictionary::oid::bigint").Scan(&source); err != nil {
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
		regtsExec(t, fresh, fb, fc, "CREATE ROLE dictionary_peer", 0)
		var collision string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.porter_collision_%d", i)
			regtsExec(t, fresh, fb, fc, "CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=simple,accept=false)", 0)
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regdictionary::oid::bigint", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id > source {
				t.Fatal("same-class collision overshot", id, source)
			}
			if id == source {
				collision = name
				break
			}
		}
		collisionCheck := func() {
			regtsSelect(t, fresh, fb, fc, "SELECT d.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary,d.dictinitoption FROM pg_ts_dict d JOIN pg_namespace n ON n.oid=d.dictnamespace WHERE d.oid=$1::oid AND d.oid=$2::regdictionary::oid", []string{"oid", "temporary", "dictinitoption"}, []string{"INT8", "BOOL", "TEXT"}, [][]any{{source, true, "accept = 'false'"}}, source, collision)
			dictionaryResult(t, fresh, fb, fc, "SELECT ts_lexize($1::regdictionary,'DYING') AS lexemes", nil, collision)
		}
		collisionCheck()
		retained, err := fc.QueryContext(fresh.Ctx, "SELECT ts_lexize($1::regdictionary,'DYING') AS lexemes", collision)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		porterLexizeMetadata(t, fb, retained)
		retainedTypes, err := retained.ColumnTypes()
		if err != nil || len(retainedTypes) != 1 || retainedTypes[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(retainedTypes, err)
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		var target int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'dictionary_right.porter_saved'::regdictionary::oid::bigint").Scan(&target); err != nil || target == source {
			t.Fatal("import did not relocate", target, source, err)
		}
		collisionCheck()
		porterSaved(t, fresh, fb, fc)
		porterFailedRestore(t, fresh, fb, fc, emitted)
		collisionCheck()
		porterLexizeMetadata(t, fb, retained)
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var retainedValue any
		if err := retained.Scan(&retainedValue); err != nil || retainedValue != nil {
			t.Fatal("old collision output", retainedValue, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal(retained.Err())
		}
	})
}

func porterFailedRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TABLE porter_sentinel(v int)", 0)
	regtsExec(t, p, b, c, "INSERT INTO porter_sentinel VALUES(42)", 1)
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_right.porter_saved','ONLY') AS lexemes")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil || meta[0].DatabaseTypeName() != "_TEXT" {
		t.Fatal(meta, err)
	}
	definition := norwegianDefinitionSnapshot(t, p, b, c, "dictionary_right.porter_saved", "dictionary_right.porter_saved_config")
	stoppedDefinition := norwegianDefinitionSnapshot(t, p, b, c, "porter_saved_stops", "porter_saved_stops_config")
	preserved := func() {
		definition()
		stoppedDefinition()
		porterLexizeMetadata(t, b, old)
		porterSaved(t, p, b, c)
		regtsSelect(t, p, b, c, "SELECT v FROM porter_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed Restore changed full destination", err)
		}
	}
	for _, tc := range []struct{ suffix, code, message string }{
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.porter_saved (stopwords=english,accept=true);", "22023", `unrecognized Snowball parameter: "accept"`},
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.porter_saved (stopwords=porter_missing);", "F0000", `could not open stop-word file "porter_missing.stop": No such file or directory`},
		{"SET SESSION AUTHORIZATION dictionary_peer; ALTER TEXT SEARCH DICTIONARY public.porter_saved_stops (stopwords);", "42501", "must be owner of text search dictionary public.porter_saved_stops"},
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
	expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
	defer expire()
	if err := stuntgres.Restore(expired, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.DeadlineExceeded) {
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
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var value string
	if err := old.Scan(&value); err != nil || value != "{onli}" {
		t.Fatal(value, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
}

func TestReferencePorterSnowballCancellation(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY porter_cancel (TEMPLATE=snowball,Language=porter,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION porter_cancel_config (COPY=simple)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION porter_cancel_config ALTER MAPPING FOR asciiword WITH porter_cancel", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('porter_cancel','SKIES') AS lexemes", "{ski}")
		if !b.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, stop := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer stop()
			for _, tc := range []struct {
				ctx  context.Context
				want error
			}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY porter_canceled (TEMPLATE=snowball,Language=porter)", "ALTER TEXT SEARCH DICTIONARY porter_cancel (stopwords)", "DROP TEXT SEARCH DICTIONARY porter_cancel"} {
					for _, prepared := range []bool{false, true} {
						var result sql.Result
						var err error
						if prepared {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							result, err = stmt.ExecContext(tc.ctx)
							stmt.Close()
						} else {
							result, err = c.ExecContext(tc.ctx, q)
						}
						if result != nil || !errors.Is(err, tc.want) {
							t.Fatal("canceled DDL published", q, result, err)
						}
					}
				}
				for _, q := range []string{"SELECT ts_lexize('porter_cancel',$1::text) AS lexemes", "SELECT to_tsvector('porter_cancel_config',$1::text) AS normalized"} {
					for _, prepared := range []bool{false, true} {
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							rows, err = stmt.QueryContext(tc.ctx, "SKIES")
							stmt.Close()
						} else {
							rows, err = c.QueryContext(tc.ctx, q, "SKIES")
						}
						if rows != nil {
							rows.Close()
							t.Fatal("canceled native query returned Rows")
						}
						if !errors.Is(err, tc.want) {
							t.Fatal(err)
						}
					}
				}
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("cancellation published", err)
			}
		}
		var committed bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
				t.Fatal(err)
			}
		}
		var identity int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'porter_cancel'::regdictionary::oid::bigint").Scan(&identity); err != nil || identity <= 0 {
			t.Fatal("dictionary identity", identity, err)
		}
		var template, configID, configOwner, configNamespace, parser int64
		if err := c.QueryRowContext(p.Ctx, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid='porter_cancel'::regdictionary").Scan(&template); err != nil {
			t.Fatal(err)
		}
		const configSnapshot = "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid='porter_cancel_config'::regconfig"
		if err := c.QueryRowContext(p.Ctx, configSnapshot).Scan(&configID, &configOwner, &configNamespace, &parser); err != nil {
			t.Fatal(err)
		}
		fullSnapshot := norwegianDefinitionSnapshot(t, p, b, c, "porter_cancel", "porter_cancel_config")
		preserved := func(t *testing.T, owner *sql.Conn) {
			t.Helper()
			fullSnapshot()
			regtsSelect(t, p, b, owner, `SELECT d.oid::bigint,d.dictinitoption,
				d.dictnamespace='public'::regnamespace AS namespace,
				d.dictowner=current_user::regrole::oid AS owned,
				t.tmplname::text,n.nspname::text
				FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate
				JOIN pg_namespace n ON n.oid=t.tmplnamespace
				WHERE d.oid='porter_cancel'::regdictionary`,
				[]string{"oid", "dictinitoption", "namespace", "owned", "tmplname", "nspname"},
				[]string{"INT8", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT"},
				[][]any{{identity, "language = 'porter', stopwords = 'english'", true, true, "snowball", "pg_catalog"}})
			regtsSelect(t, p, b, owner, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid='porter_cancel'::regdictionary", []string{"dicttemplate"}, []string{"INT8"}, [][]any{{template}})
			regtsSelect(t, p, b, owner, configSnapshot, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{configID, configOwner, configNamespace, parser}})
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('porter_cancel',$1::text) AS lexemes", "{ski}", "SKIES")
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('porter_cancel',$1::text) AS lexemes", "{}", "ONLY")
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('porter_cancel',$1::text) AS lexemes", "{dy}", "DYING")
			regtsSelect(t, p, b, owner, "SELECT mapseqno,mapdict='porter_cancel'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='porter_cancel_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, owner, "SELECT to_tsvector('porter_cancel_config','SKIES DYING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'dy':2 'ski':1"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(committed.Bytes(), after.Bytes()) {
					t.Fatal("admitted cancellation changed committed state", err)
				}
			}
		}
		preserved(t, c)
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("admitted/prepared=%t", prepared), func(t *testing.T) {
				owner, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				pid := fixture.OutputGatePID(t, p.Ctx, owner)
				// Admission requires real parameterized Porter output.
				// Cancellation is after lexization, not inside the stemmer.
				const query = `SELECT pg_advisory_xact_lock(CASE
					WHEN ts_lexize('porter_cancel',$1::text)=ARRAY['ski']::text[]
					THEN 67108864::bigint ELSE 2::bigint END)`
				var stmt *sql.Stmt
				if prepared {
					stmt, err = owner.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				fixture.OutputGateCancel(t, p, b, owner, false, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(ctx, "SKIES")
					} else {
						rows, err = owner.QueryContext(ctx, query, "SKIES")
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if after := fixture.OutputGatePID(t, p.Ctx, owner); after != pid {
					t.Fatal("cancellation replaced physical owner", pid, after)
				}
				preserved(t, owner)
			})
			preserved(t, c)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('porter_cancel','ONLY') AS lexemes", "{}")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='porter_canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
