package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexDiagnostics(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE VIEW ps_part.ps_view AS SELECT k FROM ps_part.ps_heap", "CREATE SEQUENCE ps_part.ps_sequence", "CREATE INDEX ps_hash ON ps_part.ps_heap USING hash(k)"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, name := range []string{"ps_heap", "ps_view", "ps_sequence", "ps_hash"} {
			for _, typ := range []string{"text", "regclass"} {
				pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex($1::"+typ+")", "42809", "relation \""+name+"\" is not a btree index", "ps_part."+name))
			}
		}
		for _, name := range []string{"ps_part.ps_missing", "ps_missing"} {
			pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex($1::text)", "42P01", "relation \""+name+"\" does not exist", name))
		}
		for _, name := range []string{"", "\"", "ps_part..ps_index"} {
			pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex($1::text)", "42602", "invalid name syntax", name))
		}
		for _, oid := range []string{"0", "4294967295"} {
			pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex($1::regclass)", "XX000", "could not open relation with OID "+oid, oid))
		}
		// Failed inspection is read-only and cannot poison later valid calls.
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex($1::text)", "ps_part.ps_index")
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ps_part.ps_heap", []string{"k"}, []string{"INT4"}, nil))

		// Use real partition DDL and explicit index identities, not a synthetic
		// descriptor or an assumed automatically generated child-index name.
		for _, q := range []string{
			"CREATE TABLE ps_part.ps_partitioned(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE ps_part.ps_child PARTITION OF ps_part.ps_partitioned DEFAULT",
			"CREATE UNIQUE INDEX ps_partitioned_index ON ONLY ps_part.ps_partitioned(k)",
			"CREATE UNIQUE INDEX ps_child_index ON ps_part.ps_child(k)",
			"ALTER INDEX ps_part.ps_partitioned_index ATTACH PARTITION ps_part.ps_child_index",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ps_part.ps_partitioned VALUES(1),(2),(3)", 3))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT inhparent='ps_part.ps_partitioned_index'::regclass AS parent FROM pg_inherits WHERE inhrelid='ps_part.ps_child_index'::regclass", []string{"parent"}, []string{"BOOL"}, [][]any{{true}}))
		checkChild := func(want [][]any) {
			for _, typ := range []string{"text", "regclass"} {
				pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_child_index'::"+typ+")")
			}
			for _, name := range []string{"ps_partitioned", "ps_child"} {
				pgstatindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ps_part."+name+" ORDER BY k", []string{"k"}, []string{"INT4"}, want))
			}
		}
		want := [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
		checkChild(want)
		for _, name := range []string{"ps_partitioned", "ps_partitioned_index"} {
			for _, typ := range []string{"text", "regclass"} {
				for _, prepared := range []bool{false, true} {
					s := pgstatindexError("SELECT * FROM ps_part.pgstatindex($1::"+typ+")", "42809", "relation \""+name+"\" is not a btree index", "ps_part."+name)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					// Every rejected call leaves both overloads of the ordinary
					// attached child callable, and all routed rows unchanged.
					checkChild(want)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ps_part.ps_partitioned VALUES(4)", 1))
		checkChild(append(want, []any{int64(4)}))
	})
}
