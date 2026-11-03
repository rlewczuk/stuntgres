package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const ftsMatchReplacementOwner = "fts_match_replacement_owner"

// The imported source: a populated compound GIN table and an empty counterpart.
var ftsMatchReplacementSource = []string{
	"CREATE TABLE gin_control_heap(id integer, a integer[], v tsvector)",
	"ALTER TABLE gin_control_heap OWNER TO " + ftsMatchReplacementOwner,
	"INSERT INTO gin_control_heap VALUES (1,'{1,2,2}','cat:1 rat:2'), (2,'{2,3}','dog:1')",
	"CREATE INDEX gin_control_heap_idx ON gin_control_heap USING gin(a, v)",
	"CREATE TABLE gin_control_empty(id integer, a integer[], v tsvector)",
	"ALTER TABLE gin_control_empty OWNER TO " + ftsMatchReplacementOwner,
	"CREATE INDEX gin_control_empty_idx ON gin_control_empty USING gin(a, v)",
}

// Full-text matching on imported and retained TEMP rows survives Restore into
// a destination whose live session owns colliding TEMP objects. Dump, Restore
// and Reset are native-only phases; PostgreSQL replays the same source script.
func TestReferenceFTSMatchTemporaryReplacement(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{ftsMatchReplacementOwner})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE "+ftsMatchReplacementOwner, 0))
			run(fixture.Exec("CREATE TEMP TABLE kept(id integer, v tsvector)", 0))
			run(fixture.Exec("INSERT INTO kept VALUES (73,'retained:1')", 1))
			run(fixture.Exec("CREATE INDEX kept_idx ON kept USING gin(v)", 0))
			temporary := ftsMatchTemporaryOIDs(t, p, conn)
			heldQuery := "SELECT id FROM kept WHERE v @@ $1::tsquery"
			held, err := conn.PrepareContext(p.Ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()

			var importDump func()
			if b.PG {
				importDump = func() {
					run(fixture.Exec("DROP TABLE IF EXISTS gin_control_heap, gin_control_empty", 0))
					for _, statement := range ftsMatchReplacementSource {
						run(fixture.Exec(statement, ftsMatchReplacementAffected(statement)))
					}
				}
			} else {
				dump := ftsMatchReplacementDump(t, p, temporary)
				importDump = func() {
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
				}
			}
			witness := func(phase string) {
				t.Run(phase, func(t *testing.T) {
					run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					if got := ftsMatchTemporaryOIDs(t, p, conn); got != temporary {
						t.Fatal("retained TEMP identities changed", temporary, got)
					}
					ftsMatchReplacementWitness(t, p, b, conn)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: heldQuery}, b.Recorder,
						fixture.Select(heldQuery, []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}, "retained"))
					run(fixture.Exec("INSERT INTO kept VALUES (74,'retained:1')", 1))
					run(fixture.Exec("DELETE FROM kept WHERE id = 74", 1))
				})
			}
			importDump()
			witness("restore")
			importDump()
			witness("repeated_restore")
			if !b.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			importDump()
			witness("reset_restore")
			run(fixture.Exec("DROP TABLE gin_control_heap, gin_control_empty", 0))
			run(fixture.Exec("DROP TABLE kept", 0))
		})
	}
}

func ftsMatchReplacementAffected(statement string) int64 {
	if statement == ftsMatchReplacementSource[2] {
		return 2
	}
	return 0
}

// The witness's independently specified rows after the same DML, through
// direct and prepared execution.
func ftsMatchReplacementWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) {
		t.Helper()
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id, a, v FROM gin_control_heap ORDER BY id",
		[]string{"id", "a", "v"}, []string{"INT4", "_INT4", "TSVECTOR"},
		[][]any{{int64(1), "{1,2,2}", "'cat':1 'rat':2"}, {int64(2), "{2,3}", "'dog':1"}}))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO gin_control_heap VALUES (3, '{2}', 'cat:1 rat:2')", 1))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("UPDATE gin_control_heap SET a = '{9}', v = 'dog:1' WHERE id = 1", 1))
	ids := func(query string, want []int64, args ...any) {
		t.Helper()
		var rows [][]any
		for _, id := range want {
			rows = append(rows, []any{id})
		}
		run(fixture.Select(query, []string{"id"}, []string{"INT4"}, rows, args...))
	}
	ids("SELECT id FROM gin_control_heap WHERE a @> '{2}'::integer[] ORDER BY id", []int64{2, 3})
	ids("SELECT id FROM gin_control_heap WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{3})
	ids("SELECT id FROM gin_control_heap WHERE 'cat <-> rat'::tsquery @@ v ORDER BY id", []int64{3})
	ids("SELECT id FROM gin_control_heap WHERE v @@ $1::tsquery ORDER BY id", []int64{3}, "cat <-> rat")
	ids("SELECT id FROM gin_control_heap WHERE a @> $1::integer[] AND v @@ 'cat & !dog'::tsquery ORDER BY id", []int64{3}, "{2}")
	ids("SELECT id FROM gin_control_empty WHERE v @@ 'cat'::tsquery ORDER BY id", nil)
	ids("SELECT id FROM kept WHERE v @@ 'retained'::tsquery ORDER BY id", []int64{73})
	ids("SELECT h.id FROM gin_control_heap h, kept k WHERE h.v @@ 'dog'::tsquery AND k.v @@ 'retained'::tsquery ORDER BY h.id", []int64{1, 2})
	run(fixture.Select("SELECT 'cat rat'::text @@ 'cat <-> rat'::tsquery AS tq, 'cat rat'::text @@ 'rat cat'::text AS tt",
		[]string{"tq", "tt"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: tsvector @@ integer")
	d.Position = "10"
	d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	s := fixture.ErrorDiagnostic("SELECT v @@ 1 FROM gin_control_heap", d)
	s.SelectRows = true
	run(s)
}

// TEMP relation, index, row type and namespace identities of the live session.
type ftsMatchTemporaryIdentity struct{ namespace, relation, index, rowType int64 }

func ftsMatchTemporaryOIDs(t *testing.T, p *fixture.Pair, conn *sql.Conn) ftsMatchTemporaryIdentity {
	t.Helper()
	var id ftsMatchTemporaryIdentity
	if err := conn.QueryRowContext(p.Ctx, `SELECT c.relnamespace::oid::int8, c.oid::int8, i.oid::int8, c.reltype::oid::int8
		FROM pg_catalog.pg_class c, pg_catalog.pg_class i
		WHERE c.oid = 'pg_temp.kept'::regclass AND i.oid = 'pg_temp.kept_idx'::regclass`).Scan(&id.namespace, &id.relation, &id.index, &id.rowType); err != nil {
		t.Fatal(err)
	}
	return id
}

// The independent source must genuinely reuse an identity the destination's
// retained TEMP objects hold, not only coincide by name.
func ftsMatchReplacementDump(t *testing.T, p *fixture.Pair, temporary ftsMatchTemporaryIdentity) []byte {
	t.Helper()
	source := fixture.NewFixture(t, false, true)
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, source.Memory, source.MemoryRecord, s) }
	run(fixture.Exec("CREATE ROLE "+ftsMatchReplacementOwner, 0))
	for _, statement := range ftsMatchReplacementSource {
		run(fixture.Exec(statement, ftsMatchReplacementAffected(statement)))
	}
	var overlap int64
	if err := source.Memory.QueryRowContext(p.Ctx, `SELECT (SELECT count(*) FROM pg_catalog.pg_class WHERE oid IN ($1,$2,$3)) +
		(SELECT count(*) FROM pg_catalog.pg_type WHERE oid IN ($1,$2,$3,$4))`,
		temporary.namespace, temporary.relation, temporary.index, temporary.rowType).Scan(&overlap); err != nil {
		t.Fatal(err)
	}
	if overlap == 0 {
		t.Fatal("fixture did not exercise an actual imported/TEMP identity collision", temporary)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, source.Native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}
