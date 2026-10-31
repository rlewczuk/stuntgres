package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceYiddishSnowballCancellation(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY yiddish_cancel (TEMPLATE=snowball,Language=yiddish,StopWords=english)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION yiddish_cancel_config (COPY=yiddish)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION yiddish_cancel_config ALTER MAPPING FOR asciiword,word WITH yiddish_cancel", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_cancel','קינדער') AS lexemes", "{קינד}")
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "yiddish_cancel", "yiddish_cancel_config")
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, stop := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer stop()
			for _, tc := range []struct { ctx context.Context; want error }{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY yiddish_canceled (TEMPLATE=snowball,Language=yiddish)", "ALTER TEXT SEARCH DICTIONARY yiddish_cancel (stopwords)", "DROP TEXT SEARCH DICTIONARY yiddish_cancel"} {
					for _, prepared := range []bool{false, true} {
						var result sql.Result
						var err error
						if prepared {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil { t.Fatal(e) }
							result, err = stmt.ExecContext(tc.ctx)
							stmt.Close()
						} else { result, err = c.ExecContext(tc.ctx, q) }
						if result != nil || !errors.Is(err, tc.want) { t.Fatal("canceled DDL published", q, result, err) }
					}
				}
				for _, prepared := range []bool{false, true} {
					const q = "SELECT ts_lexize('yiddish_cancel',$1::text) AS lexemes"
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := c.PrepareContext(p.Ctx, q)
						if e != nil { t.Fatal(e) }
						rows, err = stmt.QueryContext(tc.ctx, "קינדער")
						stmt.Close()
					} else { rows, err = c.QueryContext(tc.ctx, q, "קינדער") }
					if rows != nil { rows.Close(); t.Fatal("canceled query returned Rows") }
					if !errors.Is(err, tc.want) { t.Fatal(err) }
				}
				preserved()
			}
		}
		var identity, template, config, owner, namespace, parser int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE oid='yiddish_cancel'::regdictionary").Scan(&identity, &template); err != nil || identity <= 0 { t.Fatal(identity, err) }
		const configSnapshot = "SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid='yiddish_cancel_config'::regconfig"
		if err := c.QueryRowContext(p.Ctx, configSnapshot).Scan(&config, &owner, &namespace, &parser); err != nil { t.Fatal(err) }
		var committed bytes.Buffer
		if !b.PG { if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil { t.Fatal(err) } }
		check := func(t *testing.T, session *sql.Conn) {
			t.Helper()
			preserved()
			regtsSelect(t, p, b, session, "SELECT d.oid::bigint,d.dicttemplate::bigint,d.dictinitoption,d.dictnamespace='public'::regnamespace AS namespace,d.dictowner=current_user::regrole::oid AS owned,t.tmplname::text,n.nspname::text FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_namespace n ON n.oid=t.tmplnamespace WHERE d.oid='yiddish_cancel'::regdictionary", []string{"oid", "dicttemplate", "dictinitoption", "namespace", "owned", "tmplname", "nspname"}, []string{"INT8", "INT8", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{identity, template, "language = 'yiddish', stopwords = 'english'", true, true, "snowball", "pg_catalog"}})
			regtsSelect(t, p, b, session, configSnapshot, []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{config, owner, namespace, parser}})
			for _, pair := range [][2]string{{"קינדער", "{קינד}"}, {"ONLY", "{}"}, {"RATS", "{rats}"}} { dictionaryResult(t, p, b, session, "SELECT ts_lexize('yiddish_cancel',$1::text) AS lexemes", pair[1], pair[0]) }
			regtsSelect(t, p, b, session, "SELECT mapseqno,mapdict='yiddish_cancel'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='yiddish_cancel_config'::regconfig AND maptokentype IN (1,2) ORDER BY maptokentype", []string{"mapseqno", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(1), true}})
			regtsSelect(t, p, b, session, "SELECT to_tsvector('yiddish_cancel_config','ONLY קינדער RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':3 'קינד':2"}})
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(committed.Bytes(), after.Bytes()) { t.Fatal("admitted cancellation changed whole state", err) }
			}
		}
		check(t, c)
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("admitted/prepared=%t", prepared), func(t *testing.T) {
				session, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer session.Close()
				pid := fixture.OutputGatePID(t, p.Ctx, session)
				// Admission depends on genuine parameterized Yiddish output. The
				// fixture observes holder/waiter/blocker, cancels and joins.
				const query = `SELECT pg_advisory_xact_lock(CASE
					WHEN ts_lexize('yiddish_cancel',$1::text)=ARRAY['קינד']::text[]
					THEN 67108864::bigint ELSE 2::bigint END)`
				var stmt *sql.Stmt
				if prepared {
					stmt, err = session.PrepareContext(p.Ctx, query)
					if err != nil { t.Fatal(err) }
					defer stmt.Close()
				}
				fixture.OutputGateCancel(t, p, b, session, false, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared { rows, err = stmt.QueryContext(ctx, "קינדער") } else { rows, err = session.QueryContext(ctx, query, "קינדער") }
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if after := fixture.OutputGatePID(t, p.Ctx, session); after != pid { t.Fatal("physical owner replaced", pid, after) }
				check(t, session)
			})
			check(t, c)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='yiddish_canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
