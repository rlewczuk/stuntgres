package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClusterDiagnostics(t *testing.T) {
	t.Run("foreign_temp", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			for _, q := range []string{"CREATE TEMP TABLE cluster_foreign(id int)", "CREATE INDEX foreign_idx ON cluster_foreign(id)"} {
				persistenceExec(t, p, b, holder, q)
			}
			persistenceRun(t, p, b, holder, fixture.Exec("INSERT INTO cluster_foreign VALUES(2),(1)", 2))
			var schema string
			if err = holder.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='cluster_foreign'::regclass").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			query := `CLUSTER "` + strings.ReplaceAll(schema, `"`, `""`) + `".cluster_foreign USING foreign_idx`
			want := persistenceDiagnostic("0A000", "cannot cluster temporary tables of other sessions")
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic(query, want))
			held := clusterCommandHeld(t, p, b, c, query)
			_, err = held.ExecContext(p.Ctx)
			fixture.AssertDiagnostic(t, err, &want)
			persistenceRun(t, p, b, holder, fixture.Select("SELECT id FROM cluster_foreign ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	})
	for _, tc := range []struct{ name, query, code, message string }{
		{"missing_table", "CLUSTER missing USING a", "42P01", `relation "missing" does not exist`},
		{"no_preference", "CLUSTER unmarked", "42704", `there is no previously clustered index for table "unmarked"`},
		{"missing_index", "CLUSTER t USING missing_idx", "42704", `index "missing_idx" for table "t" does not exist`},
		{"nonindex", "CLUSTER t USING not_an_index", "42809", `"not_an_index" is not an index`},
		{"wrong_parent", "CLUSTER t USING other_idx", "42809", `"other_idx" is not an index for table "t"`},
		{"hash", "CLUSTER t USING hash_idx", "0A000", `cannot cluster on index "hash_idx" because access method does not support clustering`},
		{"partial", "CLUSTER t USING partial_idx", "0A000", `cannot cluster on partial index "partial_idx"`},
		{"view", "CLUSTER v USING a", "42809", `"v" is not a table or materialized view`},
		{"index_target", "CLUSTER a", "42809", `"a" is not a table or materialized view`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				for _, q := range []string{"ALTER TABLE t CLUSTER ON a", "CREATE TABLE unmarked(id int)", "CREATE TABLE not_an_index(id int)", "CREATE INDEX other_idx ON not_an_index(id)", "CREATE INDEX hash_idx ON t USING hash(k)", "CREATE INDEX partial_idx ON t(k) WHERE k>0", "CREATE VIEW v AS SELECT id FROM t"} {
					persistenceExec(t, p, b, c, q)
				}
				want := persistenceDiagnostic(tc.code, tc.message)
				persistenceRun(t, p, b, c, fixture.ErrorDiagnostic(tc.query, want))
				held := clusterCommandHeld(t, p, b, c, tc.query)
				_, err := held.ExecContext(p.Ctx)
				fixture.AssertDiagnostic(t, err, &want)
				persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", true}, {"b", false}, {"hash_idx", false}, {"partial_idx", false}, {"u", false}}))
				persistenceRun(t, p, b, c, clusterCommandRows())
				persistenceRun(t, p, b, c, fixture.Exec("CLUSTER t", 0))
			})
		})
	}
	t.Run("invalid_index", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			persistenceExec(t, p, b, c, "CREATE TABLE duplicates(id int)")
			persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO duplicates VALUES(1),(1)", 2))
			persistenceExec(t, p, b, c, "CREATE INDEX valid_idx ON duplicates(id)")
			persistenceExec(t, p, b, c, "ALTER TABLE duplicates CLUSTER ON valid_idx")
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY invalid_idx ON duplicates(id)", fixture.ConcurrentIndexUnique("duplicates", "invalid_idx", "Key (id)=(1) is duplicated.", true))
			for _, prepared := range []bool{false, true} {
				persistenceFailure(t, p, b, c, "CLUSTER duplicates USING invalid_idx", prepared, persistenceDiagnostic("0A000", `cannot cluster on invalid index "invalid_idx"`))
			}
			persistenceRun(t, p, b, c, clusterState("duplicates", [][]any{{"invalid_idx", false}, {"valid_idx", true}}))
			persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM duplicates ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		})
	})
	t.Run("syntax", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, tc := range []struct {
			q, token string
			pos      int
		}{
			{"CLUSTER t USING", "", 16},
			{"CLUSTER t USING public.a", ".", 23},
			{"CLUSTER t USING a junk", "junk", 19},
		} {
			message := `syntax error at or near "` + tc.token + `"`
			if tc.token == "" {
				message = "syntax error at end of input"
			}
			want := persistenceDiagnostic("42601", message)
			want.Position = fixture.Position(tc.pos)
			p.Step(t, fixture.ErrorDiagnostic(tc.q, want))
			s := fixture.Prepared(fixture.ErrorDiagnostic(tc.q, want))
			s.ErrorStage = "prepare"
			p.Step(t, s)
		}
	})
	t.Run("cursor", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			clusterCommandSeed(t, p, b, c)
			for _, q := range []string{"BEGIN", "DECLARE cluster_cursor CURSOR FOR SELECT id FROM t ORDER BY id"} {
				persistenceExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			persistenceRun(t, p, b, c, fixture.Select("FETCH 1 FROM cluster_cursor", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			// REL_18_6 CheckTableNotInUse; root records every optional field.
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER t USING a", persistenceDiagnostic("55006", `cannot CLUSTER "t" because it is being used by active queries in this session`)))
		})
	})
	t.Run("pending_trigger", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE parent(id int PRIMARY KEY)", "CREATE TABLE child(id int REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)", "CREATE INDEX child_idx ON child(id)", "BEGIN"} {
				persistenceExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO child VALUES(1)", 1))
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER child USING child_idx", persistenceDiagnostic("55006", `cannot CLUSTER "child" because it has pending trigger events`)))
		})
	})
}
