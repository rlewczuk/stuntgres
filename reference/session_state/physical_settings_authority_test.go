package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var physicalVisibilityRoles = []string{"physical_plain", "physical_reader", "physical_bridge", "physical_inherited", "physical_monitor", "physical_setonly", "physical_super", "physical_supermember", "physical_parameter"}

func TestReferencePhysicalSettingsVisibility(t *testing.T) {
	physicalCases(t, physicalVisibilityRoles, func(s *physicalCase) {
		values := s.inventory(false)
		for _, role := range physicalVisibilityRoles {
			attribute := "LOGIN PASSWORD 'role-core-fixture'"
			if role == "physical_super" {
				attribute += " SUPERUSER"
			}
			s.run(fixture.Exec("CREATE ROLE "+role+" "+attribute, 0))
		}
		for _, query := range []string{
			"GRANT pg_read_all_settings TO physical_reader",
			"GRANT pg_read_all_settings TO physical_bridge",
			"GRANT physical_bridge TO physical_inherited",
			"GRANT pg_monitor TO physical_monitor",
			"GRANT physical_reader TO physical_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT physical_super TO physical_supermember",
		} {
			s.run(fixture.Exec(query, 0))
		}
		// Parameter ACLs are cluster-wide dependencies, not public-schema
		// objects. Revoke them even if a later assertion terminates the case.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, def := range physicalSettings {
				if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+def.name+" FROM physical_parameter"); err != nil {
					s.t.Error("parameter privilege cleanup", def.name, err)
				}
			}
		}()
		for _, def := range physicalSettings {
			s.run(fixture.Exec("GRANT SET ON PARAMETER "+def.name+" TO physical_parameter", 0))
		}
		// This is the actual bootstrap identity, not a role that merely has
		// membership of a SUPERUSER role.
		s.reads(values, true, false)
		s.reads(values, true, true)
		for _, tc := range []struct {
			role    string
			allowed bool
		}{
			{"physical_plain", false}, {"physical_reader", true},
			{"physical_inherited", true}, {"physical_monitor", true},
			{"physical_setonly", false}, {"physical_supermember", false},
			{"physical_parameter", false},
		} {
			user := s.login(tc.role)
			user.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{tc.role, tc.role}}))
			for _, prepared := range []bool{false, true} {
				user.reads(values, tc.allowed, prepared)
				for _, def := range physicalSettings {
					user.run(physicalError("SET "+def.name+"='/denied/location'", physicalRestart(def.name), prepared, false))
					user.run(physicalError("SELECT set_config($1,$2,$3)", physicalRestart(def.name), prepared, true, def.name, nil, false))
				}
				step := physicalError("SELECT current_setting('CONFIG_FILE')", physicalDenied("CONFIG_FILE"), prepared, true)
				if !tc.allowed {
					user.run(step)
				}
			}
			if tc.role == "physical_setonly" {
				user.run(fixture.Exec("SET ROLE physical_reader", 0))
				user.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"physical_setonly", "physical_reader"}}))
				user.reads(values, true, true)
				user.run(fixture.Exec("RESET ROLE", 0))
				user.reads(values, false, true)
			}
			user.conn.Close()
		}
		s.reads(values, true, true)
	})
}

type physicalHeldReader struct {
	query string
	stmt  *sql.Stmt
	name  string
	kind  string
}

func (s *physicalCase) prepareReaders(includeShow bool) []physicalHeldReader {
	s.t.Helper()
	readers := []physicalHeldReader{
		{query: "SHOW ALL", kind: "all"},
		{query: "SELECT name,setting,short_desc FROM pg_settings WHERE " + physicalPredicate + " ORDER BY name", kind: "catalog"},
	}
	for _, def := range physicalSettings {
		if includeShow {
			readers = append(readers, physicalHeldReader{query: "SHOW " + def.name, name: def.name, kind: "show"})
		}
		readers = append(readers, physicalHeldReader{query: "SELECT current_setting('" + def.name + "')", name: def.name, kind: "current"})
	}
	for i := range readers {
		stmt, err := s.conn.PrepareContext(s.p.Ctx, readers[i].query)
		if err != nil {
			s.t.Fatal(readers[i].query, err)
		}
		readers[i].stmt = stmt
		s.t.Cleanup(func() { stmt.Close() })
	}
	return readers
}

func (s *physicalCase) heldReads(readers []physicalHeldReader, values map[string]string, allowed bool) {
	s.t.Helper()
	for _, reader := range readers {
		if reader.kind == "all" {
			rows, err := reader.stmt.QueryContext(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			s.checkAll(rows, values, allowed)
			rows.Close()
			continue
		}
		var step fixture.Step
		if reader.kind == "catalog" {
			var expected [][]any
			if allowed {
				for _, def := range physicalSettings {
					expected = append(expected, []any{def.name, values[def.name], def.description})
				}
			}
			step = fixture.Select(reader.query, []string{"name", "setting", "short_desc"}, []string{"TEXT", "TEXT", "TEXT"}, expected)
		} else if !allowed {
			step = physicalError(reader.query, physicalDenied(reader.name), false, true)
		} else {
			column := "current_setting"
			if reader.kind == "show" {
				column = reader.name
			}
			step = fixture.Select(reader.query, []string{column}, []string{"TEXT"}, [][]any{{values[reader.name]}})
		}
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: reader.stmt, SQL: reader.query}, s.recorder, step)
	}
}

func TestReferencePhysicalSettingsPreparedAuthorization(t *testing.T) {
	roles := []string{"physical_prepared_plain", "physical_prepared_reader", "physical_prepared_bridge"}
	physicalCases(t, roles, func(s *physicalCase) {
		values := s.inventory(false)
		for _, role := range roles {
			s.run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", 0))
		}
		s.run(fixture.Exec("GRANT pg_read_all_settings TO physical_prepared_bridge", 0))
		s.run(fixture.Exec("GRANT physical_prepared_bridge TO physical_prepared_reader", 0))
		plain := s.login("physical_prepared_plain")
		for _, def := range physicalSettings {
			for _, name := range []string{def.name, strings.ToUpper(def.name)} {
				step := physicalError("SHOW \""+name+"\"", physicalDenied(name), true, true)
				step.ErrorStage = "prepare"
				plain.run(step)
			}
		}
		// Unauthorized preparation of current_setting and aggregate readers is
		// legal; execution alone determines their visibility.
		plainReaders := plain.prepareReaders(false)
		plain.heldReads(plainReaders, values, false)
		s.run(fixture.Exec("GRANT pg_read_all_settings TO physical_prepared_plain", 0))
		plain.heldReads(plainReaders, values, true)
		s.run(fixture.Exec("REVOKE pg_read_all_settings FROM physical_prepared_plain", 0))
		plain.heldReads(plainReaders, values, false)

		// A bootstrap session loses privilege when CurrentUser changes even
		// though its authenticated/session identity remains the superuser.
		adminReaders := s.prepareReaders(true)
		s.heldReads(adminReaders, values, true)
		s.run(fixture.Exec("SET ROLE physical_prepared_plain", 0))
		s.heldReads(adminReaders, values, false)
		s.run(fixture.Exec("RESET ROLE", 0))
		s.heldReads(adminReaders, values, true)

		// A genuinely authenticated reader must observe committed membership
		// changes from another session without re-preparing any handle.
		reader := s.login("physical_prepared_reader")
		reader.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"physical_prepared_reader", "physical_prepared_reader"}}))
		readers := reader.prepareReaders(true)
		reader.heldReads(readers, values, true)
		s.run(fixture.Exec("REVOKE pg_read_all_settings FROM physical_prepared_bridge", 0))
		reader.heldReads(readers, values, false)
		s.run(fixture.Exec("GRANT pg_read_all_settings TO physical_prepared_bridge", 0))
		reader.heldReads(readers, values, true)
	})
}
