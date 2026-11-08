package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// ALTER TABLE IF EXISTS carries the statement-level missing_ok flag: every
// ordinary action keeps the effect it has without the flag on an existing
// relation, and a missing relation is skipped with one NOTICE before any
// action is examined. Every expected catalog row and diagnostic here is the
// pinned PostgreSQL 18.6 oracle capture, never native output.
type atieNames func(string) string

type atieRoute struct {
	name, suffix string
	prepared     bool
}

var atieRoutes = []atieRoute{
	{name: "direct"},
	{name: "prepared", suffix: "_p", prepared: true},
}

// atieSession groups one route's notice-capable session: notices, prepared
// steps and catalog probes all observe the same connection.
type atieSession struct {
	p        *fixture.Pair
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	name     atieNames
	notices  *[]fixture.Diagnostic
}

func (s atieSession) run(t *testing.T, step fixture.Step) {
	t.Helper()
	if s.prepared {
		step = fixture.Prepared(step)
	}
	fixture.RunStep(t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s atieSession) exec(t *testing.T, query string, affected int64) {
	t.Helper()
	s.run(t, fixture.Exec(query, affected))
}

// ok runs a statement that must apply without emitting a notice.
func (s atieSession) ok(t *testing.T, query string) {
	t.Helper()
	s.exec(t, query, 0)
	s.noNotices(t)
}

func (s atieSession) noNotices(t *testing.T) {
	t.Helper()
	if len(*s.notices) != 0 {
		t.Fatalf("unexpected notices %#v", *s.notices)
	}
	*s.notices = nil
}

// wantNotice asserts the single missing-relation skip NOTICE and clears it.
func (s atieSession) wantNotice(t *testing.T, name string) {
	t.Helper()
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "` + name + `" does not exist, skipping`}
	if len(*s.notices) != 1 || (*s.notices)[0] != want {
		t.Fatalf("notices %#v, want one %#v", *s.notices, want)
	}
	*s.notices = nil
}

// expectMissing proves the skip changed nothing: each name neither resolves nor
// appears in the relation catalog.
func (s atieSession) expectMissing(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		s.run(t, atieAbsent(name))
		s.run(t, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='"+name+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

// atieScenario runs one body per backend.
func atieScenario(t *testing.T, body func(t *testing.T, p *fixture.Pair, b fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

// atieForEachRoute runs one backend body for each route; route-suffixed names
// let both routes share one schema.
func atieForEachRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, body func(t *testing.T, s atieSession)) {
	t.Helper()
	for _, route := range atieRoutes {
		t.Run(route.name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, atieSession{
				p: p, conn: conn, recorder: recorder, prepared: route.prepared,
				name:    func(base string) string { return base + route.suffix },
				notices: &notices,
			})
		})
	}
}

func atieAttributes(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT attname,format_type(atttypid,atttypmod),attnotnull,atthasdef FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "format_type", "attnotnull", "atthasdef"}, []string{"NAME", "TEXT", "BOOL", "BOOL"}, rows, table)
}

// atieLayout reports every attnum slot, including dropped placeholders, so a
// DROP COLUMN witness proves the physical layout the server publishes.
func atieLayout(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT attnum,attname,attisdropped FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "attisdropped"}, []string{"INT2", "NAME", "BOOL"}, rows, table)
}

func atieDefaults(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1::regclass ORDER BY adnum", []string{"pg_get_expr"}, []string{"TEXT"}, rows, table)
}

func atieConstraints(table, kinds string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ("+kinds+") ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, rows, table)
}

func atieAbsent(name string) fixture.Step {
	return fixture.Select("SELECT to_regclass('"+name+"') IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{true}})
}

// TestReferenceAlterTableIfExistsActions applies every implemented ordinary
// action through the relation-level IF EXISTS spelling and asserts the same
// catalog state and enforcement the plain form publishes.
func TestReferenceAlterTableIfExistsActions(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			parent := s.name("atie_parent")
			table := s.name("atie_act")
			s.ok(t, "CREATE TABLE "+parent+"(pid INT PRIMARY KEY)")
			s.exec(t, "INSERT INTO "+parent+" VALUES (1)", 1)
			s.ok(t, "CREATE TABLE "+table+"(id INT, v INT, gone INT)")
			s.exec(t, "INSERT INTO "+table+" VALUES (1,10,100),(2,20,200)", 2)

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD COLUMN a text")
			s.run(t, atieAttributes(table,
				[]any{"id", "integer", false, false},
				[]any{"v", "integer", false, false},
				[]any{"gone", "integer", false, false},
				[]any{"a", "text", false, false},
			))

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD COLUMN b int DEFAULT 7")
			s.run(t, atieDefaults(table, [][]any{{"7"}}))
			s.run(t, fixture.Select("SELECT id,b FROM "+table+" ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}}))
			s.exec(t, "INSERT INTO "+table+"(id) VALUES (3)", 1)
			s.run(t, fixture.Select("SELECT b FROM "+table+" WHERE id=3", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}}))

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD COLUMN c int REFERENCES "+parent+"(pid)")
			s.run(t, atieConstraints(table, "'f'", []any{table + "_c_fkey", "f", "FOREIGN KEY (c) REFERENCES " + parent + "(pid)"}))
			s.exec(t, "INSERT INTO "+table+"(id,c) VALUES (4,1)", 1)
			s.run(t, fixture.ImplicitError("INSERT INTO "+table+"(id,c) VALUES (5,99)", fixture.ForeignKeyChild(table, table+"_c_fkey", "Key (c)=(99) is not present in table \""+parent+"\".")))

			// The standalone AlterTableDefault route.
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN v SET DEFAULT 7")
			s.run(t, atieDefaults(table, [][]any{{"7"}, {"7"}}))
			s.exec(t, "INSERT INTO "+table+"(id) VALUES (6)", 1)
			s.run(t, fixture.Select("SELECT v FROM "+table+" WHERE id=6", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN v DROP DEFAULT")
			s.run(t, atieDefaults(table, [][]any{{"7"}}))

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN v TYPE bigint")
			s.run(t, fixture.Select("SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid=$1::regclass AND attname='v'", []string{"format_type"}, []string{"TEXT"}, [][]any{{"bigint"}}, table))
			s.run(t, fixture.Select("SELECT id,v FROM "+table+" WHERE id IN (1,2) ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))

			s.run(t, fixture.ImplicitError("ALTER TABLE IF EXISTS "+table+" ALTER COLUMN a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "` + table + `" contains null values`, Schema: "public", Table: table, Column: "a"}))
			s.exec(t, "UPDATE "+table+" SET a='x'", 5)
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN a SET NOT NULL")
			s.run(t, fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}, table))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN a DROP NOT NULL")
			s.run(t, fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}, table))

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP COLUMN gone")
			s.run(t, atieLayout(table,
				[]any{int64(1), "id", false},
				[]any{int64(2), "v", false},
				[]any{int64(3), "........pg.dropped.3........", true},
				[]any{int64(4), "a", false},
				[]any{int64(5), "b", false},
				[]any{int64(6), "c", false},
			))
			s.run(t, atieAttributes(table,
				[]any{"id", "integer", false, false},
				[]any{"v", "bigint", false, false},
				[]any{"a", "text", false, false},
				[]any{"b", "integer", false, true},
				[]any{"c", "integer", false, false},
			))

			uq := s.name("atie_uq")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD CONSTRAINT "+uq+" UNIQUE(id)")
			s.run(t, atieConstraints(table, "'u'", []any{uq, "u", "UNIQUE (id)"}))
			s.run(t, fixture.ImplicitError("INSERT INTO "+table+"(id) VALUES (1)", fixture.UniqueReferenceDuplicate(table, uq, "Key (id)=(1) already exists.")))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP CONSTRAINT "+uq)
			s.run(t, atieConstraints(table, "'u'"))

			ck := s.name("atie_ck")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD CONSTRAINT "+ck+" CHECK (v > 0)")
			s.run(t, atieConstraints(table, "'c'", []any{ck, "c", "CHECK ((v > 0))"}))
			s.run(t, fixture.ImplicitError("INSERT INTO "+table+"(id,v,a,b,c) VALUES (7,-1,NULL,NULL,NULL)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + ck + `"`, Detail: "Failing row contains (7, -1, null, null, null).", Schema: "public", Table: table, Constraint: ck}))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP CONSTRAINT "+ck)

			nn := s.name("atie_nn")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD CONSTRAINT "+nn+" NOT NULL b")
			s.run(t, atieConstraints(table, "'n'", []any{nn, "n", "NOT NULL b"}))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP CONSTRAINT "+nn)

			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP CONSTRAINT "+table+"_c_fkey")
			fk := s.name("atie_fk")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD CONSTRAINT "+fk+" FOREIGN KEY (c) REFERENCES "+parent+"(pid)")
			s.run(t, atieConstraints(table, "'f'", []any{fk, "f", "FOREIGN KEY (c) REFERENCES " + parent + "(pid)"}))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP CONSTRAINT "+fk)

			keyed := s.name("atie_pk")
			keyedName := s.name("atie_pk_pkey")
			s.ok(t, "CREATE TABLE "+keyed+"(id INT)")
			s.ok(t, "ALTER TABLE IF EXISTS "+keyed+" ADD CONSTRAINT "+keyedName+" PRIMARY KEY(id)")
			s.run(t, atieConstraints(keyed, "'p'", []any{keyedName, "p", "PRIMARY KEY (id)"}))
		})
	})
}

// TestReferenceAlterTableIfExistsDefault proves the standalone one-default AST
// carries the IF EXISTS marker without changing inheritance semantics.
func TestReferenceAlterTableIfExistsDefault(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			parent := s.name("atie_def_parent")
			child := s.name("atie_def_child")
			s.ok(t, "CREATE TABLE "+parent+"(id INT, v INT)")
			s.ok(t, "CREATE TABLE "+child+"() INHERITS ("+parent+")")

			s.ok(t, "ALTER TABLE IF EXISTS "+parent+" ALTER v SET DEFAULT 7")
			s.run(t, atieDefaults(parent, [][]any{{"7"}}))
			s.run(t, atieDefaults(child, [][]any{{"7"}}))

			s.ok(t, "ALTER TABLE IF EXISTS ONLY "+parent+" ALTER v SET DEFAULT 9")
			s.run(t, atieDefaults(parent, [][]any{{"9"}}))
			s.run(t, atieDefaults(child, [][]any{{"7"}}))

			s.exec(t, "INSERT INTO "+parent+"(id) VALUES (1)", 1)
			s.exec(t, "INSERT INTO "+child+"(id) VALUES (2)", 1)
			s.run(t, fixture.Select("SELECT id,v FROM ONLY "+parent+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
			s.run(t, fixture.Select("SELECT id,v FROM ONLY "+child+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}}))

			s.ok(t, "ALTER TABLE IF EXISTS "+parent+" ALTER v DROP DEFAULT")
			s.run(t, atieDefaults(parent, nil))
			s.run(t, atieDefaults(child, nil))
			s.exec(t, "INSERT INTO "+parent+"(id) VALUES (3)", 1)
			s.exec(t, "INSERT INTO "+child+"(id) VALUES (4)", 1)
			s.run(t, fixture.Select("SELECT id,v FROM ONLY "+parent+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(3), nil}}))
			s.run(t, fixture.Select("SELECT id,v FROM ONLY "+child+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}, {int64(4), nil}}))

			// A column without a default drops cleanly and silently.
			s.ok(t, "ALTER TABLE IF EXISTS "+parent+" ALTER COLUMN id DROP DEFAULT")
		})
	})
}

// TestReferenceAlterTableIfExistsMissing proves one NOTICE and no change for
// every ordinary action against a relation that does not exist, including the
// absent-schema form and an execute-time prepared skip.
func TestReferenceAlterTableIfExistsMissing(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			missing := s.name("atie_missing")
			renamed := s.name("atie_renamed")
			uq := s.name("atie_uq")
			uq2 := s.name("atie_uq2")
			for _, action := range []string{
				"ADD COLUMN a text",
				"ADD COLUMN b int DEFAULT 7 REFERENCES " + s.name("atie_parent") + "(pid)",
				"ALTER COLUMN v SET DEFAULT 7",
				"ALTER COLUMN v DROP DEFAULT",
				"ALTER COLUMN v TYPE bigint",
				"ALTER COLUMN id SET NOT NULL",
				"ALTER COLUMN id DROP NOT NULL",
				"DROP COLUMN a",
				"ADD CONSTRAINT " + uq + " UNIQUE(id)",
				"DROP CONSTRAINT " + uq,
				"RENAME COLUMN a TO b",
				"RENAME TO " + renamed,
				"RENAME CONSTRAINT " + uq + " TO " + uq2,
			} {
				s.exec(t, "ALTER TABLE IF EXISTS "+missing+" "+action, 0)
				s.wantNotice(t, missing)
				s.expectMissing(t, missing, renamed)
			}
			s.exec(t, "ALTER TABLE IF EXISTS ONLY "+missing+" ALTER COLUMN v SET DEFAULT 7", 0)
			s.wantNotice(t, missing)
			s.expectMissing(t, missing, renamed)
			s.exec(t, "ALTER TABLE IF EXISTS absent_schema."+missing+" ADD COLUMN a text", 0)
			s.wantNotice(t, missing)
			s.expectMissing(t, missing, renamed)

			// The skip is execute-time like the server: preparing emits nothing.
			stmt, err := s.conn.PrepareContext(s.p.Ctx, "ALTER TABLE IF EXISTS "+missing+" ADD COLUMN a text")
			if err != nil {
				t.Fatal(err)
			}
			if len(*s.notices) != 0 {
				t.Fatalf("prepare emitted notices %#v", *s.notices)
			}
			result, err := stmt.ExecContext(s.p.Ctx)
			if closeErr := stmt.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 0)
			s.wantNotice(t, missing)
			s.expectMissing(t, missing, renamed)

			// The skip left the name untouched: the same statement applies now.
			s.ok(t, "CREATE TABLE "+missing+"(id INT, v INT)")
			s.ok(t, "ALTER TABLE IF EXISTS "+missing+" ADD COLUMN a text")
			s.run(t, fixture.Select("SELECT attname,format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid=$1::regclass AND attname='a'", []string{"attname", "format_type"}, []string{"NAME", "TEXT"}, [][]any{{"a", "text"}}, missing))
		})
	})
}

// TestReferenceAlterTableIfExistsMultiAction compares one list against the
// plain spelling and shows the same list is skipped whole on a missing
// relation, before any action would fail.
func TestReferenceAlterTableIfExistsMultiAction(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			plain := s.name("atie_multi_plain")
			flagged := s.name("atie_multi_if")
			for _, table := range []string{plain, flagged} {
				s.ok(t, "CREATE TABLE "+table+"(id INT, v INT, gone INT, CONSTRAINT "+table+"_old_uq UNIQUE(gone))")
				s.exec(t, "INSERT INTO "+table+" VALUES (1,10,100),(2,20,200)", 2)
			}
			actions := func(table string) string {
				return "ADD COLUMN b text DEFAULT 'd', ALTER COLUMN v SET DEFAULT 7, ALTER COLUMN id SET NOT NULL, DROP CONSTRAINT " + table + "_old_uq, DROP COLUMN gone, ADD CONSTRAINT " + table + "_uq UNIQUE(id)"
			}
			s.ok(t, "ALTER TABLE "+plain+" "+actions(plain))
			s.ok(t, "ALTER TABLE IF EXISTS "+flagged+" "+actions(flagged))
			layout := [][]any{
				{int64(1), "id", false},
				{int64(2), "v", false},
				{int64(3), "........pg.dropped.3........", true},
				{int64(4), "b", false},
			}
			s.run(t, atieLayout(plain, layout...))
			s.run(t, atieLayout(flagged, layout...))
			wantAttributes := [][]any{
				{"id", "integer", true, false},
				{"v", "integer", false, true},
				{"b", "text", false, true},
			}
			s.run(t, atieAttributes(plain, wantAttributes...))
			s.run(t, atieAttributes(flagged, wantAttributes...))
			s.run(t, atieDefaults(plain, [][]any{{"7"}, {"'d'::text"}}))
			s.run(t, atieDefaults(flagged, [][]any{{"7"}, {"'d'::text"}}))
			for _, table := range []string{plain, flagged} {
				s.run(t, atieConstraints(table, "'u','n'",
					[]any{table + "_id_not_null", "n", "NOT NULL id"},
					[]any{table + "_uq", "u", "UNIQUE (id)"},
				))
				s.run(t, fixture.Select("SELECT id,v,b FROM "+table+" ORDER BY id", []string{"id", "v", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(10), "d"}, {int64(2), int64(20), "d"}}))
			}
			missing := s.name("atie_multi_missing")
			s.exec(t, "ALTER TABLE IF EXISTS "+missing+" "+actions(missing), 0)
			s.wantNotice(t, missing)
			s.expectMissing(t, missing)
		})
	})
}

// TestReferenceAlterTableIfExistsRenames keeps the covered-action matrix
// self-contained: relation-level IF EXISTS also renames existing columns,
// tables and constraints with no NOTICE.
func TestReferenceAlterTableIfExistsRenames(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			table := s.name("atie_rename")
			renamed := s.name("atie_rename2")
			oldName := s.name("atie_old")
			newName := s.name("atie_new")
			s.ok(t, "CREATE TABLE "+table+"(a INT, old INT, CONSTRAINT "+oldName+" UNIQUE(old))")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" RENAME COLUMN a TO b")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" RENAME TO "+renamed)
			s.ok(t, "ALTER TABLE IF EXISTS "+renamed+" RENAME CONSTRAINT "+oldName+" TO "+newName)
			s.run(t, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"b"}, {"old"}}, renamed))
			s.run(t, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('"+table+"','"+renamed+"') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{renamed}}))
			s.run(t, atieConstraints(renamed, "'u'", []any{newName, "u", "UNIQUE (old)"}))
			s.run(t, atieAbsent(table))
		})
	})
}

// TestReferenceAlterTableIfExistsFeatureSmoke is the new-behavior smoke: the
// exact round-2 repro in order and the verbatim coder 000245 ALTER forms apply.
func TestReferenceAlterTableIfExistsFeatureSmoke(t *testing.T) {
	atieScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		atieForEachRoute(t, p, b, func(t *testing.T, s atieSession) {
			table := s.name("grin_atie")
			missing := s.name("grin_atie_missing")
			s.ok(t, "CREATE TABLE "+table+" (id int, v int)")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD COLUMN a text")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ALTER COLUMN v SET DEFAULT 7")
			s.exec(t, "ALTER TABLE IF EXISTS "+missing+" ADD COLUMN b text", 0)
			s.wantNotice(t, missing)
			s.expectMissing(t, missing)
			s.run(t, atieAttributes(table,
				[]any{"id", "integer", false, false},
				[]any{"v", "integer", false, true},
				[]any{"a", "text", false, false},
			))
			s.run(t, atieDefaults(table, [][]any{{"7"}}))

			// The coder 000245 forms apply verbatim on a routed table.
			s.ok(t, "DROP TABLE "+table)
			s.ok(t, "CREATE TABLE "+table+"(id INT)")
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" ADD COLUMN IF NOT EXISTS dedupe_hash TEXT NULL")
			s.run(t, atieAttributes(table,
				[]any{"id", "integer", false, false},
				[]any{"dedupe_hash", "text", false, false},
			))
			s.ok(t, "ALTER TABLE IF EXISTS "+table+" DROP COLUMN IF EXISTS dedupe_hash")
			s.run(t, atieLayout(table,
				[]any{int64(1), "id", false},
				[]any{int64(2), "........pg.dropped.2........", true},
			))
			s.run(t, atieAttributes(table, []any{"id", "integer", false, false}))
		})
	})
}
