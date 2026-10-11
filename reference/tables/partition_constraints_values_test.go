package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsValues(t *testing.T) {
	for _, c := range []struct {
		name, declaration, index string
		primary, nnd             bool
	}{
		{"distinct", "UNIQUE(k,payload)", "pc_a_k_payload_key", false, false},
		{"not_distinct", "UNIQUE NULLS NOT DISTINCT(k,payload)", "pc_a_k_payload_key", false, true},
		{"primary", "PRIMARY KEY(k,payload)", "pc_a_pkey", true, false},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			partitionConstraintTree(run, c.declaration)
			run(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 1, "x"))
			run(fixture.Exec("INSERT INTO pc_b VALUES($1,$2)", 1, 3, "x"))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_a", c.index, "Key (k, payload)=(1, x) already exists.", 1, "x"))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(4,'late'),(1,'x')", "pc_a", c.index, "Key (k, payload)=(1, x) already exists."))
			want := fixture.Select("SELECT k,payload FROM pc_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(3), "x"}})
			want.CheckMetadata = referenceRangeMetadata(b.PG, want.Columns, want.Types, []bool{!c.primary, !c.primary})
			run(want)
			if c.primary {
				for _, n := range []struct{ query, leaf, column, row string }{
					{"INSERT INTO pc_root VALUES(4,'late'),(1,NULL)", "pc_a", "payload", "1, null"},
					{"INSERT INTO pc_root VALUES(NULL,'x')", "pc_def", "k", "null, x"},
				} {
					run(referenceRangeError(n.query, "23502", "null value in column \""+n.column+"\" of relation \""+n.leaf+"\" violates not-null constraint", fixture.Diagnostic{Schema: "public", Table: n.leaf, Column: n.column, Detail: "Failing row contains (" + n.row + ")."}))
					run(want)
				}
			} else {
				run(fixture.Exec("INSERT INTO pc_root VALUES(1,NULL),(NULL,'z')", 2))
				if c.nnd {
					run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,NULL)", "pc_a", c.index, "Key (k, payload)=(1, null) already exists."))
					run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(NULL,'z')", "pc_def", "pc_def_k_payload_key", "Key (k, payload)=(null, z) already exists."))
				} else {
					run(fixture.Exec("INSERT INTO pc_root VALUES(1,NULL),(NULL,'z')", 2))
				}
			}
			run(fixture.Exec("INSERT INTO pc_a VALUES(2,'healthy')", 1))
			run(fixture.Select("SELECT k,payload FROM pc_b", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "x"}}))
		})
	}
}

func TestReferencePartitionConstraintsTypedEquality(t *testing.T) {
	for _, c := range []struct{ name, typ, first, equal, detail string }{
		{"numeric", "NUMERIC", "1.0", "1.00", "1.00"},
		{"bpchar", "CHAR(3)", "x", "x ", "x  "},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pc_root(k INT,payload "+c.typ+",UNIQUE(k,payload)) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 1, c.first))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_a", "pc_a_k_payload_key", "Key (k, payload)=(1, "+c.detail+") already exists.", 1, c.equal))
			run(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 2, c.equal))
			run(fixture.Select("SELECT k FROM pc_root ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferencePartitionConstraintsPreparedMetadata(t *testing.T) {
	partitionConstraintCase(t, "held_through_drop_rollback", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(run, "PRIMARY KEY(k,payload)")
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		query := "SELECT k,payload FROM pc_root ORDER BY k"
		selectStmt, err := a.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		insert := "INSERT INTO pc_root VALUES($1,$2)"
		insertStmt, err := a.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer insertStmt.Close()
		selectHeld := fixture.HeldStatement{Executor: a, Statement: selectStmt, SQL: query}
		insertHeld := fixture.HeldStatement{Executor: a, Statement: insertStmt, SQL: insert}
		check := func(rows [][]any) {
			s := fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows)
			s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{false, false})
			fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, s)
		}
		check(nil)
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insert, 1, 1, "x"))
		check([][]any{{int64(1), "x"}})
		txrun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		txrun(fixture.Exec("BEGIN", 0))
		txrun(fixture.Exec("SAVEPOINT before_drop", 0))
		txrun(fixture.Exec("ALTER TABLE pc_root DROP CONSTRAINT pc_root_pkey", 0))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insert, 1, 1, "x"))
		check([][]any{{int64(1), "x"}, {int64(1), "x"}})
		txrun(fixture.Exec("ROLLBACK TO before_drop", 0))
		fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, partitionConstraintDuplicate(insert, "pc_a", "pc_a_pkey", "Key (k, payload)=(1, x) already exists.", 1, "x"))
		txrun(fixture.Exec("ROLLBACK TO before_drop", 0))
		txrun(fixture.Exec("COMMIT", 0))
		check([][]any{{int64(1), "x"}})
	})
}

func TestReferencePartitionConstraintsNestedValues(t *testing.T) {
	for _, c := range []struct {
		name, declaration, index string
		primary, nnd             bool
	}{
		{"distinct", "UNIQUE(k,payload)", "pc_leaf_k_payload_key", false, false},
		{"not_distinct", "UNIQUE NULLS NOT DISTINCT(k,payload)", "pc_leaf_k_payload_key", false, true},
		{"primary", "PRIMARY KEY(k,payload)", "pc_leaf_pkey", true, false},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			for _, query := range []string{"CREATE TABLE pc_root(k INT,payload TEXT," + c.declaration + ") PARTITION BY RANGE(k)", "CREATE TABLE pc_mid PARTITION OF pc_root FOR VALUES FROM(1) TO(10) PARTITION BY RANGE(k)", "CREATE TABLE pc_leaf PARTITION OF pc_mid DEFAULT", "CREATE TABLE pc_other PARTITION OF pc_root DEFAULT"} {
				run(fixture.Exec(query, 0))
			}
			run(fixture.Exec("INSERT INTO pc_mid VALUES($1,$2)", 1, 1, "x"))
			run(fixture.Exec("INSERT INTO pc_leaf VALUES($1,$2)", 1, 2, "y"))
			run(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 20, "x"))
			for _, target := range []string{"pc_root", "pc_mid", "pc_leaf"} {
				run(partitionConstraintDuplicate("INSERT INTO "+target+" VALUES($1,$2)", "pc_leaf", c.index, "Key (k, payload)=(1, x) already exists.", 1, "x"))
			}
			run(partitionConstraintDuplicate("INSERT INTO pc_mid VALUES(3,'late'),(2,'y')", "pc_leaf", c.index, "Key (k, payload)=(2, y) already exists."))
			bag := func(rows [][]any) {
				s := fixture.Select("SELECT k,payload FROM pc_root ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows)
				s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{!c.primary, !c.primary})
				run(s)
			}
			bag([][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(20), "x"}})
			if c.primary {
				for _, target := range []string{"pc_root", "pc_mid", "pc_leaf"} {
					run(referenceRangeError("INSERT INTO "+target+" VALUES(1,NULL)", "23502", `null value in column "payload" of relation "pc_leaf" violates not-null constraint`, fixture.Diagnostic{Schema: "public", Table: "pc_leaf", Column: "payload", Detail: "Failing row contains (1, null)."}))
				}
				bag([][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(20), "x"}})
			} else {
				run(fixture.Exec("INSERT INTO pc_mid VALUES(1,NULL)", 1))
				run(fixture.Exec("INSERT INTO pc_other VALUES(20,NULL)", 1))
				if c.nnd {
					run(partitionConstraintDuplicate("INSERT INTO pc_leaf VALUES(1,NULL)", "pc_leaf", c.index, "Key (k, payload)=(1, null) already exists."))
					run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(20,NULL)", "pc_other", "pc_other_k_payload_key", "Key (k, payload)=(20, null) already exists."))
					bag([][]any{{int64(1), "x"}, {int64(1), nil}, {int64(2), "y"}, {int64(20), "x"}, {int64(20), nil}})
				} else {
					run(fixture.Exec("INSERT INTO pc_leaf VALUES(1,NULL)", 1))
					run(fixture.Exec("INSERT INTO pc_root VALUES(20,NULL)", 1))
					bag([][]any{{int64(1), "x"}, {int64(1), nil}, {int64(1), nil}, {int64(2), "y"}, {int64(20), "x"}, {int64(20), nil}, {int64(20), nil}})
				}
			}
			run(fixture.Exec("INSERT INTO pc_leaf VALUES(3,'healthy')", 1))
			run(fixture.Select("SELECT k,payload FROM pc_mid WHERE k=3", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "healthy"}}))
		})
	}
}
