package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func queryDistinctOnNativeReplay(t *testing.T, p *fixture.Pair, s fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/on-copy")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	fixture.StringCoreControlExec(t, db, "CREATE TABLE sentinel(id INT)")
	fixture.StringCoreControlExec(t, db, "INSERT INTO sentinel VALUES(17)")
	fixture.StringCoreControlExec(t, db, "CREATE VIEW prior_on AS SELECT DISTINCT ON(id) id FROM sentinel")
	old, err := db.Query("SELECT id FROM prior_on")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	check := func() {
		rows, err := db.Query("SELECT id FROM prior_on")
		fixture.StringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	}
	suffix := "/* λ */ CREATE VIEW broken_on AS SELECT DISTINCT ON(id) 17 AS payload FROM sentinel ORDER BY payload;\n"
	want := fixture.ControlsReferenceDiagnostic("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
	// broken_on must refer to an object in staged dump, not the destination's
	// sentinel. Inject that object into the staged input before the bad view.
	prefix := "CREATE TABLE sentinel(id INT); INSERT INTO sentinel VALUES(99);\n"
	invalid := append(append(append([]byte(nil), dump.Bytes()...), prefix...), suffix...)
	want.Position = fixture.Position(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(prefix) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "id)")]) + 1)
	err = stuntgres.Restore(p.Ctx, db, bytes.NewReader(invalid))
	fixture.AssertDiagnostic(t, err, &want)
	check()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check()
	var held *sql.Stmt
	defer func() {
		if held != nil {
			held.Close()
		}
	}()
	for _, reset := range []bool{false, true} {
		if reset {
			if err := stuntgres.Reset(p.Ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(s.Query, s.Args...)
		fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, s.Bag)
		if held == nil {
			held, err = db.Prepare(s.Query)
			if err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			rows, err := held.Query(s.Args...)
			fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, s.Bag)
		}
	}
	fixture.StringCoreRetainedRows(t, old, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	retained, err := db.Query(s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, retained, nil, s.Columns, s.Types, s.Rows, s.Bag)
	// Replay the actual native emitted SQL on the oracle, without polluting the
	// later PostgreSQL scenario. Native APIs themselves have no PG equivalent.
	if p.Postgres != nil {
		tx, err := p.Postgres.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("emitted ON replay", err)
		}
		rows, err := tx.QueryContext(p.Ctx, s.Query, s.Args...)
		fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, s.Bag)
	}
}

func queryDistinctOnViewWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE VIEW latest AS SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", 0))
	s := queryDistinctOnHidden()
	s.Query = "SELECT id,payload FROM latest ORDER BY id"
	fixture.QueryDistinctBoth(run, s)
	queryDistinctOnHeld(t, p, b, c, s)
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('latest'::regclass)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("CREATE VIEW replay_latest AS "+definition, 0))
	replay := s
	replay.Query = "SELECT id,payload FROM replay_latest ORDER BY id"
	fixture.QueryDistinctBoth(run, replay)
	if !b.PG {
		queryDistinctOnNativeReplay(t, p, s)
	}
}

func TestReferenceQueryDistinctOnControls(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		queryDistinctOnViewWitness(t, p, b, c, run)
		// Override collides with a hidden physical key name. Capture must not
		// turn the hidden k into the newly named output (id).
		run(fixture.Exec("CREATE VIEW on_override(k,label) AS SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", 0))
		s := queryDistinctOnHidden()
		s.Query = "SELECT k,label FROM on_override ORDER BY k"
		s.Columns = []string{"k", "label"}
		fixture.QueryDistinctBoth(run, s)
		run(fixture.Exec("CREATE VIEW on_visible(chosen,id) AS SELECT DISTINCT ON(chosen) id,k AS chosen FROM events ORDER BY chosen,stamp DESC,id DESC", 0))
		visible := fixture.Select("SELECT chosen,id FROM on_visible ORDER BY chosen", []string{"chosen", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(1)}, {int64(5), int64(2)}, {int64(7), nil}})
		fixture.QueryDistinctBoth(run, visible)
		queryDistinctOnHeld(t, p, b, c, visible)
		run(fixture.Exec("CREATE TABLE l(id INT,k INT)", 0))
		run(fixture.Exec("CREATE TABLE r(k INT)", 0))
		run(fixture.Exec("INSERT INTO l VALUES(1,1),(2,1),(3,NULL)", 3))
		run(fixture.Exec("INSERT INTO r VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE VIEW on_using AS SELECT DISTINCT ON(k) id FROM l FULL JOIN r USING(k) ORDER BY k,id DESC NULLS LAST", 0))
		using := fixture.Select("SELECT id FROM on_using ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {nil}})
		fixture.QueryDistinctBoth(run, using)
		run(fixture.Exec("CREATE TABLE only_key(k INT)", 0))
		run(fixture.Exec("INSERT INTO only_key VALUES(1)", 1))
		run(fixture.Exec("CREATE VIEW on_dependency AS SELECT DISTINCT ON((SELECT k FROM only_key)) id FROM events ORDER BY (SELECT k FROM only_key),id DESC", 0))
		dependency := fixture.Select("SELECT id FROM on_dependency", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.QueryDistinctBoth(run, dependency)
		if !b.PG {
			queryDistinctOnNativeReplay(t, p, s)
			queryDistinctOnNativeReplay(t, p, visible)
			queryDistinctOnNativeReplay(t, p, using)
			queryDistinctOnNativeReplay(t, p, dependency)
		}
	})
}

func TestReferenceQueryDistinctOnTypedControls(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TYPE replay_mood AS ENUM('z','a')", 0))
		run(fixture.Exec("CREATE DOMAIN replay_mood_domain AS replay_mood", 0))
		run(fixture.Exec("CREATE DOMAIN replay_element AS INT", 0))
		run(fixture.Exec("CREATE TABLE replay_keys(id INT,k replay_mood_domain,a replay_element[])", 0))
		run(fixture.Exec("INSERT INTO replay_keys VALUES(1,'z','{1,NULL}'),(2,'z','{1,NULL}'),(3,'a','{2}'),(4,NULL,NULL),(5,NULL,NULL)", 5))
		run(fixture.Exec("CREATE VIEW on_enum AS SELECT DISTINCT ON(k) id FROM replay_keys ORDER BY k,id DESC", 0))
		run(fixture.Exec("CREATE VIEW on_array AS SELECT DISTINCT ON(a) 17 AS payload FROM replay_keys", 0))
		enum := fixture.Select("SELECT id FROM on_enum", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}})
		array := fixture.Select("SELECT count(*) AS n FROM on_array", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
		fixture.QueryDistinctBoth(run, enum)
		fixture.QueryDistinctBoth(run, array)
		if !b.PG {
			queryDistinctOnNativeReplay(t, p, enum)
			queryDistinctOnNativeReplay(t, p, array)
		}
	})
}

func TestReferenceQueryDistinctOnFeatureSmoke(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		s := queryDistinctOnHidden()
		fixture.QueryDistinctBoth(run, s)
		queryDistinctOnHeld(t, p, b, c, s)
		alias := s
		alias.Query = "SELECT DISTINCT ON(2) id,k AS key FROM events ORDER BY key,stamp DESC,id DESC,key DESC NULLS FIRST"
		alias.Columns = []string{"id", "key"}
		alias.Types = []string{"INT4", "INT4"}
		alias.Rows = [][]any{{int64(3), int64(1)}, {int64(5), int64(2)}, {int64(7), nil}}
		fixture.QueryDistinctBoth(run, alias)
		nullGroup := s
		nullGroup.Query = "SELECT DISTINCT ON(k) id,payload FROM events WHERE k IS NULL ORDER BY k,stamp DESC,id DESC"
		nullGroup.Rows = [][]any{{int64(7), "newnull"}}
		fixture.QueryDistinctBoth(run, nullGroup)
		heldQuery := "SELECT DISTINCT ON(k) id,payload FROM events WHERE id>=$1 ORDER BY k,stamp DESC,id DESC"
		held, err := c.PrepareContext(p.Ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, arg := range []any{nil, 8, 1, 1} {
			rows := [][]any{}
			if arg == 1 {
				rows = s.Rows
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, held, heldQuery}, b.Recorder, fixture.Select(heldQuery, s.Columns, s.Types, rows, arg))
		}
		bounded := s
		bounded.Query += " LIMIT 1 OFFSET 1"
		bounded.Rows = [][]any{{int64(5), "new2"}}
		fixture.QueryDistinctBoth(run, bounded)
		q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|k) id FROM events ORDER BY stamp DESC,id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
		run(fixture.Exec("CREATE TABLE winners(id INT,payload TEXT)", 0))
		run(fixture.Exec("INSERT INTO winners "+s.Query, 3))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT id,payload FROM winners ORDER BY id", s.Columns, s.Types, s.Rows))
		queryDistinctOnViewWitness(t, p, b, c, run)
	})
}
