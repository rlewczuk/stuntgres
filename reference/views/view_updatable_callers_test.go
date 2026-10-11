package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableCallers(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,a INT,b INT,hidden TEXT DEFAULT 'keep')", 0), fixture.Exec("CREATE VIEW v AS SELECT b AS y,id AS key,a AS x FROM t", 0),
			fixture.Exec("INSERT INTO v(key,x,y) VALUES(1,10,20),(2,30,40)", 2), fixture.Exec("INSERT INTO v(key,x) SELECT 3,50", 1), fixture.Exec("INSERT INTO v(key,x) SELECT 4,60 WHERE FALSE", 0),
			fixture.Exec("INSERT INTO v(key,x) VALUES(1,99) ON CONFLICT DO NOTHING", 0),
			fixture.Exec("CREATE TABLE src(id INT,n INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,7),(2,8)", 2),
			fixture.Select("UPDATE v AS dst SET x=src.n,y=dst.x FROM src WHERE dst.key=src.id RETURNING WITH(OLD AS before,NEW AS after) before.key,before.x,after.x,after.y,src.n", []string{"key", "x", "x", "y", "n"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(7), int64(10), int64(7)}, {int64(2), int64(30), int64(8), int64(30), int64(8)}}),
			fixture.Exec("UPDATE v SET x=(SELECT n FROM src WHERE id=1) WHERE key=3", 1),
			fixture.Select("DELETE FROM v AS dst USING src WHERE dst.key=src.id RETURNING dst.key,src.n", []string{"key", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}}),
			fixture.Select("SELECT id,a,b,hidden FROM t", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(7), nil, "keep"}}),
			fixture.Exec("INSERT INTO t(id,a,b) VALUES(4,40,50)", 1), fixture.Exec("UPDATE t SET a=b,b=a WHERE id=4", 1), fixture.Exec("DELETE FROM t WHERE id=4", 1),
			fixture.Exec("CREATE TABLE arrays(id INT,items INT[])", 0), fixture.Exec("CREATE VIEW av AS SELECT items AS vals,id AS key FROM arrays", 0), fixture.Exec("INSERT INTO av(key,vals) VALUES(1,ARRAY[10,20])", 1), fixture.Exec("UPDATE av SET vals[1]=30,vals[2]=40 WHERE key=1", 1),
			fixture.Select("SELECT vals[1] AS first,vals[2] AS second FROM av", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(30), int64(40)}}),
			fixture.Exec("CREATE TABLE generated(id INT GENERATED ALWAYS AS IDENTITY,a INT,g INT GENERATED ALWAYS AS(a+1) STORED)", 0), fixture.Exec("CREATE VIEW gv AS SELECT id AS key,a AS x,g AS calc FROM generated", 0), fixture.Exec("INSERT INTO gv(x) VALUES(5)", 1),
			fixture.Select("SELECT key,x,calc FROM gv", []string{"key", "x", "calc"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(5), int64(6)}}), fixture.Exec("UPDATE gv SET x=7", 1),
			fixture.Select("SELECT id,a,g FROM generated", []string{"id", "a", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(8)}}), fixture.Exec("INSERT INTO gv(key,x) OVERRIDING SYSTEM VALUE VALUES(10,9)", 1),
		} {
			s.Bag = s.SelectRows
			run(s)
		}
	})
}
