package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

type addGeneratedCase struct {
	t          *testing.T
	p          *fixture.Pair
	b          fixture.Backend
	c          *sql.Conn
	prepared   bool
	statements []*sql.Stmt
}

// Backend failures are contained by t.Run: native red must not hide the oracle.
func addGeneratedRoutes(t *testing.T, body func(*addGeneratedCase)) {
	t.Helper()
	for _, route := range []struct {
		name     string
		prepared bool
	}{{"direct", false}, {"prepared", true}} {
		t.Run(route.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					g := &addGeneratedCase{t: t, p: p, b: b, c: c, prepared: route.prepared}
					defer g.closeStatements()
					body(g)
				})
			}
		})
	}
}

func (g *addGeneratedCase) run(s fixture.Step) {
	g.t.Helper()
	s.Prepared = g.prepared
	fixture.RunStep(g.t, g.p.Ctx, g.c, g.b.Recorder, s)
}

func (g *addGeneratedCase) exec(q string, n int64, args ...any) {
	g.t.Helper()
	g.run(fixture.Exec(q, n, args...))
}
func (g *addGeneratedCase) fail(q string, d fixture.Diagnostic, selectRows bool, stage string, args ...any) {
	g.t.Helper()
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.ErrorStage = selectRows, stage
	g.run(s)
}
func addGeneratedDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func addGeneratedPosition(q, token string) string {
	return fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}
func (g *addGeneratedCase) hold(q string) fixture.HeldStatement {
	g.t.Helper()
	s, err := g.c.PrepareContext(g.p.Ctx, q)
	if err != nil {
		g.t.Fatal(err)
	}
	g.statements = append(g.statements, s)
	return fixture.HeldStatement{Executor: g.c, Statement: s, SQL: q}
}

// Statements belong to the connection borrower, not the longer-lived test cleanup.
func (g *addGeneratedCase) closeStatements() {
	g.t.Helper()
	for _, s := range g.statements {
		if err := s.Close(); err != nil {
			g.t.Error(err)
		}
	}
	g.statements = nil
}

func (g *addGeneratedCase) releaseHeld(h fixture.HeldStatement) error {
	g.t.Helper()
	for i, s := range g.statements {
		if s == h.Statement {
			g.statements = append(g.statements[:i], g.statements[i+1:]...)
			return s.Close()
		}
	}
	g.t.Fatal("retained statement has no owning borrower")
	return nil
}
func (g *addGeneratedCase) held(h fixture.HeldStatement, s fixture.Step) {
	g.t.Helper()
	fixture.RunStep(g.t, g.p.Ctx, h, g.b.Recorder, s)
}
func (g *addGeneratedCase) kinds(table string, rows ...[]any) {
	g.t.Helper()
	g.run(fixture.Select("SELECT attname,attgenerated FROM pg_attribute WHERE attrelid=$1::regclass AND attgenerated<>'' AND NOT attisdropped ORDER BY attnum", []string{"attname", "attgenerated"}, []string{"NAME", "CHAR"}, rows, table))
}
func (g *addGeneratedCase) unchanged(rows [][]any) {
	g.t.Helper()
	g.run(fixture.Select("SELECT * FROM g ORDER BY a", []string{"a"}, []string{"INT4"}, rows))
	g.run(fixture.Select("SELECT adnum FROM pg_attrdef WHERE adrelid='g'::regclass", []string{"adnum"}, []string{"INT2"}, nil))
	g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g'", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, nil))
	g.kinds("g")
}
func (g *addGeneratedCase) notices(got, want []fixture.Diagnostic) {
	g.t.Helper()
	if !reflect.DeepEqual(got, want) {
		g.t.Fatalf("notices=%#v want=%#v", got, want)
	}
}

const addGeneratedSearch = "setweight(to_tsvector('simple'::regconfig, title), 'A') || setweight(to_tsvector('simple'::regconfig, description), 'B')"

func (g *addGeneratedCase) posts() {
	g.exec("CREATE TABLE posts(id serial PRIMARY KEY,title text NOT NULL,description text NOT NULL)", 0)
	g.exec("INSERT INTO posts(title,description) VALUES('Hello world','first post')", 1)
	g.exec("ALTER TABLE posts ADD search tsvector GENERATED ALWAYS AS ("+addGeneratedSearch+") STORED", 0)
	g.exec("ALTER TABLE posts ADD COLUMN title_len integer GENERATED ALWAYS AS (length(title)) STORED", 0)
}
func (g *addGeneratedCase) postsRows(second bool) {
	rows := [][]any{{int64(1), "'first':3B 'hello':1A 'post':4B 'world':2A", int64(11)}}
	if second {
		rows = append(rows, []any{int64(2), "'more':2B 'second':1A 'text':3B", int64(6)})
	}
	g.run(fixture.Select("SELECT id,search,title_len FROM posts ORDER BY id", []string{"id", "search", "title_len"}, []string{"INT4", "TSVECTOR", "INT4"}, rows))
}
func (g *addGeneratedCase) gin() {
	g.exec("CREATE INDEX idx_posts_search_gin ON posts USING gin(search)", 0)
	g.run(fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='posts' AND indexname='idx_posts_search_gin'", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{"idx_posts_search_gin", "CREATE INDEX idx_posts_search_gin ON public.posts USING gin (search)"}}))
	g.run(fixture.Select("SELECT am.amname,i.indrelid='posts'::regclass AS owner,i.indisvalid,i.indisready,a.attname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0] WHERE c.relname='idx_posts_search_gin'", []string{"amname", "owner", "indisvalid", "indisready", "attname"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "NAME"}, [][]any{{"gin", true, true, true, "search"}}))
}
