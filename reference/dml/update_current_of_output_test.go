package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCurrentOfExistingFromReturningComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("CREATE TABLE s(label TEXT)", 0))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2 FOR UPDATE", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				q := "UPDATE t AS d SET v=x.label FROM s AS x WHERE CURRENT OF c RETURNING d.id,old.v AS before_v,new.v AS after_v,x.label"
				s := fixture.Select(q, []string{"id", "before_v", "after_v", "label"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, nil)
				s.Prepared = prepared
				run(s)
				run(fixture.Exec("INSERT INTO s VALUES('same'),('same')", 2))
				s.Rows = [][]any{{int64(2), "two", "same", "same"}}
				run(s)
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "same"}, {int64(3), "three"}}))
				run(fixture.Exec("DELETE FROM s", 2))
				// Independent candidate: CURRENT OF demand occurs even with no FROM pairs.
				for _, name := range []string{"missing", "before", "wrong"} {
					if name == "before" {
						run(fixture.Exec("DECLARE before CURSOR FOR SELECT id FROM t", 0))
					}
					if name == "wrong" {
						run(fixture.Exec("DECLARE wrong CURSOR FOR SELECT label FROM s", 0))
					}
					code, msg := "34000", `cursor "missing" does not exist`
					if name == "before" {
						code, msg = "24000", `cursor "before" is not positioned on a row`
					}
					if name == "wrong" {
						code, msg = "24000", `cursor "wrong" is not a simply updatable scan of table "t"`
					}
					currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v='bad' FROM s WHERE CURRENT OF "+name, code, msg, prepared))
				}
			})
		})
	}
	for _, target := range []string{"t", "visible"} {
		t.Run("view_target/"+target, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("CREATE VIEW visible AS SELECT id,v FROM t", 0))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM visible WHERE id=1", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				q := "UPDATE " + target + " SET v='view' WHERE CURRENT OF c RETURNING id,v"
				if target == "visible" {
					// Rewrite rejects a CURRENT OF view target; a transparent cursor view above
					// remains eligible when the mutation target is the physical base table.
					for _, prepared := range []bool{false, true} {
						s := currentOfError(q, "0A000", "WHERE CURRENT OF on a view is not implemented", prepared)
						s.SelectRows = true
						if prepared {
							s.ErrorStage = "prepare"
						}
						currentOfFailure(t, p, b, c, s)
						run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
					}
				} else {
					run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "view"}}))
					run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "view"}, {int64(2), "two"}, {int64(3), "three"}}))
				}
			})
		})
	}
	t.Run("losing_pair_demand", func(t *testing.T) {
		updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
			run(fixture.Exec("CREATE TABLE s(n INT)", 0))
			run(fixture.Exec("INSERT INTO s VALUES(2),(0)", 2))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			// SET is demanded for every join pair, including the unchosen duplicate.
			currentOfFailure(t, p, b, c, currentOfError("UPDATE t SET v=10/s.n FROM s WHERE CURRENT OF c", "22012", "division by zero", false))
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		})
	})
}

func updateCurrentOfConstraintFailures(t *testing.T, prepared bool) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v INT,CONSTRAINT positive CHECK(id>0),CONSTRAINT key UNIQUE(id))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
		run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		for _, tc := range []struct {
			set string
			d   fixture.Diagnostic
		}{
			{"id=NULL", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 10).", Schema: "public", Table: "t", Column: "id"}},
			{"id=0", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (0, 10).", Schema: "public", Table: "t", Constraint: "positive"}},
			{"id=2", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "key"`, Detail: "Key (id)=(2) already exists.", Schema: "public", Table: "t", Constraint: "key"}},
			{"v=v/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
			{"v=2147483647+v", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		} {
			s := fixture.ImplicitError("UPDATE t SET "+tc.set+" WHERE CURRENT OF c", tc.d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			currentOfFailure(t, p, b, c, s)
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		}
		run(fixture.Exec("UPDATE t SET v=v+1 WHERE CURRENT OF c", 1))
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}))
	})
}
