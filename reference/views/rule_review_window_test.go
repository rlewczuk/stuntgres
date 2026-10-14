package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleReviewWindowScheduling(t *testing.T) {
	ruleReviewLifecycleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE review_window_src(id INT,g INT,v INT)", 0),
			fixture.Exec("CREATE TABLE review_window_log(id INT,g INT,v INT,rank BIGINT,total BIGINT)", 0),
			fixture.Exec("INSERT INTO review_window_src VALUES(1,0,10),(2,0,20),(3,0,30),(4,0,40)", 4),
			fixture.Exec("CREATE RULE window_audit AS ON UPDATE TO review_window_src DO INSERT INTO review_window_log SELECT NEW.id,NEW.g,NEW.v,row_number() OVER(PARTITION BY NEW.g%2 ORDER BY NEW.v),sum(NEW.v) OVER(PARTITION BY NEW.g%2 ORDER BY NEW.v ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)", 0),
		} {
			run(s)
		}
		q := "UPDATE review_window_src SET v=-v,g=g+id WHERE id<=$1"
		executor := fixture.Executor(conn)
		if held {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 4, 4))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 0, 0))
		run(fixture.Select("SELECT id,g,v,rank,total FROM review_window_log ORDER BY id", []string{"id", "g", "v", "rank", "total"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8"}, [][]any{
			{int64(1), int64(1), int64(-10), int64(2), int64(-40)},
			{int64(2), int64(2), int64(-20), int64(2), int64(-60)},
			{int64(3), int64(3), int64(-30), int64(1), int64(-30)},
			{int64(4), int64(4), int64(-40), int64(1), int64(-40)},
		}))
		run(fixture.Select("SELECT id,g,v FROM review_window_src ORDER BY id", []string{"id", "g", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{
			{int64(1), int64(1), int64(-10)}, {int64(2), int64(2), int64(-20)}, {int64(3), int64(3), int64(-30)}, {int64(4), int64(4), int64(-40)},
		}))
	})
}
