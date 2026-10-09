//go:build reference

package reference_test

import "testing"

func TestReferenceAlterTypeForeignKeyBoundary(t *testing.T) {
	p := newReferencePair(t)
	t.Run("postgres", func(t *testing.T) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
		run(referenceExec("CREATE TABLE parent(c INT PRIMARY KEY)", 0))
		run(referenceExec("CREATE TABLE child(c INT REFERENCES parent(c))", 0))
		run(referenceExec("INSERT INTO parent VALUES(1)", 1))
		run(referenceExec("INSERT INTO child VALUES(1)", 1))
		ids := make(map[string]int64)
		for _, name := range []string{"parent_pkey", "child_c_fkey"} {
			var id int64
			if err := p.postgres.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&id); err != nil || id <= 0 {
				t.Fatal(name, id, err)
			}
			ids[name] = id
		}
		run(referenceExec("ALTER TABLE parent ALTER c TYPE BIGINT", 0))
		for name, oldID := range ids {
			var newID int64
			if err := p.postgres.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&newID); err != nil || newID <= 0 || newID == oldID {
				t.Fatal("successful TYPE must rebuild dependency identity", name, oldID, newID, err)
			}
			run(referenceSelect("SELECT count(*)::int FROM pg_constraint WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, oldID))
			run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, newID))
		}
		run(referenceSelect("SELECT fk.conindid=pk.conindid AS linked FROM pg_constraint fk,pg_constraint pk WHERE fk.conname='child_c_fkey' AND pk.conname='parent_pkey'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
		run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid=(SELECT conindid FROM pg_constraint WHERE conname='parent_pkey')", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
		run(referenceSelect("SELECT c FROM parent", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='parent'::regclass AND conname='parent_pkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (c)"}}))
		failure := referenceImplicitError("INSERT INTO child VALUES(2)", referenceDiagnostic{Code: "23503", Message: `insert or update on table "child" violates foreign key constraint "child_c_fkey"`, Detail: `Key (c)=(2) is not present in table "parent".`, Schema: "public", Table: "child", Constraint: "child_c_fkey"})
		failure.errorStage = "execute"
		run(failure)
		run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='child'::regclass AND conname='child_c_fkey'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (c) REFERENCES parent(c)"}}))
		run(referenceExec("INSERT INTO child VALUES(1)", 1))
		run(referenceSelect("SELECT c FROM child ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
	})
	t.Run("native", func(t *testing.T) {
		failure := referenceImplicitError("CREATE TABLE parent(c INT PRIMARY KEY)", referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "27"})
		failure.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE parent(c INT)", 0))
		failure = referenceImplicitError("CREATE TABLE child(c INT REFERENCES parent(c))", referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "26"})
		failure.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
	})
}

func TestReferenceAlterTypeCompatiblePlainIndexBoundary(t *testing.T) {
	p := newReferencePair(t)
	t.Run("postgres", func(t *testing.T) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
		run(referenceExec("CREATE TABLE t(c INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(3),(1),(2),(NULL)", 4))
		run(referenceExec("CREATE INDEX ix ON t(c)", 0))
		var oldID int64
		if err := p.postgres.QueryRowContext(p.ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&oldID); err != nil || oldID <= 0 {
			t.Fatal(oldID, err)
		}
		run(referenceExec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
		var newID int64
		if err := p.postgres.QueryRowContext(p.ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&newID); err != nil || newID <= 0 || newID == oldID {
			t.Fatal("compatible rewrite must rebuild index identity", oldID, newID, err)
		}
		run(referenceSelect("SELECT count(*)::int FROM pg_class WHERE oid=$1", []string{"count"}, []string{"INT4"}, [][]any{{int64(0)}}, oldID))
		run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid='ix'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
		run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
		run(referenceSelect("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (c)"}}))
		run(referenceExec("SET enable_seqscan=off", 0))
		run(referenceSelect("EXPLAIN (COSTS OFF) SELECT c FROM t WHERE c=2", []string{"QUERY PLAN"}, []string{"TEXT"}, [][]any{{"Index Only Scan using ix on t"}, {"  Index Cond: (c = 2)"}}))
		run(referenceSelect("SELECT c FROM t WHERE c=2", []string{"c"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceExec("RESET enable_seqscan", 0))
		run(referenceSelect("SELECT atttypid::int FROM pg_attribute WHERE attrelid='t'::regclass AND attname='c'", []string{"atttypid"}, []string{"INT4"}, [][]any{{int64(20)}}))
	})
	t.Run("native", func(t *testing.T) {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE t(c INT)", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO t VALUES(3),(1),(2),(NULL)", 4))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE INDEX ix ON t(c)", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT relkind FROM pg_class WHERE relname='ix'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
	})
}
