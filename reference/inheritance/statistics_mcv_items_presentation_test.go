package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsMCVItemsPresentation(t *testing.T) {
	for _, c := range []struct{ name, typ, literal, before, setting, after string }{
		{"date-style", "date", "'2000-01-02'", "2000-01-02", "SET DateStyle='SQL, DMY'", "02/01/2000"},
		{"timestamp-style", "timestamp", "'2000-01-02 00:00:01'", "2000-01-02 00:00:01", "SET DateStyle='SQL, DMY'", "02/01/2000 00:00:01"},
		{"timezone", "timestamptz", "'2000-01-01 00:00:01+00'", "2000-01-01 00:00:01+00", "SET TIME ZONE 'America/New_York'", "1999-12-31 19:00:01-05"},
		{"interval-style", "interval", "'2 months 3 days 4 seconds'", "2 mons 3 days 00:00:04", "SET IntervalStyle='iso_8601'", "P2M3DT4S"},
		{"bytea-output", "bytea", `'\x0001ff'`, `\x0001ff`, "SET bytea_output='escape'", `\000\001\377`},
	} {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				for _, q := range []string{"SET DateStyle='ISO, MDY'", "SET TIME ZONE 'UTC'", "SET IntervalStyle='postgres'", "SET bytea_output='hex'"} {
					s.run(fixture.Exec(q, 0))
				}
				defer func() {
					for _, q := range []string{"RESET DateStyle", "RESET TimeZone", "RESET IntervalStyle", "RESET bytea_output"} {
						if _, err := s.conn.ExecContext(s.pair.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				mcvItemsTypeSetup(s, c.typ, c.literal)
				before := mcvItemsExpected{[]any{c.before, "1"}, 1, 1}
				after := mcvItemsExpected{[]any{c.after, "1"}, 1, 1}
				mcvItemsCheck(t, s, "mcv_item_type_s", before)
				stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(s.pair.Ctx, "mcv_item_type_s")
				if err != nil {
					t.Fatal(err)
				}
				var old *sql.Rows
				if s.pg {
					mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), before)
				} else {
					old = rows
					defer old.Close()
				}
				s.run(fixture.Exec(c.setting, 0))
				// No ANALYZE: output belongs to this execution, not collection.
				mcvItemsCheck(t, s, "mcv_item_type_s", after)
				rows, err = stmt.QueryContext(s.pair.Ctx, "mcv_item_type_s")
				if err != nil {
					t.Fatal(err)
				}
				mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), after)
				if old != nil {
					mcvItemsAssert(t, mcvItemsConsume(t, old, false), before)
				}
			})
		})
	}
	t.Run("current-regclass-name", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_item_target(v INT)", 0))
			mcvItemsTypeSetup(s, "regclass", "'mcv_item_target'")
			mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{"mcv_item_target", "1"}, 1, 1})
			stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(s.pair.Ctx, "mcv_item_type_s")
			if err != nil {
				t.Fatal(err)
			}
			mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), mcvItemsExpected{[]any{"mcv_item_target", "1"}, 1, 1})
			s.run(fixture.Exec("ALTER TABLE mcv_item_target RENAME TO mcv_item_renamed", 0))
			rows, err = stmt.QueryContext(s.pair.Ctx, "mcv_item_type_s")
			if err != nil {
				t.Fatal(err)
			}
			mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), mcvItemsExpected{[]any{"mcv_item_renamed", "1"}, 1, 1})
		})
	})
	t.Run("current-regrole-name", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"mcv_item_role", "mcv_item_role_renamed"})
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE ROLE mcv_item_role", 0))
			defer s.conn.ExecContext(s.pair.Ctx, "DROP ROLE IF EXISTS mcv_item_role")
			defer s.conn.ExecContext(s.pair.Ctx, "DROP ROLE IF EXISTS mcv_item_role_renamed")
			mcvItemsTypeSetup(s, "regrole", "'mcv_item_role'")
			mcvItemsCheck(t, s, "mcv_item_type_s", mcvItemsExpected{[]any{"mcv_item_role", "1"}, 1, 1})
			stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			s.run(fixture.Exec("ALTER ROLE mcv_item_role RENAME TO mcv_item_role_renamed", 0))
			rows, err := stmt.QueryContext(s.pair.Ctx, "mcv_item_type_s")
			if err != nil {
				t.Fatal(err)
			}
			mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), mcvItemsExpected{[]any{"mcv_item_role_renamed", "1"}, 1, 1})
		})
	})
}
