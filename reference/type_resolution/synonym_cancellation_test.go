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

func TestReferenceSynonymCancellation(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_cancel")
		synonymConfig(t, p, b, c, "synonym_cancel_config", "synonym_cancel")
		before := synonymSnapshot(t, p, c, "synonym_cancel")
		if before.options != "synonyms = 'synonym_sample'" {
			t.Fatal(before)
		}
		preserved := func(t *testing.T, owner *sql.Conn) {
			t.Helper()
			synonymUnchanged(t, p, owner, "synonym_cancel", before)
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('synonym_cancel',$1::text) AS lexemes", "{pgsql}", "POSTGRES")
			dictionaryResult(t, p, b, owner, "SELECT ts_lexize('synonym_cancel',$1::text) AS lexemes", "{index}", "indices")
			synonymConverted(t, p, b, owner, "SELECT to_tsquery('synonym_cancel_config',$1::text) AS normalized", "TSQUERY", "'index':*", "indices")
		}
		preserved(t, c)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
				for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY synonym_canceled (TEMPLATE=synonym,Synonyms=synonym_sample)", "ALTER TEXT SEARCH DICTIONARY synonym_cancel (casesensitive=true)", "DROP TEXT SEARCH DICTIONARY synonym_cancel CASCADE"} {
					if _, err := c.ExecContext(tc.ctx, q); !errors.Is(err, tc.want) {
						t.Fatal(q, err)
					}
				}
				for _, q := range []string{"SELECT ts_lexize('synonym_cancel',$1::text) AS lexemes", "SELECT to_tsquery('synonym_cancel_config',$1::text) AS normalized", "SELECT to_tsvector('synonym_cancel_config',$1::text) AS normalized"} {
					for _, prepared := range []bool{false, true} {
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							rows, err = stmt.QueryContext(tc.ctx, "indices")
							stmt.Close()
						} else {
							rows, err = c.QueryContext(tc.ctx, q, "indices")
						}
						if rows != nil {
							rows.Close()
							t.Fatal("canceled native synonym query returned Rows")
						}
						if !errors.Is(err, tc.want) {
							t.Fatal(q, err)
						}
					}
				}
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatal("native synonym cancellation published", err)
			}
			preserved(t, c)
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("admitted/prepared=%t", prepared), func(t *testing.T) {
				owner, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				pid := fixture.OutputGatePID(t, p.Ctx, owner)
				// Actual runtime-param synonym output chooses the blocked key.
				// This proves admitted interruption after lexization, not inside
				// the definition parser (covered privately in production round).
				const query = `SELECT pg_advisory_xact_lock(CASE
					WHEN ts_lexize('synonym_cancel',$1::text)=ARRAY['pgsql']::text[]
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
						rows, err = stmt.QueryContext(ctx, "POSTGRES")
					} else {
						rows, err = owner.QueryContext(ctx, query, "POSTGRES")
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if after := fixture.OutputGatePID(t, p.Ctx, owner); after != pid {
					t.Fatal("synonym cancellation replaced physical owner", pid, after)
				}
				preserved(t, owner)
			})
			preserved(t, c)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='synonym_canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
