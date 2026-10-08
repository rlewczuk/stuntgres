//go:build reference

package reference_test

import "testing"

func TestReferenceAlterTypeDependentCapabilityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, create, seed, dependency, alter string
		want                                  referenceDiagnostic
	}{
		{"unique", "CREATE TABLE t(c INT UNIQUE)", "INSERT INTO t VALUES(1),(2)", "", "ALTER TABLE t ALTER c TYPE INT USING 0", referenceDiagnostic{Code: "23505", Message: `could not create unique index "t_c_key"`, Detail: "Key (c)=(0) is duplicated.", Schema: "public", Table: "t", Constraint: "t_c_key"}},
		{"expression-index", "CREATE TABLE t(c INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t((c+1))", "ALTER TABLE t ALTER c TYPE TEXT USING c::text", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "46"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			t.Run("postgres", func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
				run(referenceExec(tc.create, 0))
				count := int64(1)
				if tc.name == "unique" {
					count = 2
				}
				run(referenceExec(tc.seed, count))
				if tc.dependency != "" {
					run(referenceExec(tc.dependency, 0))
				}
				var dependencyID int64
				identityQuery := "SELECT oid::bigint FROM pg_constraint WHERE conrelid='t'::regclass AND conname=$1"
				name := "t_c_key"
				if tc.name == "expression-index" {
					name = "ix"
					identityQuery = "SELECT oid::bigint FROM pg_class WHERE oid=$1::regclass"
				}
				if err := p.postgres.QueryRowContext(p.ctx, identityQuery, name).Scan(&dependencyID); err != nil || dependencyID <= 0 {
					t.Fatal(dependencyID, err)
				}
				var backingID int64
				if tc.name == "unique" {
					if err := p.postgres.QueryRowContext(p.ctx, "SELECT 't_c_key'::regclass::oid::bigint").Scan(&backingID); err != nil || backingID <= 0 {
						t.Fatal(backingID, err)
					}
				}
				failure := referenceImplicitError(tc.alter, tc.want)
				failure.errorStage = "execute"
				run(failure)
				var retainedID int64
				if err := p.postgres.QueryRowContext(p.ctx, identityQuery, name).Scan(&retainedID); err != nil || retainedID != dependencyID {
					t.Fatal("failed TYPE changed dependency identity", dependencyID, retainedID, err)
				}
				if tc.name == "expression-index" {
					run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid='ix'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
				} else {
					run(referenceSelect("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, dependencyID))
					if tc.name == "unique" {
						var retainedBackingID int64
						if err := p.postgres.QueryRowContext(p.ctx, "SELECT 't_c_key'::regclass::oid::bigint").Scan(&retainedBackingID); err != nil || retainedBackingID != backingID {
							t.Fatal("failed TYPE replaced unique backing index", backingID, retainedBackingID, err)
						}
						run(referenceSelect("SELECT indisvalid FROM pg_index WHERE indexrelid='t_c_key'::regclass", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{true}}))
					}
				}
				rows := [][]any{{int64(1)}}
				if tc.name == "unique" {
					rows = append(rows, []any{int64(2)})
				}
				run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, rows))
				if tc.name == "unique" {
					run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass AND conname='t_c_key'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (c)"}}))
					run(referenceSelect("SELECT pg_get_indexdef('t_c_key'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX t_c_key ON public.t USING btree (c)"}}))
					failure := referenceImplicitError("INSERT INTO t VALUES(1)", referenceDiagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_c_key"`, Detail: "Key (c)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_c_key"})
					failure.errorStage = "execute"
					run(failure)
					run(referenceExec("INSERT INTO t VALUES(3)", 1))
				}
				if tc.name == "expression-index" {
					run(referenceSelect("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (((c + 1)))"}}))
					run(referenceSelect("SELECT c+1 AS next FROM t WHERE c+1=2", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}}))
				}
			})
			t.Run("native", func(t *testing.T) {
				if tc.dependency != "" {
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(tc.create, 0))
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(tc.seed, 1))
					failure := referenceImplicitError(tc.dependency, referenceDiagnostic{Code: "0A000", Message: "expression indexes are unsupported", Position: "22"})
					failure.errorStage = "execute"
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
					preparedFailure := referencePrepared(failure)
					preparedFailure.errorStage = "prepare"
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, preparedFailure)
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT c FROM t", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
				} else {
					position := "22"
					failure := referenceImplicitError(tc.create, referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: position})
					failure.errorStage = "execute"
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
				}
			})
		})
	}
}

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
