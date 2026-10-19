package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateRowBoundaries(t *testing.T) {
	for _, prefix := range []string{
		"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET ",
		"INSERT INTO t VALUES(1,90,91) ON CONFLICT(id) DO UPDATE SET ",
	} {
		for _, set := range []string{"(a,b)=(t.b,t.a)", "id=t.id,(a,b)=(t.b,t.a)"} {
			for _, prepared := range []bool{false, true} {
				t.Run(prefix+set+map[bool]string{false: "/direct", true: "/prepare"}[prepared], func(t *testing.T) {
					updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,a INT,b INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10,20)", 1))
						q := prefix + set
						if b.PG {
							// lib/pq does not decode the MERGE command tag's affected count;
							// assert its exposed count and independently assert actual effects.
							count := int64(1)
							if prefix == "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET " {
								count = 0
							}
							s := fixture.Exec(q, count)
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
							run(updateRowSnapshot([][]any{{int64(1), int64(20), int64(10)}}))
						} else {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "row assignments are unsupported", Position: updateRowPosition(q, "(a,b)")})
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							}
							run(s)
							run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}}))
						}
					})
				})
			}
		}
	}
	t.Run("ordinary_default", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,a INT DEFAULT 7,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10,20)", 1))
			q := "UPDATE t SET (a,b)=ROW(DEFAULT,a)"
			if b.PG {
				run(fixture.Exec(q, 1))
				run(updateRowSnapshot([][]any{{int64(1), int64(7), int64(10)}}))
			} else {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "UPDATE DEFAULT is unsupported", Position: updateRowPosition(q, "DEFAULT")}))
				run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}}))
			}
		})
	})
	t.Run("generated_default", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT GENERATED ALWAYS AS (a+1) STORED)", 0))
			run(fixture.Exec("INSERT INTO t(a) VALUES(1)", 1))
			run(fixture.Exec("UPDATE t SET (a,b)=ROW(7,DEFAULT)", 1))
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
		})
	})
	for _, q := range []string{"UPDATE t SET (a,b)=(SELECT DEFAULT,1)", "SELECT ROW(DEFAULT,1)"} {
		t.Run("invalid_default/"+q, func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				updateRowSeed(t, p, b)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: updateRowPosition(q, "DEFAULT")}))
			})
		})
	}
}

func TestReferenceUpdateRowFeatureSmoke(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		run(fixture.Exec("UPDATE t SET (a,b)=(b,a)", 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(20), int64(10)}, {int64(2), int64(40), int64(30)}}))
		stmt, err := b.DB.PrepareContext(p.Ctx, "UPDATE t SET (a,b)=ROW($1,b+$2) WHERE id=1 RETURNING a,b")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			args []any
			want [][]any
		}{
			{[]any{7, 3}, [][]any{{int64(7), int64(13)}}},
			{[]any{nil, 4}, [][]any{{nil, int64(17)}}},
		} {
			rows, err := stmt.QueryContext(p.Ctx, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			fixture.UpdateCoreRetained(t, b.PG, rows, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.want, false)
		}
		run(fixture.Exec("CREATE TABLE s(id INT,x INT,y INT)", 0))
		run(fixture.Exec("INSERT INTO s VALUES(1,100,200),(2,300,400)", 2))
		q := "UPDATE t SET (a,b)=(SELECT x,y FROM s WHERE s.id=t.id)"
		run(fixture.Exec(q, 2))
		run(updateRowSnapshot([][]any{{int64(1), int64(100), int64(200)}, {int64(2), int64(300), int64(400)}}))
		run(fixture.Exec("DELETE FROM s WHERE id=1", 1))
		run(fixture.Exec(q+" WHERE id=1", 1))
		unchanged := updateRowSnapshot([][]any{{int64(1), nil, nil}, {int64(2), int64(300), int64(400)}})
		run(unchanged)
		run(fixture.Exec("INSERT INTO s VALUES(2,500,600)", 1))
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"}))
		run(unchanged)
	})
}
