package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const citextComparisonViewQuery = "SELECT id,a,b,eq,lt FROM citext_compare_view ORDER BY id"

func citextComparisonStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE citext_compare_source(id integer,a citext,b citext)", 0),
		fixture.Exec("INSERT INTO citext_compare_source VALUES (1,'ABC','abc'),(2,'ABC','abd'),(3,NULL,'abc')", 3),
		fixture.Exec(`CREATE VIEW citext_compare_view AS SELECT id,a::text AS a,b::text AS b,a=b AS eq,a COLLATE "C"<b AS lt FROM citext_compare_source`, 0),
		fixture.Exec("CREATE TABLE citext_compare_guard(id integer,a citext,b citext,CONSTRAINT citext_compare_equal CHECK(a=b))", 0),
		fixture.Exec("INSERT INTO citext_compare_guard VALUES (1,'ABC','abc'),(4,NULL,'abc')", 2),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func citextComparisonStoredRows() [][]any {
	return [][]any{{int64(1), "ABC", "abc", true, false}, {int64(2), "ABC", "abd", false, true}, {int64(3), nil, "abc", nil, nil}}
}

func citextComparisonStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	citextComparisonRoutes(t, p, b, c, fixture.Select(citextComparisonViewQuery, []string{"id", "a", "b", "eq", "lt"}, []string{"INT4", "TEXT", "TEXT", "BOOL", "BOOL"}, citextComparisonStoredRows()))
	citextError(t, p, b, c, "INSERT INTO citext_compare_guard VALUES (2,'ABC','abc'),(3,'ABC','abd')", false, false, false, citextComparisonCheckDiagnostic())
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,a::text AS a,b::text AS b FROM citext_compare_guard ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "ABC", "abc"}, {int64(4), nil, "abc"}}))
}

func citextComparisonRetained(t *testing.T, rows *sql.Rows, metadata []*sql.ColumnType) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var id, a, b, eq, lt any
		if err := rows.Scan(&id, &a, &b, &eq, &lt); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, a, b, eq, lt})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, citextComparisonStoredRows()) {
		t.Fatalf("retained comparison rows %#v", got)
	}
	for i, name := range []string{"INT4", "TEXT", "TEXT", "BOOL", "BOOL"} {
		if metadata[i].DatabaseTypeName() != name {
			t.Fatalf("retained type %d: %q want %q", i, metadata[i].DatabaseTypeName(), name)
		}
	}
}

func TestReferenceCitextComparisonControls(t *testing.T) {
	for _, phase := range []string{"same_engine_and_emitted_replay", "independent_driver", "failed_restore"} {
		t.Run(phase, func(t *testing.T) {
			citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextComparisonStoredSetup(t, p, b, c)
				citextComparisonStoredChecks(t, p, b, c)
				if b.PG {
					return // PostgreSQL has no Reset/Dump/Restore control API.
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if phase == "independent_driver" {
					target := fixture.NewFixture(t, false, true)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE citext_compare_allocator_spacer(v integer)", 0))
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					destination := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
					citextComparisonStoredChecks(t, target, destination, target.Memory)
					return
				}
				old, err := c.QueryContext(p.Ctx, citextComparisonViewQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				metadata, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if phase == "failed_restore" {
					bad := "CREATE EXTENSION citext; CREATE TABLE citext_compare_guard(id integer,a citext,b citext,CONSTRAINT citext_compare_equal CHECK(a=b)); INSERT INTO citext_compare_guard VALUES (2,'ABC','abc'),(3,'ABC','abd');"
					want := citextComparisonCheckDiagnostic()
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(after.Bytes(), dump.Bytes()) {
						t.Fatal("failed Restore changed original deterministic dump")
					}
				} else {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				citextComparisonStoredChecks(t, p, b, c)
				citextComparisonRetained(t, old, metadata)
				if phase == "same_engine_and_emitted_replay" && p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual emitted comparison SQL replay", err)
					}
					pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
					citextComparisonStoredChecks(t, p, pg, p.Postgres)
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
