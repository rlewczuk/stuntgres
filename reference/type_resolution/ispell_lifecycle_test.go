package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIspellPrepared(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_prepared", "legacy", "")
		constantSQL := "SELECT ts_lexize('ispell_prepared'::regdictionary,'unbook'::text) AS lexemes"
		constantStmt, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constantStmt.Close()
		constant := fixture.HeldStatement{Executor: c, Statement: constantStmt, SQL: constantSQL}
		dictionaryResult(t, p, b, constant, constantSQL, "{book}")
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		ispellToken(t, p, b, held, "ispell_prepared", "unbook", "{book}")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("ALTER TEXT SEARCH DICTIONARY ispell_prepared (StopWords=tr_ispell_book)", 0)))
		dictionaryResult(t, p, b, constant, constantSQL, "{book}")
		dictionaryResult(t, p, b, c, constantSQL, "{}")
		fresh, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
		fresh.Close()
		ispellToken(t, p, b, held, "ispell_prepared", "unbook", "{}")
		ispellToken(t, p, b, held, "ispell_prepared", "booking", "{booking}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_prepared (StopWords='',AffFile)", "22023", "missing AffFile parameter")
		ispellToken(t, p, b, held, "ispell_prepared", "unbook", "{}")
		dictionaryResult(t, p, b, constant, constantSQL, "{book}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_prepared RENAME TO ispell_renamed", 0)
		ispellToken(t, p, b, held, "ispell_renamed", "booking", "{booking}")
		regtsExec(t, p, b, c, "CREATE SCHEMA ispell_right", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_renamed SET SCHEMA ispell_right", 0)
		ispellToken(t, p, b, held, "ispell_right.ispell_renamed", "unbook", "{}")
		regtsExec(t, p, b, c, "CREATE TABLE ispell_unrelated(v int)", 0)
		ispellToken(t, p, b, held, "ispell_right.ispell_renamed", "booking", "{booking}")
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY ispell_right.ispell_renamed", 0)
		ispellCreate(t, p, b, c, "ispell_right.ispell_renamed", "legacy", "")
		ispellToken(t, p, b, held, "ispell_right.ispell_renamed", "unbook", "{book}")
	})
}

func TestReferenceIspellOwnership(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE ispell_creator", "CREATE ROLE ispell_peer", "CREATE ROLE ispell_recipient",
			"CREATE SCHEMA ispell_owned", "CREATE SCHEMA ispell_right",
			"GRANT USAGE,CREATE ON SCHEMA ispell_owned,ispell_right TO ispell_creator",
			"SET ROLE ispell_creator",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		ispellCreate(t, p, b, c, "ispell_owned.owned", "legacy", "")
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ispell_owned.owned'::regdictionary::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
			t.Fatal(oid, err)
		}
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT USAGE ON SCHEMA ispell_owned TO ispell_peer", 0)
		regtsExec(t, p, b, c, "SET ROLE ispell_peer", 0)
		for _, tc := range []struct{ q, name string }{
			{"ALTER TEXT SEARCH DICTIONARY ispell_owned.owned (StopWords=tr_ispell_book)", "ispell_owned.owned"},
			{"ALTER TEXT SEARCH DICTIONARY ispell_owned.owned RENAME TO denied", "owned"},
			{"ALTER TEXT SEARCH DICTIONARY ispell_owned.owned OWNER TO ispell_recipient", "owned"},
			{"DROP TEXT SEARCH DICTIONARY ispell_owned.owned", "ispell_owned.owned"},
		} {
			dictionaryError(t, p, b, c, tc.q, "42501", "must be owner of text search dictionary "+tc.name)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY ispell_owned.denied (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", "42501", "permission denied for schema ispell_owned")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_owned.owned SET SCHEMA ispell_right", "42501", "permission denied for schema ispell_right")
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "SET ROLE ispell_creator", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_owned.owned RENAME TO renamed", 0)
		ispellCreate(t, p, b, c, "ispell_right.renamed", "legacy", "")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_owned.renamed SET SCHEMA ispell_right", "42710", `text search dictionary "renamed" already exists in schema "ispell_right"`)
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY ispell_right.renamed", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_owned.renamed SET SCHEMA ispell_right", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_right.renamed OWNER TO ispell_recipient", "42501", `must be able to SET ROLE "ispell_recipient"`)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT ispell_recipient TO ispell_creator", 0)
		regtsExec(t, p, b, c, "GRANT CREATE ON SCHEMA ispell_right TO ispell_recipient", 0)
		regtsExec(t, p, b, c, "SET ROLE ispell_creator", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_right.renamed OWNER TO ispell_recipient", 0)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsSelect(t, p, b, c, "SELECT d.oid::bigint AS id,r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='renamed'", []string{"id", "rolname"}, []string{"INT8", "NAME"}, [][]any{{oid, "ispell_recipient"}})
		ispellToken(t, p, b, c, "ispell_right.renamed", "rebookings", "{booking,book}")
		regtsExec(t, p, b, c, "CREATE TABLE ispell_owner_rows(d regdictionary DEFAULT 'ispell_right.renamed'::regdictionary)", 0)
		regtsExec(t, p, b, c, "CREATE VIEW ispell_owner_view AS SELECT 'ispell_right.renamed'::regdictionary AS d", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("DROP TEXT SEARCH DICTIONARY ispell_right.renamed RESTRICT", "2BP01"))
		ispellToken(t, p, b, c, "ispell_right.renamed", "skies", "{sky}")
		regtsExec(t, p, b, c, "REASSIGN OWNED BY ispell_recipient TO ispell_creator", 0)
		regtsExec(t, p, b, c, "DROP OWNED BY ispell_creator CASCADE", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='renamed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceIspellAtomicity(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_atomic", "legacy", "")
		regtsExec(t, p, b, c, "CREATE TABLE ispell_sentinel(v int)", 0)
		regtsExec(t, p, b, c, "INSERT INTO ispell_sentinel VALUES(42)", 1)
		for _, q := range []string{"BEGIN", "ALTER TEXT SEARCH DICTIONARY ispell_atomic (StopWords=tr_ispell_book)", "SAVEPOINT ispell_save", "ALTER TEXT SEARCH DICTIONARY ispell_atomic (StopWords)", "ROLLBACK TO ispell_save"} {
			regtsExec(t, p, b, c, q, 0)
		}
		ispellToken(t, p, b, c, "ispell_atomic", "unbook", "{}")
		regtsExec(t, p, b, c, "ROLLBACK", 0)
		ispellToken(t, p, b, c, "ispell_atomic", "unbook", "{book}")
		for _, q := range []string{"BEGIN", "CREATE TEXT SEARCH DICTIONARY ispell_rolled (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", "ALTER TEXT SEARCH DICTIONARY ispell_atomic RENAME TO ispell_temporal", "ROLLBACK"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('ispell_rolled','ispell_temporal')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_atomic (AffFile)", "22023", "missing AffFile parameter")
		ispellToken(t, p, b, c, "ispell_atomic", "rebookings", "{booking,book}")
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, stop := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer stop()
			for _, state := range []struct {
				ctx context.Context
				err error
			}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY ispell_canceled (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", "ALTER TEXT SEARCH DICTIONARY ispell_atomic (StopWords=tr_ispell_book)"} {
					_, err := c.ExecContext(state.ctx, q)
					if !errors.Is(err, state.err) {
						t.Fatal(q, err)
					}
				}
			}
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell_canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		}
		regtsSelect(t, p, b, c, "SELECT v FROM ispell_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		ispellToken(t, p, b, c, "ispell_atomic", "unbook", "{book}")
	})
}

func TestReferenceIspellFeatureSmoke(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "public.ispell_smoke", "legacy", "")
		var smokeID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.ispell_smoke'::regdictionary::oid::bigint").Scan(&smokeID); err != nil || smokeID <= 0 {
			t.Fatal(smokeID, err)
		}
		regtsExec(t, p, b, c, "CREATE ROLE ispell_creator", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.ispell_smoke OWNER TO ispell_creator", 0)
		constantSQL := "SELECT ts_lexize('public.ispell_smoke'::regdictionary,'unbook'::text) AS lexemes"
		constantStmt, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer constantStmt.Close()
		constant := fixture.HeldStatement{Executor: c, Statement: constantStmt, SQL: constantSQL}
		dictionaryResult(t, p, b, constant, constantSQL, "{book}")
		for _, tc := range append(append([]struct {
			token string
			want  any
		}{}, ispellMorphology...), ispellCompound...) {
			ispellToken(t, p, b, c, "public.ispell_smoke", tc.token, tc.want)
		}
		q := "SELECT ts_lexize('public.ispell_smoke'::regdictionary,$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{book}", "unbook")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.ispell_smoke (StopWords=tr_ispell_book)", 0)
		dictionaryResult(t, p, b, constant, constantSQL, "{book}")
		dictionaryResult(t, p, b, c, constantSQL, "{}")
		dictionaryResult(t, p, b, held, q, "{}", "unbook")
		dictionaryResult(t, p, b, held, q, "{booking}", "booking")
		dictionaryResult(t, p, b, held, q, nil, "zzzzunknown")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.ispell_smoke (AffFile)", "22023", "missing AffFile parameter")
		dictionaryResult(t, p, b, held, q, "{}", "unbook")
		for _, prepared := range []bool{false, true} {
			d := dictionaryDiagnostic("F0000", "syntax error")
			d.Context = ispellReadContext(b, "tr_ispell_syntax.affix", 3, ". > 1\n")
			s := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY public.ispell_smoke (AffFile=tr_ispell_syntax)", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		dictionaryResult(t, p, b, held, q, "{booking}", "booking")
		regtsSelect(t, p, b, c, "SELECT dicttemplate::bigint AS template,dictinitoption FROM pg_ts_dict WHERE dictname='ispell_smoke'", []string{"template", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{int64(3733), "dictfile = 'tr_ispell_legacy', afffile = 'tr_ispell_legacy', stopwords = 'tr_ispell_book'"}})
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id FROM pg_ts_dict WHERE dictname='ispell_smoke'", []string{"id"}, []string{"INT8"}, [][]any{{smokeID}})
		regtsSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE dictname='ispell_smoke'", []string{"rolname"}, []string{"NAME"}, [][]any{{"ispell_creator"}})
		regtsSelect(t, p, b, c, "SELECT tmplinit::oid::bigint AS init,tmpllexize::oid::bigint AS lexize FROM pg_ts_template WHERE oid=3733", []string{"init", "lexize"}, []string{"INT8", "INT8"}, [][]any{{int64(3731), int64(3732)}})
		stmt.Close()
		constantStmt.Close()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(dump.Bytes(), []byte("book/GJUS")) {
				t.Fatal("resource contents serialized")
			}
			old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('public.ispell_smoke','booking') AS lexemes")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err := old.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
				t.Fatal(types, err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var value any
			if err := old.Scan(&value); err != nil || value != "{booking}" {
				t.Fatal(value, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			old.Close()
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('public.ispell_smoke','booking') AS lexemes", "{booking}")
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("smoke redump changed")
			}
		}
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY public.ispell_smoke", 0)
	})
}
