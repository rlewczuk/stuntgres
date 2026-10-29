package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPartitionedTableKeys(t *testing.T) {
	t.Run("ordered_keys_and_definitions", func(t *testing.T) {
		pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := pptRun(t, p, b, b.DB)
			for _, q := range []string{
				"CREATE TABLE ppt_range(a INT,b INT,c INT) PARTITION BY RANGE(c,a)",
				"CREATE TABLE ppt_expr(a INT,b INT,c INT) PARTITION BY RANGE(a,(b+1),c,(a+2))",
				"CREATE TABLE ppt_definition(a INT) PARTITION BY RANGE((a+(1+2)))",
			} {
				run(fixture.Exec(q, 0))
			}
			pptBoth(run, pptFacts("ppt_range", "r", 2, "3 1", "1978 1978", "0 0", nil))
			pptBoth(run, pptFacts("ppt_expr", "r", 4, "1 0 3 0", "1978 1978 1978 1978", "0 0 0 0", "(b + 1), (a + 2)"))
			pptBoth(run, pptFacts("ppt_definition", "r", 1, "0", "1978", "0", "(a + (1 + 2))"))
			q := `SELECT partattrs[0] AS first,partattrs[1] AS second,array_length(partattrs,1)=partnatts AS attrs_length,array_length(partclass,1)=partnatts AS class_length,array_length(partcollation,1)=partnatts AS collation_length,array_lower(partattrs,1) AS attrs_lower,array_lower(partclass,1) AS class_lower,array_lower(partcollation,1) AS collation_lower,array_upper(partattrs,1) AS attrs_upper,array_upper(partclass,1) AS class_upper,array_upper(partcollation,1) AS collation_upper FROM pg_partitioned_table WHERE partrelid=$1::regclass`
			step := fixture.Select(q, []string{"first", "second", "attrs_length", "class_length", "collation_length", "attrs_lower", "class_lower", "collation_lower", "attrs_upper", "class_upper", "collation_upper"}, []string{"INT2", "INT2", "BOOL", "BOOL", "BOOL", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(1), true, true, true, int64(0), int64(0), int64(0), int64(1), int64(1), int64(1)}}, "ppt_range")
			pptBoth(run, step)
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, step)
			id := pptID(t, p, b.DB, "ppt_range")
			run(fixture.Exec("ALTER TABLE ppt_range RENAME c TO renamed_c", 0))
			if got := pptID(t, p, b.DB, "ppt_range"); got != id {
				t.Fatal("column rename changed identity", id, got)
			}
			pptBoth(run, pptFacts("ppt_range", "r", 2, "3 1", "1978 1978", "0 0", nil))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, step)
			run(fixture.Exec("ALTER TABLE ppt_expr RENAME b TO renamed_b", 0))
			pptBoth(run, pptFacts("ppt_expr", "r", 4, "1 0 3 0", "1978 1978 1978 1978", "0 0 0 0", "(renamed_b + 1), (a + 2)"))
		})
	})
	for _, tc := range []struct{ name, ddl, strategy, method, class, typ, collation string }{
		{"inherited_collation", `CREATE TABLE ppt_key(v TEXT COLLATE "C") PARTITION BY LIST(v)`, "l", "btree", "text_ops", "text", "C"},
		{"explicit_collation", `CREATE TABLE ppt_key(v TEXT COLLATE "C") PARTITION BY LIST(v COLLATE "POSIX")`, "l", "btree", "text_ops", "text", "POSIX"},
		{"selected_class", `CREATE TABLE ppt_key(v TEXT COLLATE "C") PARTITION BY LIST(v text_pattern_ops)`, "l", "btree", "text_pattern_ops", "text", "C"},
		{"hash", `CREATE TABLE ppt_key(v INT) PARTITION BY HASH(v)`, "h", "hash", "int4_ops", "integer", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := pptRun(t, p, b, b.DB)
				run(fixture.Exec(tc.ddl, 0))
				coll := "p.partcollation[0]=0"
				if tc.collation != "" {
					coll = fmt.Sprintf("p.partcollation[0]=(SELECT oid FROM pg_collation WHERE collname='%s' AND collnamespace=11)", tc.collation)
				}
				q := `SELECT p.partstrat,p.partnatts,p.partattrs::text,o.opcname,a.amname,o.opcintype=$1::regtype AS input_type,` + coll + ` AS collation,array_length(p.partattrs,1) AS attrs_length,array_length(p.partclass,1) AS class_length,array_length(p.partcollation,1) AS coll_length,array_lower(p.partclass,1) AS lower,array_upper(p.partclass,1) AS upper,p.partexprs IS NULL AS no_expression FROM pg_partitioned_table p JOIN pg_opclass o ON o.oid=p.partclass[0] JOIN pg_am a ON a.oid=o.opcmethod WHERE p.partrelid='ppt_key'::regclass`
				pptBoth(run, fixture.Select(q, []string{"partstrat", "partnatts", "partattrs", "opcname", "amname", "input_type", "collation", "attrs_length", "class_length", "coll_length", "lower", "upper", "no_expression"}, []string{"CHAR", "INT2", "TEXT", "NAME", "NAME", "BOOL", "BOOL", "INT4", "INT4", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{tc.strategy, int64(1), "1", tc.class, tc.method, true, true, int64(1), int64(1), int64(1), int64(0), int64(0), true}}, tc.typ))
				if tc.method == "hash" {
					pptBoth(run, fixture.Select(`SELECT o.opcmethod::bigint,o.opcintype::bigint,p.partclass[0]<>1978 AS not_btree FROM pg_partitioned_table p JOIN pg_opclass o ON o.oid=p.partclass[0] WHERE p.partrelid='ppt_key'::regclass`, []string{"opcmethod", "opcintype", "not_btree"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(405), int64(23), true}}))
				}
			})
		})
	}
}
