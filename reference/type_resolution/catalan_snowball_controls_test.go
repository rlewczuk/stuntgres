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

func catalanSaved(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.catalan_saved','ONLY') AS lexemes", "{only}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.catalan_saved','GATS') AS lexemes", "{gat}")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.catalan_saved','café') AS lexemes", "{caf}")
	regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='dictionary_right.catalan_saved'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'catalan'", true}})
	for _, q := range []string{"SELECT d='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows", "SELECT d='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_view", "SELECT a[1]='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows", "SELECT dom::regdictionary='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows", "SELECT (pair).v='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows", "SELECT (pair).d::regdictionary='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows"} {
		regtsSelect(t, p, b, c, q, []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	}
	regtsExec(t, p, b, c, "INSERT INTO catalan_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d='dictionary_right.catalan_saved'::regdictionary AS same FROM catalan_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
	regtsExec(t, p, b, c, "DELETE FROM catalan_rows WHERE id=2", 1)
}

func TestReferenceCatalanSnowballControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !b.PG {
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.catalan_source (TEMPLATE=simple)", 0)
		}
		for _, q := range []string{
			"CREATE ROLE dictionary_creator",
			"CREATE SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY catalan_original (TEMPLATE=snowball,Language=catalan,StopWords=english)",
			"ALTER TEXT SEARCH DICTIONARY catalan_original (stopwords)",
			"ALTER TEXT SEARCH DICTIONARY catalan_original RENAME TO catalan_saved",
			"ALTER TEXT SEARCH DICTIONARY catalan_saved SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_saved OWNER TO dictionary_creator",
			"CREATE DOMAIN catalan_dictionary_domain AS regdictionary",
			"CREATE TYPE catalan_dictionary_pair AS (v regdictionary,d catalan_dictionary_domain)",
			"CREATE TABLE catalan_rows(id int,d regdictionary DEFAULT 'dictionary_right.catalan_saved'::regdictionary,a regdictionary[] DEFAULT ARRAY['dictionary_right.catalan_saved'::regdictionary],dom catalan_dictionary_domain DEFAULT 'dictionary_right.catalan_saved'::regdictionary,pair catalan_dictionary_pair DEFAULT ROW('dictionary_right.catalan_saved'::regdictionary,'dictionary_right.catalan_saved'::catalan_dictionary_domain)::catalan_dictionary_pair)",
			"CREATE VIEW catalan_view AS SELECT d FROM catalan_rows",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO catalan_rows(id) VALUES(1)", 1)
		catalanSaved(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing native stream")
			}
			for _, q := range []string{"DROP VIEW catalan_view", "DROP TABLE catalan_rows", "DROP TYPE catalan_dictionary_pair", "DROP DOMAIN catalan_dictionary_domain", "DROP SCHEMA dictionary_right CASCADE"} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted replay", err)
			}
			catalanSaved(t, p, b, c)
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('catalan_stem','ONLY') AS lexemes", "{only}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			catalanSaved(t, p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic redump", err)
			}
		}
		catalanFailedRestore(t, p, b, c, emitted)
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_right.catalan_saved'::regdictionary::oid::bigint").Scan(&source); err != nil {
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
			name := fmt.Sprintf("pg_temp.catalan_collision_%d", i)
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
			dictionaryResult(t, fresh, fb, fc, "SELECT ts_lexize($1::regdictionary,'GATS') AS lexemes", nil, collision)
		}
		collisionCheck()
		retained, err := fc.QueryContext(fresh.Ctx, "SELECT ts_lexize($1::regdictionary,'GATS') AS lexemes", collision)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		retainedTypes, err := retained.ColumnTypes()
		if err != nil || len(retainedTypes) != 1 || retainedTypes[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal(retainedTypes, err)
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		var target int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'dictionary_right.catalan_saved'::regdictionary::oid::bigint").Scan(&target); err != nil || target == source {
			t.Fatal("import did not relocate", target, source, err)
		}
		collisionCheck()
		catalanSaved(t, fresh, fb, fc)
		catalanFailedRestore(t, fresh, fb, fc, emitted)
		collisionCheck()
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

func catalanFailedRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TABLE catalan_sentinel(v int)", 0)
	regtsExec(t, p, b, c, "INSERT INTO catalan_sentinel VALUES(42)", 1)
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('dictionary_right.catalan_saved','ONLY') AS lexemes")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil || meta[0].DatabaseTypeName() != "_TEXT" {
		t.Fatal(meta, err)
	}
	preserved := func() {
		catalanSaved(t, p, b, c)
		regtsSelect(t, p, b, c, "SELECT v FROM catalan_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed Restore changed full destination", err)
		}
	}
	for _, tc := range []struct{ suffix, code, message string }{
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_saved (stopwords=english,accept=true);", "22023", `unrecognized Snowball parameter: "accept"`},
		{"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_saved (stopwords=catalan);", "F0000", `could not open stop-word file "catalan.stop": No such file or directory`},
		{"SET SESSION AUTHORIZATION dictionary_creator; ALTER TEXT SEARCH DICTIONARY pg_catalog.catalan_stem (stopwords);", "42501", "must be owner of text search dictionary pg_catalog.catalan_stem"},
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
	if err := old.Scan(&value); err != nil || value != "{only}" {
		t.Fatal(value, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
}

func TestReferenceCatalanSnowballCancellation(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY catalan_cancel (TEMPLATE=snowball,Language=catalan)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION catalan_cancel_config (COPY=catalan)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION catalan_cancel_config ALTER MAPPING FOR asciiword WITH catalan_cancel", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('catalan_cancel','GATS') AS lexemes", "{gat}")
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
				for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY catalan_canceled (TEMPLATE=snowball,Language=catalan)", "ALTER TEXT SEARCH DICTIONARY catalan_cancel (stopwords)", "DROP TEXT SEARCH DICTIONARY catalan_cancel"} {
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
				for _, prepared := range []bool{false, true} {
					q := "SELECT ts_lexize('catalan_cancel',$1::text) AS lexemes"
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := c.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						rows, err = stmt.QueryContext(tc.ctx, "GATS")
						stmt.Close()
					} else {
						rows, err = c.QueryContext(tc.ctx, q, "GATS")
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
		if err := c.QueryRowContext(p.Ctx, "SELECT 'catalan_cancel'::regdictionary::oid::bigint").Scan(&identity); err != nil || identity <= 0 {
			t.Fatal("dictionary identity", identity, err)
		}
		var template, configID, configOwner, configNamespace, parser int64
		if err := c.QueryRowContext(p.Ctx, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid='catalan_cancel'::regdictionary").Scan(&template); err != nil {
			t.Fatal(err)
		}
		const configSnapshot = "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid='catalan_cancel_config'::regconfig"
		if err := c.QueryRowContext(p.Ctx, configSnapshot).Scan(&configID, &configOwner, &configNamespace, &parser); err != nil {
			t.Fatal(err)
		}
		preserved := func(t *testing.T, owner *sql.Conn) {
			t.Helper()
			regtsSelect(t, p, b, owner, `SELECT d.oid::bigint,d.dictinitoption,
				d.dictnamespace='public'::regnamespace AS namespace,
				d.dictowner=current_user::regrole::oid AS owned,
				t.tmplname::text,n.nspname::text
				FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate
				JOIN pg_namespace n ON n.oid=t.tmplnamespace
				WHERE d.oid='catalan_cancel'::regdictionary`,
				[]string{"oid", "dictinitoption", "namespace", "owned", "tmplname", "nspname"},
				[]string{"INT8", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT"},
				[][]any{{identity, "language = 'catalan'", true, true, "snowball", "pg_catalog"}})
			regtsSelect(t, p, b, owner, "SELECT dicttemplate::bigint FROM pg_ts_dict WHERE oid='catalan_cancel'::regdictionary", []string{"dicttemplate"}, []string{"INT8"}, [][]any{{template}})
			regtsSelect(t, p, b, owner, configSnapshot, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{configID, configOwner, configNamespace, parser}})
			catalanDictionaryConfigMaps(t, p, b, owner, "catalan_cancel_config", "catalan_cancel")
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('catalan_cancel',$1::text) AS lexemes", "{gat}", "GATS")
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('catalan_cancel',$1::text) AS lexemes", "{only}", "ONLY")
			regtsSelect(t, p, b, owner, "SELECT mapseqno,mapdict='catalan_cancel'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='catalan_cancel_config'::regconfig AND maptokentype=1", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			regtsSelect(t, p, b, owner, "SELECT to_tsvector('catalan_cancel_config','GATS ONLY') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gat':1 'only':2"}})
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
				// The blocked key is selected by actual Snowball output. A
				// parameter prevents constant folding from supplying admission.
				// This witnesses interruption after lexization, not within a
				// particular stemming instruction.
				const query = `SELECT pg_advisory_xact_lock(CASE
					WHEN ts_lexize('catalan_cancel',$1::text)=ARRAY['gat']::text[]
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
						rows, err = stmt.QueryContext(ctx, "GATS")
					} else {
						rows, err = owner.QueryContext(ctx, query, "GATS")
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				// Server cancellation leaves this physical owner usable. The
				// fixture has joined the worker and removed its lock request;
				// close the held statement and return this borrower on exit.
				if after := fixture.OutputGatePID(t, p.Ctx, owner); after != pid {
					t.Fatal("cancellation replaced physical owner", pid, after)
				}
				preserved(t, owner)
			})
			preserved(t, c)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('catalan_cancel','ONLY') AS lexemes", "{only}")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='catalan_canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
