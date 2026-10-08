//go:build reference

package reference_test

import "testing"

func TestReferenceAlterTypeDependentCapabilityBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, create, seed, dependency, alter, state string }{
		{"check", "CREATE TABLE t(c INT CONSTRAINT ck CHECK(c>0))", "INSERT INTO t VALUES(1)", "", "ALTER TABLE t ALTER c TYPE INT USING -c", "23514"},
		{"unique", "CREATE TABLE t(c INT UNIQUE)", "INSERT INTO t VALUES(1),(2)", "", "ALTER TABLE t ALTER c TYPE INT USING 0", "23505"},
		{"expression-index", "CREATE TABLE t(c INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t((c+1))", "ALTER TABLE t ALTER c TYPE TEXT USING c::text", "42883"},
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
				run(referenceError(tc.alter, tc.state))
				rows := [][]any{{int64(1)}}
				if tc.name == "unique" {
					rows = append(rows, []any{int64(2)})
				}
				run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, rows))
				if tc.name == "check" {
					run(referenceError("INSERT INTO t VALUES(-1)", "23514"))
				}
				if tc.name == "unique" {
					run(referenceError("INSERT INTO t VALUES(1)", "23505"))
				}
			})
			t.Run("native", func(t *testing.T) {
				if tc.dependency != "" {
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(tc.create, 0))
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceError(tc.dependency, "0A000"))
				} else {
					referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceError(tc.create, "0A000"))
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
		run(referenceExec("ALTER TABLE parent ALTER c TYPE BIGINT", 0))
		run(referenceSelect("SELECT c FROM parent", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceError("INSERT INTO child VALUES(2)", "23503"))
		run(referenceExec("INSERT INTO child VALUES(1)", 1))
	})
	t.Run("native", func(t *testing.T) {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceError("CREATE TABLE parent(c INT PRIMARY KEY)", "0A000"))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE parent(c INT)", 0))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceError("CREATE TABLE child(c INT REFERENCES parent(c))", "0A000"))
	})
}
