package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceDictionaryOwnership(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE ROLE dictionary_creator", "CREATE ROLE dictionary_peer", "CREATE ROLE dictionary_recipient", "CREATE SCHEMA dictionary_owned", "CREATE SCHEMA dictionary_right", "GRANT USAGE,CREATE ON SCHEMA dictionary_owned,dictionary_right TO dictionary_creator", "SET ROLE dictionary_creator", "CREATE TEXT SEARCH DICTIONARY dictionary_owned.owned (TEMPLATE=pg_catalog.simple,stopwords=english)"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='owned'", []string{"rolname"}, []string{"NAME"}, [][]any{{"dictionary_creator"}})
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_owned.owned'::regdictionary::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
			t.Fatal(oid, err)
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.batch_own (TEMPLATE=simple)", 0)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT USAGE ON SCHEMA dictionary_owned TO dictionary_peer", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.batch_peer (TEMPLATE=simple)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.batch_peer OWNER TO dictionary_peer", 0)
		regtsExec(t, p, b, c, "SET ROLE dictionary_peer", 0)
		for _, tc := range []struct{ q, name string }{
			{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned (accept=false)", "dictionary_owned.owned"},
			{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned RENAME TO denied", "owned"},
			{"ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned OWNER TO dictionary_recipient", "owned"},
			{"DROP TEXT SEARCH DICTIONARY dictionary_owned.owned", "dictionary_owned.owned"},
		} {
			dictionaryError(t, p, b, c, tc.q, "42501", "must be owner of text search dictionary "+tc.name)
		}
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned SET SCHEMA dictionary_right", "42501", "permission denied for schema dictionary_right")
		regtsSelect(t, p, b, c, "SELECT 'dictionary_owned.owned'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_owned.denied (TEMPLATE=simple)", "42501", "permission denied for schema dictionary_owned")
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT CREATE ON SCHEMA dictionary_right TO dictionary_peer", 0)
		regtsExec(t, p, b, c, "SET ROLE dictionary_peer", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned SET SCHEMA dictionary_right", "42501", "must be owner of text search dictionary owned")
		regtsSelect(t, p, b, c, "SELECT 'dictionary_owned.owned'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "SET ROLE dictionary_creator", 0)
		dictionaryError(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_owned.batch_own,dictionary_owned.batch_peer", "42501", "must be owner of text search dictionary dictionary_owned.batch_peer")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_owned.batch_own','WORD') AS lexemes", "{word}")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.owned RENAME TO renamed", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_right.renamed (TEMPLATE=simple)", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.renamed SET SCHEMA dictionary_right", "42710", `text search dictionary "renamed" already exists in schema "dictionary_right"`)
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_right.renamed", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_owned.renamed SET SCHEMA dictionary_right", 0)
		regtsSelect(t, p, b, c, "SELECT 'dictionary_right.renamed'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.renamed OWNER TO dictionary_recipient", "42501", `must be able to SET ROLE "dictionary_recipient"`)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT dictionary_recipient TO dictionary_creator", 0)
		regtsExec(t, p, b, c, "SET ROLE dictionary_creator", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.renamed OWNER TO dictionary_recipient", "42501", "permission denied for schema dictionary_right")
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsExec(t, p, b, c, "GRANT CREATE ON SCHEMA dictionary_right TO dictionary_recipient", 0)
		regtsExec(t, p, b, c, "SET ROLE dictionary_creator", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.renamed OWNER TO dictionary_recipient", 0)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		regtsSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='renamed'", []string{"rolname"}, []string{"NAME"}, [][]any{{"dictionary_recipient"}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("DROP ROLE dictionary_recipient", "2BP01"))
		regtsExec(t, p, b, c, "REASSIGN OWNED BY dictionary_recipient TO dictionary_creator", 0)
		regtsSelect(t, p, b, c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='renamed'", []string{"rolname"}, []string{"NAME"}, [][]any{{"dictionary_creator"}})
		regtsExec(t, p, b, c, "DROP OWNED BY dictionary_creator CASCADE", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='renamed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceDictionaryDependencies(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("DROP TEXT SEARCH DICTIONARY IF EXISTS dictionary_absent_notice", 0))
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `text search dictionary "dictionary_absent_notice" does not exist, skipping`}
		if len(notices) != 1 || notices[0] != wantNotice {
			t.Fatal("dictionary IF EXISTS notice", notices, wantNotice)
		}
		regtsExec(t, p, b, c, "CREATE SCHEMA dictionary_left", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_left.dependent (TEMPLATE=simple,stopwords=english)", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid='dictionary_left.dependent'::regdictionary::oid AND refclassid=2615 AND refobjid='dictionary_left'::regnamespace::oid AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("DROP SCHEMA dictionary_left RESTRICT", "2BP01"))
		regtsExec(t, p, b, c, "CREATE VIEW dictionary_view AS SELECT 'dictionary_left.dependent'::regdictionary AS d", 0)
		regtsExec(t, p, b, c, "CREATE TABLE dictionary_defaults(id integer,d regdictionary DEFAULT 'dictionary_left.dependent'::regdictionary)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_second (TEMPLATE=simple)", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("DROP TEXT SEARCH DICTIONARY dictionary_second,dictionary_left.dependent RESTRICT", "2BP01"))
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_second','WORD') AS lexemes", "{word}")
		dictionaryError(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_second,dictionary_absent", "42704", `text search dictionary "dictionary_absent" does not exist`)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_second','WORD') AS lexemes", "{word}")
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_left.dependent CASCADE", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_class WHERE relname='dictionary_view'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		regtsExec(t, p, b, c, "INSERT INTO dictionary_defaults(id) VALUES(1)", 1)
		regtsSelect(t, p, b, c, "SELECT d FROM dictionary_defaults", []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{nil}})
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_left.again (TEMPLATE=simple)", 0)
		regtsExec(t, p, b, c, "DROP SCHEMA dictionary_left CASCADE", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='again'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_unaccent (TEMPLATE=public.unaccent,RULES=unaccent)", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_unaccent','Hôtel') AS lexemes", "{Hotel}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_unaccent','plain') AS lexemes", nil)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("DROP EXTENSION unaccent RESTRICT", "2BP01"))
		regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='dictionary_unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceDictionaryPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_held (TEMPLATE=simple,stopwords=english)", 0)
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		dictionaryResult(t, p, b, held, q, "{yes}", "dictionary_held", "YeS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_held (accept=false)", 0)
		dictionaryResult(t, p, b, held, q, nil, "dictionary_held", "YeS")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_held (accept=true,bad=1)", "22023", `unrecognized simple dictionary parameter: "bad"`)
		dictionaryResult(t, p, b, held, q, nil, "dictionary_held", "YeS")
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_held RENAME TO dictionary_renamed", 0)
		dictionaryResult(t, p, b, held, q, "{}", "dictionary_renamed", "The")
		regtsExec(t, p, b, c, "CREATE SCHEMA dictionary_right", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_renamed SET SCHEMA dictionary_right", 0)
		dictionaryResult(t, p, b, held, q, "{}", "dictionary_right.dictionary_renamed", "The")
		regtsExec(t, p, b, c, "CREATE TABLE dictionary_unrelated(v int)", 0)
		dictionaryResult(t, p, b, held, q, nil, "dictionary_right.dictionary_renamed", "YeS")
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_right.dictionary_renamed", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_right.dictionary_renamed (TEMPLATE=simple)", 0)
		dictionaryResult(t, p, b, held, q, "{the}", "dictionary_right.dictionary_renamed", "The")
	})
}

func TestReferenceDictionaryAtomicity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TABLE dictionary_sentinel(v int)", 0)
		regtsExec(t, p, b, c, "INSERT INTO dictionary_sentinel VALUES(42)", 1)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_atomic (TEMPLATE=simple,stopwords=english)", 0)
		for _, q := range []string{"BEGIN", "ALTER TEXT SEARCH DICTIONARY dictionary_atomic (accept=false)", "SAVEPOINT dictionary_save", "ALTER TEXT SEARCH DICTIONARY dictionary_atomic (accept)", "ROLLBACK TO dictionary_save"} {
			regtsExec(t, p, b, c, q, 0)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_atomic','YeS') AS lexemes", nil)
		regtsExec(t, p, b, c, "ROLLBACK", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_atomic','YeS') AS lexemes", "{yes}")
		for _, q := range []string{"BEGIN", "CREATE TEXT SEARCH DICTIONARY dictionary_rolled (TEMPLATE=simple)", "ALTER TEXT SEARCH DICTIONARY dictionary_atomic RENAME TO dictionary_temporal", "ROLLBACK"} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='dictionary_rolled' OR dictname='dictionary_temporal'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		for _, q := range []string{"BEGIN", "ALTER TEXT SEARCH DICTIONARY dictionary_atomic (accept=false)", "COMMIT"} {
			regtsExec(t, p, b, c, q, 0)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_atomic','YeS') AS lexemes", nil)
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY dictionary_atomic (accept)", "DROP TEXT SEARCH DICTIONARY dictionary_atomic", "CREATE TEXT SEARCH DICTIONARY dictionary_canceled (TEMPLATE=simple)"} {
				_, err := c.ExecContext(ctx, q)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(q, err)
				}
			}
			expired, cancel := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer cancel()
			_, err := c.ExecContext(expired, "ALTER TEXT SEARCH DICTIONARY dictionary_atomic (accept)")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("expired dictionary update", err)
			}
		}
		regtsSelect(t, p, b, c, "SELECT v FROM dictionary_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_atomic','YeS') AS lexemes", nil)
	})
}

func TestReferenceDictionaryDiagnostics(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		multibyte := dictionaryDiagnostic("42883", "function ts_lexize(boolean, unknown) does not exist")
		multibyte.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		multibyte.Position = "23"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 'é' AS prefix, ts_lexize(true,'word')", multibyte))
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE TEXT SEARCH DICTIONARY dictionary_bad (accept=true)", "42P17", "text search template is required"},
			{"CREATE TEXT SEARCH DICTIONARY dictionary_bad (TEMPLATE=dictionary_missing)", "42704", `text search template "dictionary_missing" does not exist`},
			{"CREATE TEXT SEARCH DICTIONARY dictionary_bad (TEMPLATE='pg_catalog.simple')", "42704", `text search template "pg_catalog.simple" does not exist`},
			{"CREATE TEXT SEARCH DICTIONARY dictionary_bad (TEMPLATE=simple,STOPWORDS='ENGLISH')", "22023", `invalid text search configuration file name "ENGLISH"`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, dictionaryDiagnostic(tc.code, tc.message))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_collision (TEMPLATE=simple)", 0)
		var ns int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public'::regnamespace::oid::bigint").Scan(&ns); err != nil {
			t.Fatal(err)
		}
		duplicate := dictionaryDiagnostic("23505", `duplicate key value violates unique constraint "pg_ts_dict_dictname_index"`)
		duplicate.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(dictionary_collision, %d) already exists.", ns)
		duplicate.Schema = "pg_catalog"
		duplicate.Table = "pg_ts_dict"
		duplicate.Constraint = "pg_ts_dict_dictname_index"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY dictionary_collision (TEMPLATE=simple)", duplicate))
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_other (TEMPLATE=simple)", 0)
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_other RENAME TO dictionary_collision", "42710", `text search dictionary "dictionary_collision" already exists in schema "public"`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("CREATE TEXT SEARCH DICTIONARY dictionary_prefix (TEMPLATE=simple) invalid_suffix", "42601"))
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='dictionary_bad' OR dictname='dictionary_prefix'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		q := "ALTER TEXT SEARCH DICTIONARY dictionary_collision (stopwords=missing_dictionary)"
		native := dictionaryDiagnostic("F0000", `could not open stop-word file "missing_dictionary.stop": No such file or directory`)
		s := fixture.ErrorDiagnostic(q, native)
		pg := dictionaryDiagnostic("F0000", `could not open stop-word file "/usr/share/postgresql/18/tsearch_data/missing_dictionary.stop": No such file or directory`)
		s.PostgresDiagnostic = &pg
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_collision','WORD') AS lexemes", "{word}")
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		defer regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		dictionaryRejectMissingRules(t, p, b, c, "public.unaccent")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_missing_rules (TEMPLATE=public.unaccent)", "22023", "missing Rules parameter")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='dictionary_missing_rules'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
