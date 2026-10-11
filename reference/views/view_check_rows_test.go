package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckBasic(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 10)", 0), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE a<b WITH CHECK OPTION", 0), fixture.Exec("INSERT INTO v VALUES(3,4)", 1), viewCheckError("INSERT INTO v VALUES(4,3)", "v", "4, 3"), viewCheckPairRows([]any{int64(3), int64(4)}), viewCheckError("INSERT INTO v VALUES(5,NULL)", "v", "5, null"), viewCheckPairRows([]any{int64(3), int64(4)}), fixture.Exec("INSERT INTO v(a) VALUES(9)", 1), viewCheckError("INSERT INTO v(a) VALUES(10)", "v", "10, 10"), viewCheckError("UPDATE v SET b=-5 WHERE a=3", "v", "3, -5"), viewCheckPairRows([]any{int64(3), int64(4)}, []any{int64(9), int64(10)}))
		q := "INSERT INTO v(a,b) VALUES($1,$2)"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, viewCheckError(q, "v", "20, 1", 20, 1))
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 1, 6, 7))
		run(fixture.Exec("INSERT INTO t VALUES(50,1)", 1), fixture.Select("SELECT a,b FROM v ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}, {int64(6), int64(7)}, {int64(9), int64(10)}}), fixture.Exec("DELETE FROM v", 3), viewCheckPairRows([]any{int64(50), int64(1)}))
	})
}

func TestReferenceViewCheckNested(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		// Indexed by top/middle/leaf modes: 0 NONE, 1 LOCAL, 2 CASCADED.
		// Bits 4/2/1 are independently specified checked top/middle/leaf predicates.
		sets := [27]int{0, 1, 1, 2, 3, 3, 3, 3, 3, 4, 5, 5, 6, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}
		clause := []string{"", " WITH LOCAL CHECK OPTION", " WITH CASCADED CHECK OPTION"}
		run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
		for top := range 3 {
			for middle := range 3 {
				for leaf := range 3 {
					index := top*9 + middle*3 + leaf
					t.Run(fmt.Sprintf("modes=%d%d%d", top, middle, leaf), func(t *testing.T) {
						run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
						run(fixture.Exec("CREATE VIEW leaf AS SELECT a,b,c FROM t WHERE c>0"+clause[leaf], 0), fixture.Exec("CREATE VIEW middle AS SELECT a,b,c FROM leaf WHERE b>0"+clause[middle], 0), fixture.Exec("CREATE VIEW top AS SELECT a,b,c FROM middle WHERE a>0"+clause[top], 0))
						q := "INSERT INTO top VALUES($1,$2,$3)"
						var e fixture.Executor = b.DB
						if held {
							e = fixture.ViewHold(t, p, b, q)
						}
						var expected [][]any
						for bad := range 8 {
							values := []any{int64(1), int64(1), int64(1)}
							for j, bit := range []int{4, 2, 1} {
								if bad&bit != 0 {
									values[j] = int64(-1)
								}
							}
							failure := bad & sets[index]
							if failure == 0 {
								fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 1, values...))
								expected = append(expected, values)
							} else {
								name := "top"
								if failure&1 != 0 {
									name = "leaf"
								} else if failure&2 != 0 {
									name = "middle"
								}
								fixture.RunStep(t, p.Ctx, e, b.Recorder, viewCheckError(q, name, fmt.Sprintf("%d, %d, %d", values...), values...))
							}
							run(viewWriteBag("SELECT a,b,c FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, expected))
						}
						run(fixture.Exec("DROP VIEW top,middle,leaf", 0), fixture.Exec("DELETE FROM t", int64(len(expected))))
					})
				}
			}
		}
		run(fixture.Exec("CREATE VIEW leaf AS SELECT a,b,c FROM t WHERE c>0", 0), fixture.Exec("CREATE VIEW top AS SELECT a,b,c FROM leaf WITH CASCADED CHECK OPTION", 0), viewCheckError("INSERT INTO top VALUES(1,1,-1)", "leaf", "1, 1, -1"), fixture.Exec("ALTER VIEW top SET(check_option=local)", 0), fixture.Exec("INSERT INTO top VALUES(1,1,-1)", 1), fixture.Exec("ALTER VIEW leaf SET(check_option=local)", 0), viewCheckError("INSERT INTO top VALUES(1,1,-2)", "leaf", "1, 1, -2"), fixture.Exec("ALTER VIEW top RESET(check_option)", 0), viewCheckError("INSERT INTO top VALUES(1,1,-3)", "leaf", "1, 1, -3"))
	})
}

func TestReferenceViewCheckNewRows(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(junk INT,a INT DEFAULT 2,b INT DEFAULT 10,g INT GENERATED ALWAYS AS(a+b) STORED,secret TEXT DEFAULT 'kept')", 0), fixture.Exec("CREATE VIEW v AS SELECT b AS y,a AS x FROM t WHERE a<b AND g>0 WITH CHECK OPTION", 0), fixture.Exec("INSERT INTO v(x) VALUES(3)", 1))
		q := "UPDATE v SET x=$1 RETURNING y,x"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		err := viewCheckError(q, "v", "null, 11, 10, 21, kept", 11)
		err.SelectRows = true
		fixture.RunStep(t, p.Ctx, e, b.Recorder, err)
		swap := viewCheckError("UPDATE v SET x=y,y=x WHERE x=3 RETURNING y,x", "v", "null, 10, 3, 13, kept")
		swap.SelectRows = true
		run(swap)
		run(fixture.Select("UPDATE v SET x=4,y=12 WHERE x=3 RETURNING y,x", []string{"y", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(4)}}), fixture.Select("UPDATE v SET x=99 WHERE x=999 RETURNING *", []string{"y", "x"}, []string{"INT4", "INT4"}, nil))
		run(fixture.Exec("ALTER TABLE t DROP COLUMN junk", 0), fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0), fixture.Exec("ALTER TABLE t ADD COLUMN a INT DEFAULT 99", 0), fixture.Exec("ALTER TABLE t ALTER COLUMN renamed SET DEFAULT 5", 0), fixture.Exec("INSERT INTO v(y) VALUES(20)", 1))
		run(fixture.Select("SELECT renamed,b,g,secret,a FROM t ORDER BY renamed", []string{"renamed", "b", "g", "secret", "a"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(4), int64(12), int64(16), "kept", int64(99)}, {int64(5), int64(20), int64(25), "kept", int64(99)}}))
		fail := viewCheckError(q, "v", "30, 12, 42, kept, 99", 30)
		fail.SelectRows = true
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fail)
		ok := fixture.Select(q, []string{"y", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(6)}, {int64(20), int64(6)}}, 6)
		ok.Bag = true
		fixture.RunStep(t, p.Ctx, e, b.Recorder, ok)
		run(fixture.Exec("CREATE TABLE src(y INT,delta INT)", 0), fixture.Exec("INSERT INTO src VALUES(12,1),(20,2)", 2))
		from := fixture.Select("UPDATE v SET x=v.x+src.delta FROM src WHERE v.y=src.y RETURNING v.y,v.x", []string{"y", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(7)}, {int64(20), int64(8)}})
		from.Bag = true
		run(from)
		run(fixture.Exec("UPDATE src SET delta=30 WHERE y=12", 1))
		badFrom := viewCheckError("UPDATE v SET x=v.x+src.delta FROM src WHERE v.y=src.y RETURNING v.y,v.x", "v", "37, 12, 49, kept, 99")
		badFrom.SelectRows = true
		run(badFrom)
		run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT b AS y,renamed AS x FROM t WHERE renamed<b", 0))
		unchecked := fixture.Select(q, []string{"y", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(99)}, {int64(20), int64(99)}}, 99)
		unchecked.Bag = true
		fixture.RunStep(t, p.Ctx, e, b.Recorder, unchecked)
	})
}
