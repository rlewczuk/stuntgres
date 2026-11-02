package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgFreespaceDiagnostics(t *testing.T) {
	fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fsRun(t, p, b, c, fixture.Exec("SET search_path=fs_part,pg_catalog", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		for _, args := range [][]any{{nil, int64(-1)}, {"fs_part.heap", nil}, {nil, nil}, {int64(4294967294), nil}} {
			fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT fs_part.pg_freespace($1::regclass,$2::bigint) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{nil}}, args...), b.PG))
		}
		fsEmpty(t, p, b, c, nil)
		for _, block := range []int64{0, 1, 4294967294} {
			fsProbe(t, p, b, c, "fs_part.heap", block, int64(0))
		}
		for _, block := range []int64{-1, 4294967295, 9223372036854775807} {
			fsBoth(t, p, b, c, fsError("SELECT pg_freespace('heap',$1::bigint)", "22023", "invalid block number", block))
		}
		fsBoth(t, p, b, c, fsError("SELECT pg_freespace('heap',-1)", "22023", "invalid block number"))
		fsBoth(t, p, b, c, fsError("SELECT pg_freespace('heap',4294967295)", "22023", "invalid block number"))
		fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.dropped(k integer)", 0))
		dropped := fsOID(t, p, c, "fs_part.dropped")
		fsRun(t, p, b, c, fixture.Exec("DROP TABLE fs_part.dropped", 0))
		for _, oid := range []int64{0, 4294967294, 4294967295, dropped} {
			for _, block := range []int64{-1, 0, 4294967295} {
				fsBoth(t, p, b, c, fsError("SELECT fs_part.pg_freespace($1::oid::regclass,$2::bigint)", "XX000", fmt.Sprintf("could not open relation with OID %d", oid), oid, block))
			}
			fsEmpty(t, p, b, c, oid)
		}
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS fs_fdw CASCADE")
		for _, q := range []string{
			"CREATE VIEW fs_part.fs_view AS SELECT k FROM fs_part.heap",
			"CREATE TYPE fs_part.composite AS(k integer)",
			"CREATE TABLE fs_part.parent(k integer,p point) PARTITION BY RANGE(k)",
			"CREATE INDEX parent_index ON fs_part.parent USING gist(p)",
			"CREATE FOREIGN DATA WRAPPER fs_fdw",
			"CREATE SERVER fs_server FOREIGN DATA WRAPPER fs_fdw",
			"CREATE FOREIGN TABLE fs_part.foreign_heap(k integer) SERVER fs_server",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		wrongKind := func(name, kind string) {
			for _, block := range []int64{-1, 0, 4294967295} {
				s := fsError("SELECT fs_part.pg_freespace($1::regclass,$2::bigint)", "42809", "relation \""+name+"\" does not have storage", "fs_part."+name, block)
				s.Diagnostic.Detail = "This operation is not supported for " + kind + "."
				fsBoth(t, p, b, c, s)
			}
			fsEmpty(t, p, b, c, "fs_part."+name)
		}
		for _, tc := range []struct{ name, kind string }{{"fs_view", "views"}, {"composite", "composite types"}, {"parent", "partitioned tables"}, {"parent_index", "partitioned indexes"}, {"foreign_heap", "foreign tables"}} {
			wrongKind(tc.name, tc.kind)
		}
		for _, q := range []string{"SELECT pg_freespace('missing_fs',0)", "SELECT * FROM pg_freespace('missing_fs')"} {
			s := fsError(q, "42P01", "relation \"missing_fs\" does not exist")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "'missing_fs'") + 1)
			fsBoth(t, p, b, c, s)
		}
		for _, tc := range []struct {
			q, code, message, context string
			args                      []any
		}{
			{"SELECT pg_freespace('heap',$1::bigint)", "22P02", "invalid input syntax for type bigint: \"bad\"", "unnamed portal parameter $1 = '...'", []any{"bad"}},
			{"SELECT pg_freespace('heap',$1::bigint)", "22003", "value \"9223372036854775808\" is out of range for type bigint", "unnamed portal parameter $1 = '...'", []any{"9223372036854775808"}},
			{"SELECT pg_freespace('heap',9223372036854775808::bigint)", "22003", "bigint out of range", "", nil},
			{"SELECT pg_freespace($1::regclass,0)", "42602", "invalid name syntax", "unnamed portal parameter $1 = '...'", []any{"fs_part..heap"}},
		} {
			s := fsError(tc.q, tc.code, tc.message, tc.args...)
			s.Diagnostic.Context = tc.context
			fsBoth(t, p, b, c, s)
		}
		fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.atomic_result(avail smallint)", 0))
		s := fsError("INSERT INTO fs_part.atomic_result VALUES(pg_freespace('heap',0)),(pg_freespace('heap',-1))", "22023", "invalid block number")
		s.SelectRows = false
		fsBoth(t, p, b, c, s)
		fsBoth(t, p, b, c, fixture.Select("SELECT avail FROM fs_part.atomic_result", []string{"avail"}, []string{"INT2"}, nil))
		// Failures must not allocate phantom pages or publish partial results.
		fsEmpty(t, p, b, c, "fs_part.heap")
		fsProbe(t, p, b, c, "fs_part.heap", 0, int64(0))
	})
}
