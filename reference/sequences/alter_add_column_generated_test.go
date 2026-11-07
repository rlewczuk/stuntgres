package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedStored(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.posts()
		g.postsRows(false)
		g.exec("INSERT INTO posts(title,description) VALUES('Second','more text')", 1)
		g.postsRows(true)
		g.exec("UPDATE posts SET title='New title' WHERE id=1", 1)
		g.run(fixture.Select("SELECT id,search,title_len FROM posts ORDER BY id", []string{"id", "search", "title_len"}, []string{"INT4", "TSVECTOR", "INT4"}, [][]any{{int64(1), "'first':3B 'new':1A 'post':4B 'title':2A", int64(9)}, {int64(2), "'more':2B 'second':1A 'text':3B", int64(6)}}))
		g.kinds("posts", []any{"search", "s"}, []any{"title_len", "s"})
		g.exec("CREATE TABLE nullable(a integer)", 0)
		g.exec("INSERT INTO nullable VALUES(NULL),(2)", 2)
		g.exec("ALTER TABLE nullable ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
		g.run(fixture.Select("SELECT a,b FROM nullable ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}, {int64(2), int64(3)}}))
		g.exec("UPDATE nullable SET a=NULL WHERE a=2", 1)
		g.run(fixture.Select("SELECT a,b FROM nullable", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}, {nil, nil}}))
	})
}

func TestReferenceAlterAddColumnGeneratedVirtual(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE TABLE g(id integer)", 0)
		g.exec("INSERT INTO g VALUES(1),(2)", 2)
		g.exec("ALTER TABLE g ADD explicit integer GENERATED ALWAYS AS(id*2) VIRTUAL", 0)
		g.exec("ALTER TABLE g ADD COLUMN omitted integer GENERATED ALWAYS AS(id*2)", 0)
		g.run(fixture.Select("SELECT * FROM g ORDER BY id", []string{"id", "explicit", "omitted"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2)}, {int64(2), int64(4), int64(4)}}))
		g.exec("INSERT INTO g(id) VALUES(3),(NULL)", 2)
		g.exec("UPDATE g SET id=4 WHERE id=2", 1)
		g.run(fixture.Select("SELECT * FROM g ORDER BY id NULLS LAST", []string{"id", "explicit", "omitted"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2)}, {int64(3), int64(6), int64(6)}, {int64(4), int64(8), int64(8)}, {nil, nil, nil}}))
		g.exec("ALTER TABLE g ADD s integer GENERATED ALWAYS AS(id+1) STORED", 0)
		g.kinds("g", []any{"explicit", "v"}, []any{"omitted", "v"}, []any{"s", "s"})
		g.exec("ALTER TABLE g ADD bad integer GENERATED ALWAYS AS(id/0) VIRTUAL", 0)
		g.exec("ALTER TABLE g ADD lazy integer GENERATED ALWAYS AS(id/0)", 0)
		g.run(fixture.Select("SELECT id FROM g ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(4)}, {nil}}))
		for _, col := range []string{"bad", "lazy"} {
			g.fail("SELECT "+col+" FROM g", addGeneratedDiagnostic("22012", "division by zero"), true, "execute")
		}
		g.run(fixture.Select("SELECT explicit,omitted,s FROM g WHERE id=3", []string{"explicit", "omitted", "s"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(6), int64(6), int64(4)}}))
	})
}
