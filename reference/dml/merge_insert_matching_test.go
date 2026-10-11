package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func mergeInsertScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) { body(t, p, backend, prepared) })
			}
		})
	}
}

func mergeInsertAffected(t *testing.T, b fixture.Backend, result sql.Result, err error, inserted int64) {
	t.Helper()
	if b.PG {
		inserted = 0
	}
	fixture.InsertSelectAffected(t, result, err, inserted)
}

func TestReferenceMergeInsertMatching(t *testing.T) {
	for _, tc := range []struct {
		name, target, source, on string
		count                    int64
		rows                     [][]any
	}{
		{"equality", "(1,'old')", "(1,'one'),(2,'two'),(3,'three')", "t.id=s.id", 2, [][]any{{int64(1), "old"}, {int64(2), "two"}, {int64(3), "three"}}},
		{"nullEquality", "(NULL,'old')", "(NULL::integer,'new')", "t.id=s.id", 1, [][]any{{nil, "old"}, {nil, "new"}}},
		{"false", "(1,'old')", "(2,'two'),(3,'three')", "FALSE", 2, [][]any{{int64(1), "old"}, {int64(2), "two"}, {int64(3), "three"}}},
		{"unknown", "(1,'old')", "(2,'two')", "NULL::boolean", 1, [][]any{{int64(1), "old"}, {int64(2), "two"}}},
		{"true", "(1,'old')", "(2,'two'),(3,'three')", "TRUE", 0, [][]any{{int64(1), "old"}}},
		{"emptyTargetTrue", "", "(2,'two'),(3,'three')", "TRUE", 2, [][]any{{int64(2), "two"}, {int64(3), "three"}}},
		{"oneSourceManyTargets", "(1,'a'),(1,'b')", "(1,'new')", "t.id=s.id", 0, [][]any{{int64(1), "a"}, {int64(1), "b"}}},
		{"manySourcesOneTarget", "(1,'old')", "(1,'a'),(1,'b')", "t.id=s.id", 0, [][]any{{int64(1), "old"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeInsertRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
				if tc.target != "" {
					run(fixture.Exec("INSERT INTO t VALUES"+tc.target, int64(len(tc.rows))-tc.count))
				}
				run(fixture.Exec("MERGE INTO t USING (VALUES"+tc.source+") s(id,v) ON "+tc.on+" WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", tc.count))
				rows := fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows)
				rows.Bag = true
				run(rows)
			})
		})
	}
}

func TestReferenceMergeInsertConditions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeInsertRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v TEXT DEFAULT 'tail')", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(2),(3)) s(id) ON t.id=s.id WHEN NOT MATCHED BY TARGET AND s.id=2 THEN INSERT VALUES(s.id,'first') WHEN NOT MATCHED THEN INSERT VALUES(s.id,'fallback')", 2))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "first"}, {int64(3), "fallback"}}))
		for _, condition := range []string{"FALSE", "NULL::boolean"} {
			run(fixture.Exec("MERGE INTO t USING (VALUES(1),(4)) s(id) ON t.id=s.id WHEN NOT MATCHED AND "+condition+" THEN INSERT VALUES(s.id)", 0))
		}
		run(fixture.Exec("MERGE INTO t USING (SELECT 5 AS id WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", 0))
		run(fixture.Exec("CREATE TABLE skipped_source(id INT)", 0))
		run(fixture.Exec("INSERT INTO skipped_source VALUES(0)", 1))
		run(fixture.Exec("MERGE INTO t USING skipped_source s ON FALSE WHEN NOT MATCHED AND NULL::boolean THEN INSERT VALUES(10/s.id) WHEN NOT MATCHED AND FALSE THEN INSERT VALUES(10/s.id) WHEN NOT MATCHED THEN INSERT VALUES(4)", 1))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "first"}, {int64(3), "fallback"}, {int64(4), "tail"}}))
		run(fixture.Exec("CREATE TABLE active_target(id INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO active_target VALUES(1,FALSE)", 1))
		run(fixture.Exec("MERGE INTO active_target t USING (VALUES(1)) s(id) ON t.id=s.id AND t.active WHEN NOT MATCHED THEN INSERT VALUES(s.id,TRUE)", 1))
		run(fixture.Select("SELECT * FROM active_target ORDER BY active", []string{"id", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), false}, {int64(1), true}}))
	})
}

func TestReferenceMergeInsertMappingsDefaults(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeInsertRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT DEFAULT 8,v TEXT DEFAULT 'tail',flag BOOL DEFAULT TRUE)", 0))
		for _, q := range []string{
			"MERGE INTO t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
			"MERGE INTO t USING (VALUES(2)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT(v,id) VALUES('two',s.id)",
			"MERGE INTO t USING (VALUES(3)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id,NULL,DEFAULT)",
			"MERGE INTO t USING (VALUES(4)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(DEFAULT,DEFAULT,DEFAULT)",
			"MERGE INTO t USING (VALUES(5)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES",
		} {
			run(fixture.Exec(q, 1))
		}
		run(fixture.Exec("MERGE INTO t USING (VALUES(6),(7)) s(id) ON FALSE WHEN NOT MATCHED AND s.id=6 THEN INSERT(id) VALUES(s.id) WHEN NOT MATCHED THEN INSERT(v,id) VALUES('seven',s.id)", 2))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "tail", true}, {int64(2), "two", true}, {int64(3), nil, true}, {int64(6), "tail", true}, {int64(7), "seven", true}, {int64(8), "tail", true}, {int64(8), "tail", true}}))
	})
}

func TestReferenceMergeInsertCardinality(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeInsertRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("MERGE INTO t USING (VALUES(7),(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 2))
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
		run(fixture.Exec("CREATE TABLE u(id INT NOT NULL CONSTRAINT u_pkey UNIQUE)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(99)", 1))
		s := fixture.ImplicitError("MERGE INTO u USING (VALUES(7),(7)) s(id) ON u.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "u_pkey"`, Detail: "Key (id)=(7) already exists.", Schema: "public", Table: "u", Constraint: "u_pkey"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Select("SELECT * FROM u", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	})
}

func TestReferenceMergeInsertSources(t *testing.T) {
	for _, source := range []string{"s", "v", "(SELECT id,v FROM s) q", "(VALUES(1,'one'),(2,'two')) q(id,v)", "s JOIN keys ON s.id=keys.k"} {
		t.Run(source, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeInsertRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
				run(fixture.Exec("CREATE TABLE s(id INT,v TEXT)", 0))
				run(fixture.Exec("INSERT INTO s VALUES(1,'one'),(2,'two')", 2))
				run(fixture.Exec("CREATE VIEW v AS SELECT * FROM s", 0))
				run(fixture.Exec("CREATE TABLE keys(k INT)", 0))
				run(fixture.Exec("INSERT INTO keys VALUES(1),(2)", 2))
				qualifier := "q"
				if source == "s" || source == "s JOIN keys ON s.id=keys.k" {
					qualifier = "s"
				}
				if source == "v" {
					qualifier = "v"
				}
				run(fixture.Exec("MERGE INTO t USING "+source+" ON t.id="+qualifier+".id WHEN NOT MATCHED THEN INSERT VALUES("+qualifier+".id,"+qualifier+".v)", 2))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
				run(fixture.Exec("MERGE INTO t a USING t b ON a.id=b.id WHEN NOT MATCHED THEN INSERT VALUES(b.id,b.v)", 0))
			})
		})
	}
}
