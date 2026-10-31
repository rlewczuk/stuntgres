package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexDiagnostics(t *testing.T) {
	t.Run("wrong_kind", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE INDEX ph_btree ON ph_part.ph_heap(k)", "CREATE VIEW ph_part.ph_view AS SELECT k FROM ph_part.ph_heap", "CREATE SEQUENCE ph_part.ph_sequence", "CREATE TABLE ph_part.\"Quoted É\"(k integer)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			for _, tc := range []struct{ input, name string }{{"ph_part.ph_heap", "ph_heap"}, {"ph_part.ph_btree", "ph_btree"}, {"ph_part.ph_view", "ph_view"}, {"ph_part.ph_sequence", "ph_sequence"}, {"ph_part.\"Quoted É\"", "Quoted É"}} {
				pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ph_part.pgstathashindex($1::regclass)", "42809", "relation \""+tc.name+"\" is not a hash index", tc.input))
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
			}
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap", []string{"k"}, []string{"INT4"}, nil))
		})
	})
	t.Run("partitions", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE TABLE ph_part.ph_parent(k integer) PARTITION BY RANGE(k)", "CREATE TABLE ph_part.ph_child PARTITION OF ph_part.ph_parent DEFAULT", "CREATE INDEX ph_child_index ON ph_part.ph_child USING hash(k)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO ph_part.ph_parent VALUES(1),(1),(NULL)", 3))
			pgstathashindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ph_part.pgstathashindex($1::regclass)", "42809", "relation \"ph_parent\" is not a hash index", "ph_part.ph_parent"))
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_child_index')", 2, 0)
			for _, table := range []string{"ph_parent", "ph_child"} {
				pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part."+table+" ORDER BY k NULLS LAST", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
			}
		})
	})
	t.Run("temporary", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS ph_temp")
			for _, q := range []string{"CREATE TEMP TABLE ph_temp(k integer)", "CREATE INDEX ph_temp_index ON ph_temp USING hash(k)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_temp_index'::regclass)", 0, 0)
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ph_temp_index'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			s := pgstatindexError("SELECT * FROM ph_part.pgstathashindex($1::oid)", "0A000", "cannot access temporary indexes of other sessions", oid)
			pgstathashindexBoth(t, p, b, peer, s)
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_temp_index'::regclass)", 0, 0)
		})
	})
	for _, tc := range []struct{ name, arg, code, message, position string; value any }{
		{"oid_zero", "$1::regclass", "XX000", "could not open relation with OID 0", "", "0"},
		{"oid_max", "$1::regclass", "XX000", "could not open relation with OID 4294967295", "", "4294967295"},
		{"missing_parameter", "$1::regclass", "42P01", "relation \"ph_part.ph_missing\" does not exist", "", "ph_part.ph_missing"},
		{"malformed_parameter", "$1::regclass", "42602", "invalid name syntax", "", "ph_part..ph_index"},
		{"missing_literal", "'ph_part.ph_missing'::regclass", "42P01", "relation \"ph_part.ph_missing\" does not exist", "39", nil},
		{"malformed_literal", "'ph_part..ph_index'::regclass", "42602", "invalid name syntax", "39", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT * FROM ph_part.pgstathashindex("+tc.arg+")"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = tc.position
				var args []any
				if tc.value != nil {
					args = []any{tc.value}
				}
				if tc.name == "missing_parameter" || tc.name == "malformed_parameter" {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				s := fixture.ImplicitError(q, d, args...)
				s.SelectRows = true
				// Literal input conversion belongs to analysis; parameter conversion
				// belongs to execution, including shared portal parameter Context.
				// Literal positions are specific to this query's regclass input.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				if tc.value == nil {
					st, err := c.PrepareContext(p.Ctx, q)
					if st != nil {
						st.Close()
						t.Fatal("bad regclass literal prepared")
					}
					fixture.AssertDiagnostic(t, err, &d)
				} else {
					s.Prepared = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					st, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					s.Prepared = false
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
				}
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
			})
		})
	}
}
