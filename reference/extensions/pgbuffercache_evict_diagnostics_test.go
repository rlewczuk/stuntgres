package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func bceMissing(query, signature string) fixture.Step {
	s := bcError(query, "42883", "function "+signature+" does not exist")
	s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	s.Diagnostic.Position = fixture.Position(strings.Index(query, "bc_part.") + 1)
	return s
}

func TestReferencePgBuffercacheEvictionDiagnostics(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		for _, id := range []int64{-2147483648, -1, 0, capacity + 1, 2147483647} {
			t.Run(fmt.Sprintf("bad_buffer_%d", id), func(t *testing.T) {
				q := "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)"
				s := bcError(q, "XX000", fmt.Sprintf("bad buffer ID: %d", id))
				s.Args = []any{id}
				bcFailure(t, p, b, c, s, "execute")
				bcRun(t, p, b, bceHeld(t, p, c, q), s)
				literal := fmt.Sprintf("SELECT * FROM bc_part.pg_buffercache_evict(%d)", id)
				bcFailure(t, p, b, c, bcError(literal, "XX000", fmt.Sprintf("bad buffer ID: %d", id)), "execute")
			})
		}
		for _, id := range []int64{1, capacity} {
			t.Run(fmt.Sprintf("valid_endpoint_%d", id), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					got := bceCall(t, p, b, c, prepared, true, "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", id)
					if _, ok := got[0].(bool); !ok {
						t.Fatal("valid endpoint result", got)
					}
					if !b.PG && got[1] != false {
						t.Fatal("native clean cache flushed", got)
					}
				}
			})
		}
		bcDDL(t, p, b, c, "CREATE TEMP TABLE evict_local(k integer)", "CREATE VIEW bc_part.cast_target AS SELECT 1 AS k")
		defer c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS evict_local")
		for _, tc := range []struct{ name, query, code, message, stage string }{
			{"local_relation", "SELECT * FROM bc_part.pg_buffercache_evict_relation('evict_local')", "22023", "relation uses local buffers, pg_buffercache_evict_relation() is intended to be used for shared buffers only", "execute"},
			{"zero_oid", "SELECT * FROM bc_part.pg_buffercache_evict_relation(0::oid::regclass)", "XX000", "could not open relation with OID 0", "execute"},
			{"missing_numeric", "SELECT * FROM bc_part.pg_buffercache_evict_relation(4294967295::oid::regclass)", "XX000", "could not open relation with OID 4294967295", "execute"},
			{"missing_named", "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.no_such_heap')", "42P01", "relation \"bc_part.no_such_heap\" does not exist", "prepare"},
			{"int4_overflow", "SELECT * FROM bc_part.pg_buffercache_evict(2147483648::bigint::integer)", "22003", "integer out of range", "execute"},
			{"bad_integer", "SELECT * FROM bc_part.pg_buffercache_evict('not_an_integer')", "22P02", "invalid input syntax for type integer: \"not_an_integer\"", "prepare"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := bcError(tc.query, tc.code, tc.message)
				if tc.name == "missing_named" || tc.name == "bad_integer" {
					s.Diagnostic.Position = fixture.Position(strings.Index(tc.query, "'") + 1)
				}
				// Positions/stages are release-source-informed oracle checkpoints,
				// not native-derived goldens; root pins query-specific fields.
				bcFailure(t, p, b, c, s, tc.stage)
			})
		}
		for _, tc := range []struct{ call, signature string }{
			{"pg_buffercache_evict()", "pg_buffercache_evict()"},
			{"pg_buffercache_evict(*)", "pg_buffercache_evict()"},
			{"pg_buffercache_evict(1,2)", "pg_buffercache_evict(integer, integer)"},
			{"pg_buffercache_evict(1::bigint)", "pg_buffercache_evict(bigint)"},
			{"pg_buffercache_evict('1'::text)", "pg_buffercache_evict(text)"},
			{"pg_buffercache_evict(true)", "pg_buffercache_evict(boolean)"},
			{"pg_buffercache_evict(bufferid=>1)", "pg_buffercache_evict(bufferid => integer)"},
			{"pg_buffercache_evict_relation()", "pg_buffercache_evict_relation()"},
			{"pg_buffercache_evict_relation('bc_part.cast_target',1)", "pg_buffercache_evict_relation(unknown, integer)"},
			{"pg_buffercache_evict_relation(rel=>NULL::regclass)", "pg_buffercache_evict_relation(rel => regclass)"},
			{"pg_buffercache_evict_all(1)", "pg_buffercache_evict_all(integer)"},
			{"pg_buffercache_evict_all(NULL)", "pg_buffercache_evict_all(unknown)"},
			{"pg_buffercache_evict(VARIADIC ARRAY[1])", "pg_buffercache_evict(integer[])"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "SELECT * FROM bc_part." + tc.call
				bcFailure(t, p, b, c, bceMissing(q, "bc_part."+tc.signature), "prepare")
			})
		}
		for _, tc := range []struct{ call, code, message string }{
			{"pg_buffercache_evict(DISTINCT 1)", "42809", "DISTINCT specified, but bc_part.pg_buffercache_evict is not an aggregate function"},
			{"pg_buffercache_evict(1 ORDER BY 1)", "42809", "ORDER BY specified, but bc_part.pg_buffercache_evict is not an aggregate function"},
			{"pg_buffercache_evict(1) FILTER (WHERE true)", "42601", "syntax error at or near \"WHERE\""},
			{"pg_buffercache_evict_all(*)", "42809", "bc_part.pg_buffercache_evict_all(*) specified, but bc_part.pg_buffercache_evict_all is not an aggregate function"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "SELECT * FROM bc_part." + tc.call
				s := bcError(q, tc.code, tc.message)
				s.Diagnostic.Position = fixture.Position(15)
				if tc.code == "42601" {
					s.Diagnostic.Position = fixture.Position(55)
				}
				bcFailure(t, p, b, c, s, "prepare")
			})
		}
		t.Run("held_runtime_coercion", func(t *testing.T) {
			for _, tc := range []struct{ query, value, code, message string }{
				{"SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", "2147483648", "22003", "value \"2147483648\" is out of range for type integer"},
				{"SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", "not_an_integer", "22P02", "invalid input syntax for type integer: \"not_an_integer\""},
				{"SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.no_such_heap", "42P01", "relation \"bc_part.no_such_heap\" does not exist"},
				{"SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "0", "XX000", "could not open relation with OID 0"},
			} {
				t.Run(tc.value, func(t *testing.T) {
					s := bcError(tc.query, tc.code, tc.message)
					s.Args = []any{tc.value}
					if tc.value != "0" {
						s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					}
					bcFailure(t, p, b, c, s, "execute")
					bcRun(t, p, b, bceHeld(t, p, c, tc.query), s)
				})
			}
		})
		t.Run("explicit_coercions", func(t *testing.T) {
			bcBoth(t, p, b, c, bceNullStep("SELECT * FROM bc_part.pg_buffercache_evict(NULL::bigint::integer)", true))
			bcBoth(t, p, b, c, fixture.Select("SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.cast_target'::text::regclass)", bceCountNames, bceCountTypes, [][]any{{int64(0), int64(0), int64(0)}}))
			q := "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.cast_target'::text)"
			s := fixture.Select(q, bceCountNames, bceCountTypes, [][]any{{int64(0), int64(0), int64(0)}})
			bcBoth(t, p, b, c, s)
			bcRun(t, p, b, bceHeld(t, p, c, q), s)
		})
	})
}
