package reference_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTypesNativeControls(t *testing.T) {
	for _, replacement := range []string{"dump_rollback", "dump_commit", "failed_reader", "failed_late", "reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE DOMAIN tt_d AS integer DEFAULT 7", "CREATE TYPE tt_pair AS(id integer)", "CREATE TABLE tt_data(v tt_d)", "CREATE TABLE tt_retire(v integer)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO tt_data DEFAULT VALUES", 1))
					if b.PG {
						return
					}
					var baseline bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
						t.Fatal(err)
					}
					retained, err := c.QueryContext(p.Ctx, "SELECT v FROM tt_data")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					metadata, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					q := "INSERT INTO tt_data DEFAULT VALUES"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT old", 0))
					run(fixture.Exec("CREATE DOMAIN tt_private AS integer", 0))
					run(fixture.Exec("ALTER DOMAIN tt_d SET DEFAULT 9", 0))
					run(fixture.Exec("DROP TABLE tt_retire", 0))
					run(fixture.Exec(q, 1))
					var current bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &current); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(baseline.Bytes(), current.Bytes()) {
						t.Fatal("Dump exposed private catalog or rows")
					}
					target := fixture.NewFixture(t, false, true)
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(current.Bytes())); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM tt_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 1))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM tt_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
					switch replacement {
					case "dump_commit":
						run(fixture.Exec("COMMIT", 0))
						run(fixture.Select("SELECT v FROM tt_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					case "failed_reader":
						want := errors.New("transactional types reader failure")
						if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(baseline.Bytes()), fixture.TypeLifecycleErrorReader{want})); !errors.Is(err, want) {
							t.Fatal(err)
						}
						run(fixture.Exec(q, 1))
						run(fixture.Exec("COMMIT", 0))
					case "failed_late":
						fixture.State(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String()+"; INSERT INTO tt_data VALUES('bad')")), "22P02")
						run(fixture.Exec(q, 1))
						run(fixture.Exec("COMMIT", 0))
					case "reset", "restore":
						if replacement == "reset" {
							err = stuntgres.Reset(p.Ctx, p.Native)
						} else {
							err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes()))
						}
						if err != nil {
							t.Fatal(err)
						}
						for _, sql := range []string{"ROLLBACK TO old", q} {
							result, err := c.ExecContext(p.Ctx, sql)
							if result != nil || err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
								t.Fatalf("replacement %s result=%v err=%v", sql, result, err)
							}
						}
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Exec("CREATE DOMAIN tt_healthy AS integer", 0))
					default:
						run(fixture.Exec("ROLLBACK", 0))
					}
					if replacement == "dump_commit" || replacement == "dump_rollback" {
						var published bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &published); err != nil {
							t.Fatal(err)
						}
						if replacement == "dump_rollback" && !bytes.Equal(baseline.Bytes(), published.Bytes()) {
							t.Fatal("rollback changed committed dump")
						}
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(published.Bytes())); err != nil {
							t.Fatal(err)
						}
						want := [][]any{{int64(7)}}
						if replacement == "dump_commit" {
							want = append(want, []any{int64(9)})
						}
						fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM tt_data ORDER BY v", []string{"v"}, []string{"INT4"}, want))
						privateCount := int64(0)
						if replacement == "dump_commit" {
							privateCount = 1
						}
						fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tt_private'", []string{"count"}, []string{"INT8"}, [][]any{{privateCount}}))
						retiredCount := int64(1) - privateCount
						fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tt_retire'", []string{"count"}, []string{"INT8"}, [][]any{{retiredCount}}))
						fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 1))
					}
					if len(metadata) != 1 || metadata[0].Name() != "v" || metadata[0].DatabaseTypeName() != "INT4" {
						t.Fatal("retained metadata changed")
					}
					if !retained.Next() {
						t.Fatal("retained row lost", retained.Err())
					}
					var value int64
					if err := retained.Scan(&value); err != nil || value != 7 {
						t.Fatal("retained value changed", value, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("retained cardinality", retained.Err())
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tt_private'", []string{"count"}, []string{"INT8"}, [][]any{{map[bool]int64{false: 0, true: 1}[replacement == "dump_commit" || replacement == "failed_reader" || replacement == "failed_late"]}}))
				})
			}
		})
	}
}
