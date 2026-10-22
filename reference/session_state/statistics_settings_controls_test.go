package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsSettingsControls(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		run(fixture.Exec("SET application_name='retained-settings'", 0))
		if pg {
			run(fixture.Select("SELECT setting FROM pg_settings WHERE name='application_name'", []string{"setting"}, []string{"TEXT"}, [][]any{{"retained-settings"}}))
			return
		}
		run(fixture.Exec("CREATE TABLE statistics_control_data(v integer)", 0))
		run(fixture.Exec("INSERT INTO statistics_control_data VALUES(7)", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"invalid", "reset", "restore"} {
			rows, err := conn.QueryContext(p.Ctx, "SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings ORDER BY name")
			if err != nil {
				t.Fatal(err)
			}
			statisticsSettingsMetadata(t, rows, false)
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			switch action {
			case "invalid":
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("not valid SQL")); err == nil {
					rows.Close()
					t.Fatal("invalid Restore succeeded")
				}
			case "reset":
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					rows.Close()
					t.Fatal(err)
				}
			case "restore":
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					rows.Close()
					t.Fatal(err)
				}
			}
			count, seen := 0, false
			for rows.Next() {
				cells, dest := make([]any, 17), make([]any, 17)
				for i := range cells {
					dest[i] = &cells[i]
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				count++
				if fixture.Normalize(cells[0]) == "application_name" {
					seen = true
					if fixture.Normalize(cells[1]) != "retained-settings" || fixture.Normalize(cells[8]) != "session" {
						t.Fatal(cells)
					}
				}
			}
			if rows.Err() != nil || count != 33 || !seen {
				t.Fatal(count, seen, rows.Err())
			}
			rows.Close()
			if !reflect.DeepEqual(columns, statisticsSettingsColumns) {
				t.Fatal(columns)
			}
			run(fixture.Select("SELECT setting,source FROM pg_settings WHERE name='application_name'", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"retained-settings", "session"}}))
			if action != "reset" {
				run(fixture.Select("SELECT v FROM statistics_control_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		// Physical adapter retirement cannot invalidate its already-owned driver rows.
		raw, err := p.NativeConnector.Connect(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT name,setting FROM pg_settings ORDER BY name", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns := append([]string(nil), rows.Columns()...)
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		count := 0
		for {
			cells := make([]driver.Value, 2)
			err := rows.Next(cells)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 33 || !reflect.DeepEqual(rows.Columns(), columns) {
			t.Fatal(count, rows.Columns())
		}
	})
}
