package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedFeatureSmoke(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		var notices []fixture.Diagnostic
		_, conn, rec := fixture.NoticeReferencePool(g.t, g.p, g.b.PG, &notices)
		n := *g
		n.c = conn
		n.b.Recorder = rec
		g = &n
		g.exec("CREATE TABLE posts(id serial PRIMARY KEY,title text NOT NULL,description text NOT NULL)", 0)
		g.exec("INSERT INTO posts(title,description) VALUES('Hello world','first post')", 1)
		g.exec("ALTER TABLE posts ADD search tsvector GENERATED ALWAYS AS("+addGeneratedSearch+") STORED, ADD title_len integer GENERATED ALWAYS AS(length(title)) STORED", 0)
		g.postsRows(false)
		g.exec("ALTER TABLE posts ADD doubled integer GENERATED ALWAYS AS(id*2) VIRTUAL, ADD omitted integer GENERATED ALWAYS AS(id+1)", 0)
		g.kinds("posts", []any{"search", "s"}, []any{"title_len", "s"}, []any{"doubled", "v"}, []any{"omitted", "v"})
		g.exec("INSERT INTO posts(title,description) VALUES('Second','more text')", 1)
		g.postsRows(true)
		g.exec("UPDATE posts SET title='New title' WHERE id=1", 1)
		g.run(fixture.Select("SELECT id,search,title_len,doubled,omitted FROM posts WHERE id=1", []string{"id", "search", "title_len", "doubled", "omitted"}, []string{"INT4", "TSVECTOR", "INT4", "INT4", "INT4"}, [][]any{{int64(1), "'first':3B 'new':1A 'post':4B 'title':2A", int64(9), int64(2), int64(2)}}))
		g.fail("ALTER TABLE posts ADD bad integer GENERATED ALWAYS AS(id/0) STORED", addGeneratedDiagnostic("22012", "division by zero"), false, "execute")
		g.run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='posts'::regclass AND attname='bad' AND NOT attisdropped", []string{"attname"}, []string{"NAME"}, nil))
		notices = nil
		g.exec("ALTER TABLE posts ADD IF NOT EXISTS search nonexistent_type GENERATED ALWAYS AS(missing/0) STORED", 0)
		g.notices(notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "search" of relation "posts" already exists, skipping`}})
		g.exec("ALTER TABLE posts DROP search", 0)
		g.exec("ALTER TABLE posts ADD COLUMN search tsvector GENERATED ALWAYS AS("+addGeneratedSearch+") STORED", 0)
		g.gin()
		read := g.hold("SELECT id,search,title_len,doubled,omitted FROM posts WHERE id=2")
		want := fixture.Select(read.SQL, []string{"id", "search", "title_len", "doubled", "omitted"}, []string{"INT4", "TSVECTOR", "INT4", "INT4", "INT4"}, [][]any{{int64(2), "'more':2B 'second':1A 'text':3B", int64(6), int64(4), int64(3)}})
		g.held(read, want)
		g.run(fixture.Select("SELECT column_name,is_generated FROM information_schema.columns WHERE table_name='posts' AND is_generated='ALWAYS' ORDER BY column_name", []string{"column_name", "is_generated"}, []string{"NAME", "VARCHAR"}, [][]any{{"doubled", "ALWAYS"}, {"omitted", "ALWAYS"}, {"search", "ALWAYS"}, {"title_len", "ALWAYS"}}))
		if !g.b.PG {
			dump := fixture.CheckDump(g.t, g.p)
			if err := stuntgres.Reset(g.p.Ctx, g.p.Native); err != nil {
				g.t.Fatal(err)
			}
			if err := stuntgres.Restore(g.p.Ctx, g.p.Native, bytes.NewReader(dump)); err != nil {
				g.t.Fatal(err)
			}
			g.run(want)
		}
		g.exec("INSERT INTO posts(title,description) VALUES('Third','new post')", 1)
		g.run(fixture.Select("SELECT id,search,title_len,doubled,omitted FROM posts WHERE id=3", []string{"id", "search", "title_len", "doubled", "omitted"}, []string{"INT4", "TSVECTOR", "INT4", "INT4", "INT4"}, [][]any{{int64(3), "'new':2B 'post':3B 'third':1A", int64(5), int64(6), int64(4)}}))
	})
}
