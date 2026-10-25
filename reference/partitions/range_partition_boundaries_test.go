package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionBoundaries(t *testing.T) {
	cases := []struct {
		name, sql, message string
		count              int64
		query              bool
	}{
		{"update_parent", "UPDATE rp_boundary SET payload='updated'", "", 2, false},
		{"update_leaf", "UPDATE rp_boundary_a SET payload='updated'", "", 1, false},
		{"delete_parent", "DELETE FROM rp_boundary", "deleting from partition hierarchies is unsupported", 2, false},
		{"delete_leaf", "DELETE FROM rp_boundary_a", "deleting from partition hierarchies is unsupported", 1, false},
		{"row_lock", "SELECT * FROM rp_boundary ORDER BY k FOR UPDATE", "row locking partition hierarchies is unsupported", 0, true},
		{"lock", "LOCK TABLE rp_boundary IN ACCESS SHARE MODE", "locking partition hierarchies is unsupported", 0, false},
		{"alter_default", "ALTER TABLE rp_boundary ALTER payload SET DEFAULT 'fresh'", "altering partition hierarchies is unsupported", 0, false},
		{"alter_rename", "ALTER TABLE rp_boundary RENAME COLUMN payload TO label", "altering partition hierarchies is unsupported", 0, false},
		{"alter_type", "ALTER TABLE rp_boundary ALTER payload TYPE VARCHAR(20)", "altering partition hierarchies is unsupported", 0, false},
		{"index", "CREATE INDEX rp_boundary_index ON rp_boundary(payload)", "partitioned indexes are unsupported", 0, false},
		{"reindex", "REINDEX TABLE rp_boundary", "partitioned indexes are unsupported", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE rp_boundary(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE rp_boundary_a PARTITION OF rp_boundary FOR VALUES FROM(1) TO(3)", 0))
				run(fixture.Exec("CREATE TABLE rp_boundary_b PARTITION OF rp_boundary FOR VALUES FROM(3) TO(5)", 0))
				run(fixture.Exec("INSERT INTO rp_boundary VALUES(1,'a'),(3,'b')", 2))
				if b.PG || c.name == "update_parent" || c.name == "update_leaf" {
					if c.name == "lock" {
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(c.sql, 0))
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else if c.query {
						run(fixture.Select(c.sql, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "b"}}))
					} else {
						run(fixture.Exec(c.sql, c.count))
					}
					rows := [][]any{{int64(1), "a"}, {int64(3), "b"}}
					names := []string{"k", "payload"}
					types := []string{"INT4", "TEXT"}
					switch c.name {
					case "update_parent":
						rows = [][]any{{int64(1), "updated"}, {int64(3), "updated"}}
					case "update_leaf":
						rows[0][1] = "updated"
					case "delete_parent":
						rows = nil
					case "delete_leaf":
						rows = rows[1:]
					case "alter_rename":
						names[1] = "label"
					case "alter_type":
						types[1] = "VARCHAR"
					}
					run(fixture.Select("SELECT * FROM rp_boundary ORDER BY k", names, types, rows))
				} else {
					s := referenceRangeError(c.sql, "0A000", c.message, fixture.Diagnostic{})
					s.SelectRows = c.query
					if c.name == "lock" {
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					} else {
						run(s)
					}
					run(fixture.Select("SELECT * FROM rp_boundary ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "b"}}))
					run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_boundary_index'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		})
	}
}

func TestReferenceRangePartitionConstraintBoundaries(t *testing.T) {
	for _, c := range []struct{ name, columns string }{{"generated", "k INT,g INT GENERATED ALWAYS AS(k+1) STORED"}, {"identity", "k INT GENERATED ALWAYS AS IDENTITY"}, {"serial", "k SERIAL"}} {
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(c.name+"/"+route.name, func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					q := "CREATE TABLE rp_combo(" + c.columns + ") PARTITION BY RANGE(k)"
					if b.PG {
						step := fixture.Exec(q, 0)
						step.Prepared = route.prepared
						run(step)
						run(fixture.Exec("CREATE TABLE rp_combo_leaf PARTITION OF rp_combo DEFAULT", 0))
						input := "INSERT INTO rp_combo(k) VALUES(1)"
						if c.name == "identity" {
							input = "INSERT INTO rp_combo DEFAULT VALUES"
						}
						run(fixture.Exec(input, 1))
						run(referenceRangeRows("SELECT k FROM rp_combo", 1))
					} else {
						step := referenceRangeError(q, "0A000", "partitioned table constraint combination is unsupported", fixture.Diagnostic{})
						step.Prepared = route.prepared
						run(step)
						run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_combo' OR relname='rp_combo_leaf' OR relname='rp_combo_k_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			})
		}
	}
}

func TestReferenceRangePartitionHierarchyKeySafety(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE partitioned(k INT,b TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE part PARTITION OF partitioned FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("INSERT INTO partitioned VALUES(1,'keep')", 1))
		run(referenceRangeError("ALTER TABLE ONLY partitioned DROP b", "42P16", "cannot drop column from only the partitioned table when partitions exist", fixture.Diagnostic{Hint: "Do not specify the ONLY keyword."}))
		run(referenceRangeError("ALTER TABLE partitioned DROP k CASCADE", "42P16", `cannot drop column "k" because it is part of the partition key of relation "partitioned"`, fixture.Diagnostic{}))
		run(fixture.Select("SELECT * FROM partitioned", []string{"k", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
		// Non-key recursive ALTER is external to the selected native hierarchy
		// mutation contract, but retains the original PostgreSQL assertion.
		if b.PG {
			run(fixture.Exec("ALTER TABLE partitioned DROP b", 0))
			run(referenceRangeRows("SELECT * FROM part", 1))
		} else {
			run(referenceRangeError("ALTER TABLE partitioned DROP b", "0A000", "altering partition hierarchies is unsupported", fixture.Diagnostic{}))
		}
		run(fixture.Exec("CREATE TABLE rp_expr_drop(k INT) PARTITION BY RANGE((k+1))", 0))
		run(referenceRangeError("ALTER TABLE rp_expr_drop DROP k CASCADE", "42P16", `cannot drop column "k" because it is part of the partition key of relation "rp_expr_drop"`, fixture.Diagnostic{}))
	})
}
