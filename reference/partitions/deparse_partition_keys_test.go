package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionKeys(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ name, columns, key, want string }{
			{"range", "k INT,v TEXT", "RANGE(k)", "RANGE (k)"},
			{"list", "k INT", "LIST(k)", "LIST (k)"},
			{"hash", "k INT,v TEXT", "HASH(k,v)", "HASH (k, v)"},
			{"arithmetic", "k INT", "RANGE((k+1))", "RANGE (((k + 1)))"},
			{"function", "txt TEXT", "LIST(lower(txt))", "LIST (lower(txt))"},
			{"unfolded", "k INT", "RANGE((k+(1+2)))", "RANGE (((k + (1 + 2))))"},
			{"explicit_default", "k INT", "RANGE(k int4_ops)", "RANGE (k)"},
			{"hash_default", "k INT", "HASH(k int4_ops)", "HASH (k)"},
			{"collation_same", `k TEXT COLLATE "C"`, `LIST(k COLLATE "C" text_ops)`, "LIST (k)"},
			{"collation_other", `k TEXT COLLATE "C"`, `RANGE(k COLLATE "POSIX")`, `RANGE (k COLLATE "POSIX")`},
			{"pattern", `k TEXT COLLATE "C"`, "RANGE(k text_pattern_ops)", "RANGE (k text_pattern_ops)"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				name := "dp_key_" + tc.name
				run(fixture.Exec("CREATE TABLE "+name+"("+tc.columns+") PARTITION BY "+tc.key, 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_catalog.pg_get_partkeydef('"+name+"'::regclass) AS definition", tc.want)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('"+name+"'::regclass) AS definition", nil))
			})
		}
		run(fixture.Exec("CREATE TABLE dp_plain(k INT)", 0))
		run(fixture.Exec("CREATE SEQUENCE dp_sequence", 0))
		run(fixture.Exec("CREATE TABLE dp_key_leaf PARTITION OF dp_key_range FOR VALUES FROM(0) TO(10)", 0))
		for _, name := range []string{"pg_get_partkeydef", "pg_get_partition_constraintdef"} {
			for _, arg := range []string{"NULL", "NULL::oid", "0::oid", "4294967295::oid", "'dp_plain'::regclass", "'dp_sequence'::regclass", "'pg_catalog.int4'::regtype::oid"} {
				s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+name+"("+arg+") AS definition", nil)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_key_leaf'::regclass) AS definition", nil))
	})
}
