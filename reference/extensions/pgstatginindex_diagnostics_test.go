package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexDiagnostics(t *testing.T) {
	t.Run("wrong_kind", func(t *testing.T) {
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pgstatginindexBuild(t, p, b, c)
			defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS gg_fdw CASCADE")
			for _, q := range []string{"CREATE INDEX gg_hash ON gg_part.gg_heap USING hash(id)", "CREATE VIEW gg_part.gg_view AS SELECT id FROM gg_part.gg_heap", "CREATE SEQUENCE gg_part.gg_sequence", "CREATE TABLE gg_part.\"Quoted É\"(a integer)", "CREATE FOREIGN DATA WRAPPER gg_fdw", "CREATE SERVER gg_server FOREIGN DATA WRAPPER gg_fdw", "CREATE FOREIGN TABLE gg_part.gg_foreign(a integer) SERVER gg_server"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			for _, tc := range []struct{ input, name string }{{"gg_part.gg_heap", "gg_heap"}, {"gg_part.gg_heap_pkey", "gg_heap_pkey"}, {"gg_part.gg_hash", "gg_hash"}, {"gg_part.gg_view", "gg_view"}, {"gg_part.gg_sequence", "gg_sequence"}, {"gg_part.\"Quoted É\"", "Quoted É"}, {"gg_part.gg_foreign", "gg_foreign"}} {
				pgstatginindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM gg_part.pgstatginindex($1::regclass)", "42809", "relation \""+tc.name+"\" is not a GIN index", tc.input))
			}
			pgstatginindexContents(t, p, b, c, nil, nil)
		})
	})
	t.Run("partitions", func(t *testing.T) {
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE gg_part.gg_parent(id integer,a integer[]) PARTITION BY RANGE(id)", "CREATE TABLE gg_part.gg_child PARTITION OF gg_part.gg_parent DEFAULT", "INSERT INTO gg_part.gg_parent VALUES(1,'{2}')", "CREATE INDEX gg_leaf_index ON gg_part.gg_child USING gin(a) WITH(fastupdate=off)"} {
				n := int64(0)
				if q == "INSERT INTO gg_part.gg_parent VALUES(1,'{2}')" {
					n = 1
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, n))
			}
			pgstatginindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM gg_part.pgstatginindex($1::regclass)", "42809", "relation \"gg_parent\" is not a GIN index", "gg_part.gg_parent"))
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_leaf_index')"))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_parent WHERE a @> '{2}'::integer[] ORDER BY id", 1))
		})
	})
	t.Run("temporary", func(t *testing.T) {
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS gg_temp")
			for _, q := range []string{"CREATE TEMP TABLE gg_temp(a integer[])", "CREATE INDEX gg_temp_index ON gg_temp USING gin(a)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_temp_index'::regclass)"))
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'gg_temp_index'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			pgstatginindexBoth(t, p, b, peer, pgstatindexError("SELECT * FROM gg_part.pgstatginindex($1::oid)", "0A000", "cannot access temporary indexes of other sessions", oid))
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_temp_index'::regclass)"))
		})
	})
	for _, tc := range []struct {
		name, arg, code, message, position string
		value                              any
	}{
		{"oid_zero", "$1::regclass", "XX000", "could not open relation with OID 0", "", "0"},
		{"oid_max", "$1::regclass", "XX000", "could not open relation with OID 4294967295", "", "4294967295"},
		{"missing_parameter", "$1::regclass", "42P01", "relation \"gg_part.gg_missing\" does not exist", "", "gg_part.gg_missing"},
		{"malformed_parameter", "$1::regclass", "42602", "invalid name syntax", "", "gg_part..gg_array"},
		{"missing_literal", "'gg_part.gg_missing'::regclass", "42P01", "relation \"gg_part.gg_missing\" does not exist", "38", nil},
		{"malformed_literal", "'gg_part..gg_array'::regclass", "42602", "invalid name syntax", "38", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT * FROM gg_part.pgstatginindex(" + tc.arg + ")"
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
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				st, err := c.PrepareContext(p.Ctx, q)
				if tc.value == nil {
					if st != nil {
						st.Close()
						t.Fatal("bad regclass literal prepared")
					}
					fixture.AssertDiagnostic(t, err, &d)
				} else {
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					s.Prepared = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s.Prepared = false
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
				}
			})
		})
	}
}
