package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintProducers(t *testing.T) {
	for _, producer := range []string{
		"INSERT INTO t(id,v) VALUES(1,1),(2,1)",
		"INSERT INTO t(id,v) SELECT id,1 FROM source",
		"INSERT INTO t(id) VALUES(1),(2)",
		"UPDATE t SET v=1",
		"UPDATE t SET v=source.v FROM source WHERE t.id=source.id",
		"MERGE INTO t USING source ON t.id=source.id WHEN NOT MATCHED THEN INSERT(id,v) VALUES(source.id,source.v)",
		"INSERT INTO t(id,v) VALUES(1,1),(2,1) ON CONFLICT(id) DO UPDATE SET v=excluded.v",
	} {
		t.Run(producer, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE source(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,1),(2,1)", 2))
				run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT DEFAULT 1,g INT GENERATED ALWAYS AS(v+10) STORED,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
				if producer[0] == 'U' || producer == "INSERT INTO t(id,v) VALUES(1,1),(2,1) ON CONFLICT(id) DO UPDATE SET v=excluded.v" {
					run(fixture.Exec("INSERT INTO t(id,v) VALUES(1,2),(2,3)", 2))
				}
				run(fixture.Exec("BEGIN", 0))
				count := int64(2)
				if producer[0] == 'M' && b.PG {
					count = 0
				}
				run(fixture.Prepared(fixture.Exec(producer, count)))
				run(fixture.Select("SELECT id,v,g FROM t ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(11)}, {int64(2), int64(1), int64(11)}}))
				run(fixture.Exec("DELETE FROM t USING source WHERE t.id=source.id AND source.id=2", 1))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
			})
		})
	}
	t.Run("returning-error-ownership", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1),(1) RETURNING v", fixture.DeferredUniqueConflict("t", "u", "v", "1"))
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		})
	})
	t.Run("nested-command", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
			run(fixture.Exec("CREATE FUNCTION deferred_insert(v INT) RETURNS INT LANGUAGE plpgsql AS $$ BEGIN INSERT INTO t VALUES(v); RETURN v; END $$", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Select("SELECT deferred_insert(1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Prepared(fixture.Select("SELECT deferred_insert($1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1))))
			run(fixture.ErrorDiagnostic("COMMIT", fixture.DeferredUniqueConflict("t", "u", "v", "1")))
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		})
	})
}

func TestReferenceDeferredConstraintImmediateChecks(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT pk PRIMARY KEY(v) INITIALLY DEFERRED,CONSTRAINT ck CHECK(v>0))", 0))
		nn := fixture.ForeignKeyError("23502", `null value in column "v" of relation "t" violates not-null constraint`, "Failing row contains (null).", "t", "")
		nn.Column = "v"
		ck := fixture.ForeignKeyError("23514", `new row for relation "t" violates check constraint "ck"`, "Failing row contains (0).", "t", "ck")
		for _, tc := range []struct {
			query string
			d     fixture.Diagnostic
		}{{"INSERT INTO t VALUES(NULL)", nn}, {"INSERT INTO t VALUES(0)", ck}} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic(tc.query, tc.d)))
			run(fixture.Exec("ROLLBACK", 0))
		}
		for _, name := range []string{"pk", "ck", "t_v_not_null"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS "+name+" IMMEDIATE", 0))
			if name != "pk" {
				run(fixture.ErrorDiagnostic("SET CONSTRAINTS "+name+" DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "`+name+`" is not deferrable`)))
			}
			run(fixture.Exec("ROLLBACK", 0))
		}
	})
}
