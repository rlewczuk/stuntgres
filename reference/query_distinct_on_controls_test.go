package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func queryDistinctOnNativeReplay(t *testing.T, p *referencePair, s referenceStep) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/on-copy")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	stringCoreControlExec(t, db, "CREATE TABLE sentinel(id INT)")
	stringCoreControlExec(t, db, "INSERT INTO sentinel VALUES(17)")
	stringCoreControlExec(t, db, "CREATE VIEW prior_on AS SELECT DISTINCT ON(id) id FROM sentinel")
	old, err := db.Query("SELECT id FROM prior_on")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	check := func() {
		rows, err := db.Query("SELECT id FROM prior_on")
		stringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	}
	suffix := "/* λ */ CREATE VIEW broken_on AS SELECT DISTINCT ON(id) 17 AS payload FROM sentinel ORDER BY payload;\n"
	want := controlsReferenceDiagnostic("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
	// broken_on must refer to an object in staged dump, not the destination's
	// sentinel. Inject that object into the staged input before the bad view.
	prefix := "CREATE TABLE sentinel(id INT); INSERT INTO sentinel VALUES(99);\n"
	invalid := append(append(append([]byte(nil), dump.Bytes()...), prefix...), suffix...)
	want.Position = referencePosition(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(prefix) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "id)")]) + 1)
	err = stuntgres.Restore(p.ctx, db, bytes.NewReader(invalid))
	referenceAssertDiagnostic(t, err, &want)
	check()
	ctx, cancel := context.WithCancel(p.ctx)
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
			if err := stuntgres.Reset(p.ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(s.query, s.args...)
		stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, s.bag)
		if held == nil {
			held, err = db.Prepare(s.query)
			if err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			rows, err := held.Query(s.args...)
			stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, s.bag)
		}
	}
	stringCoreRetainedRows(t, old, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	retained, err := db.Query(s.query, s.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	stringCoreRetainedRows(t, retained, nil, s.columns, s.types, s.rows, s.bag)
	// Replay the actual native emitted SQL on the oracle, without polluting the
	// later PostgreSQL scenario. Native APIs themselves have no PG equivalent.
	if p.postgres != nil {
		tx, err := p.postgres.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("emitted ON replay", err)
		}
		rows, err := tx.QueryContext(p.ctx, s.query, s.args...)
		stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, s.bag)
	}
}

func queryDistinctOnViewWitness(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
	t.Helper()
	run(referenceExec("CREATE VIEW latest AS SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", 0))
	s := queryDistinctOnHidden()
	s.query = "SELECT id,payload FROM latest ORDER BY id"
	queryDistinctBoth(run, s)
	queryDistinctOnHeld(t, p, b, c, s)
	var definition string
	if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('latest'::regclass)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	run(referenceExec("CREATE VIEW replay_latest AS "+definition, 0))
	replay := s
	replay.query = "SELECT id,payload FROM replay_latest ORDER BY id"
	queryDistinctBoth(run, replay)
	if !b.pg {
		queryDistinctOnNativeReplay(t, p, s)
	}
}

func TestReferenceQueryDistinctOnControls(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		queryDistinctOnViewWitness(t, p, b, c, run)
		// Override collides with a hidden physical key name. Capture must not
		// turn the hidden k into the newly named output (id).
		run(referenceExec("CREATE VIEW on_override(k,label) AS SELECT DISTINCT ON(k) id,payload FROM events ORDER BY k,stamp DESC,id DESC", 0))
		s := queryDistinctOnHidden()
		s.query = "SELECT k,label FROM on_override ORDER BY k"
		s.columns = []string{"k", "label"}
		queryDistinctBoth(run, s)
		run(referenceExec("CREATE VIEW on_visible(chosen,id) AS SELECT DISTINCT ON(chosen) id,k AS chosen FROM events ORDER BY chosen,stamp DESC,id DESC", 0))
		visible := referenceSelect("SELECT chosen,id FROM on_visible ORDER BY chosen", []string{"chosen", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(1)}, {int64(5), int64(2)}, {int64(7), nil}})
		queryDistinctBoth(run, visible)
		queryDistinctOnHeld(t, p, b, c, visible)
		run(referenceExec("CREATE TABLE l(id INT,k INT)", 0))
		run(referenceExec("CREATE TABLE r(k INT)", 0))
		run(referenceExec("INSERT INTO l VALUES(1,1),(2,1),(3,NULL)", 3))
		run(referenceExec("INSERT INTO r VALUES(1),(2)", 2))
		run(referenceExec("CREATE VIEW on_using AS SELECT DISTINCT ON(k) id FROM l FULL JOIN r USING(k) ORDER BY k,id DESC NULLS LAST", 0))
		using := referenceSelect("SELECT id FROM on_using ORDER BY id NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {nil}})
		queryDistinctBoth(run, using)
		run(referenceExec("CREATE TABLE only_key(k INT)", 0))
		run(referenceExec("INSERT INTO only_key VALUES(1)", 1))
		run(referenceExec("CREATE VIEW on_dependency AS SELECT DISTINCT ON((SELECT k FROM only_key)) id FROM events ORDER BY (SELECT k FROM only_key),id DESC", 0))
		dependency := referenceSelect("SELECT id FROM on_dependency", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		queryDistinctBoth(run, dependency)
		if !b.pg {
			queryDistinctOnNativeReplay(t, p, s)
			queryDistinctOnNativeReplay(t, p, visible)
			queryDistinctOnNativeReplay(t, p, using)
			queryDistinctOnNativeReplay(t, p, dependency)
		}
	})
}

func TestReferenceQueryDistinctOnTypedControls(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TYPE replay_mood AS ENUM('z','a')", 0))
		run(referenceExec("CREATE DOMAIN replay_mood_domain AS replay_mood", 0))
		run(referenceExec("CREATE DOMAIN replay_element AS INT", 0))
		run(referenceExec("CREATE TABLE replay_keys(id INT,k replay_mood_domain,a replay_element[])", 0))
		run(referenceExec("INSERT INTO replay_keys VALUES(1,'z','{1,NULL}'),(2,'z','{1,NULL}'),(3,'a','{2}'),(4,NULL,NULL),(5,NULL,NULL)", 5))
		run(referenceExec("CREATE VIEW on_enum AS SELECT DISTINCT ON(k) id FROM replay_keys ORDER BY k,id DESC", 0))
		run(referenceExec("CREATE VIEW on_array AS SELECT DISTINCT ON(a) 17 AS payload FROM replay_keys", 0))
		enum := referenceSelect("SELECT id FROM on_enum", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}})
		array := referenceSelect("SELECT count(*) AS n FROM on_array", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
		queryDistinctBoth(run, enum)
		queryDistinctBoth(run, array)
		if !b.pg {
			queryDistinctOnNativeReplay(t, p, enum)
			queryDistinctOnNativeReplay(t, p, array)
		}
	})
}

func TestReferenceQueryDistinctOnFeatureSmoke(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		s := queryDistinctOnHidden()
		queryDistinctBoth(run, s)
		queryDistinctOnHeld(t, p, b, c, s)
		alias := s
		alias.query = "SELECT DISTINCT ON(2) id,k AS key FROM events ORDER BY key,stamp DESC,id DESC,key DESC NULLS FIRST"
		alias.columns = []string{"id", "key"}
		alias.types = []string{"INT4", "INT4"}
		alias.rows = [][]any{{int64(3), int64(1)}, {int64(5), int64(2)}, {int64(7), nil}}
		queryDistinctBoth(run, alias)
		nullGroup := s
		nullGroup.query = "SELECT DISTINCT ON(k) id,payload FROM events WHERE k IS NULL ORDER BY k,stamp DESC,id DESC"
		nullGroup.rows = [][]any{{int64(7), "newnull"}}
		queryDistinctBoth(run, nullGroup)
		heldQuery := "SELECT DISTINCT ON(k) id,payload FROM events WHERE id>=$1 ORDER BY k,stamp DESC,id DESC"
		held, err := c.PrepareContext(p.ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, arg := range []any{nil, 8, 1, 1} {
			rows := [][]any{}
			if arg == 1 {
				rows = s.rows
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{c, held, heldQuery}, b.recorder, referenceSelect(heldQuery, s.columns, s.types, rows, arg))
		}
		bounded := s
		bounded.query += " LIMIT 1 OFFSET 1"
		bounded.rows = [][]any{{int64(5), "new2"}}
		queryDistinctBoth(run, bounded)
		q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|k) id FROM events ORDER BY stamp DESC,id DESC", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
		queryDistinctAnalysis(t, p, b, c, q, want)
		run(referenceExec("CREATE TABLE winners(id INT,payload TEXT)", 0))
		run(referenceExec("INSERT INTO winners "+s.query, 3))
		queryDistinctBoth(run, referenceSelect("SELECT id,payload FROM winners ORDER BY id", s.columns, s.types, s.rows))
		queryDistinctOnViewWitness(t, p, b, c, run)
	})
}
