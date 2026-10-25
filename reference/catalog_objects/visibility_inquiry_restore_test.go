package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryNoPrincipalRestore(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.NonContext = source; p.Step(t, s) }
			native := func(s fixture.Step) { s.NonContext = source; fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
			native(fixture.Exec("CREATE TABLE restored(id INT)", 0))
			native(fixture.Exec("INSERT INTO restored VALUES(7)", 1))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("")); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			// Replay the actual public native dump on the independent pinned server.
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Select("SELECT id FROM restored", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if source {
				run(fixture.Select("SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(id INT); INSERT INTO broken VALUES('bad');")); err == nil {
				t.Fatal("invalid same-engine restore published")
			}
			native(fixture.Select("SELECT id FROM restored", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))

			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-independent")
			if err != nil {
				t.Fatal(err)
			}
			target := sql.OpenDB(connector)
			defer target.Close()
			if err := stuntgres.Reset(p.Ctx, target); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target, strings.NewReader("")); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check := func() {
				rows, err := target.Query("SELECT id FROM restored")
				fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select("SELECT id FROM restored", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				q := "SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)"
				rows, err = target.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				fixture.VisibilityBoolMetadata(t, rows)
				fixture.DeleteReferenceConsume(t, false, rows, err, fixture.Select(q, []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			}
			if source {
				check()
			}
			var value int64
			if err := target.QueryRowContext(p.Ctx, "SELECT id FROM restored").Scan(&value); err != nil || value != 7 {
				t.Fatal(value, err)
			}
			if err := stuntgres.Restore(p.Ctx, target, strings.NewReader("CREATE TABLE broken(id INT); INSERT INTO broken VALUES('bad');")); err == nil {
				t.Fatal("invalid restore published")
			}
			if err := target.QueryRowContext(p.Ctx, "SELECT id FROM restored").Scan(&value); err != nil || value != 7 {
				t.Fatal(value, err)
			}
			if source {
				check()
			}
			var collation, conversion bool
			if err := target.QueryRowContext(p.Ctx, "SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4402)").Scan(&collation, &conversion); err != nil || !collation || !conversion {
				t.Fatal(collation, conversion, err)
			}
			var typ bool
			if err := target.QueryRowContext(p.Ctx, "SELECT pg_type_is_visible(23)").Scan(&typ); err != nil || !typ {
				t.Fatal(typ, err)
			}
		})
	}
}
