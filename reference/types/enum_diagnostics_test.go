package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumDiagnostics(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		fixture.EnumReferenceSetup(t, p, backend, conn)
		for _, tc := range []struct {
			query, state string
			rows         bool
			args         []any
		}{
			{"SELECT 'nope'::mood", "22P02", true, nil},
			{"SELECT /*é*/ 'nope'::mood", "22P02", true, nil},
			{"SELECT 'Sad'::mood", "22P02", true, nil},
			{"SELECT ' sad'::mood", "22P02", true, nil},
			{"SELECT 'sad '::mood", "22P02", true, nil},
			{"SELECT $1::mood", "22P02", true, []any{"nope"}},
			{"INSERT INTO enum_rows VALUES (9,$1,'ok')", "22P02", false, []any{"nope"}},
			{"INSERT INTO enum_rows VALUES (9,'ok','ok'),(10,'nope','ok')", "22P02", false, nil},
			{"CREATE TYPE mood AS ENUM ('new')", "42710", false, nil},
			{"CREATE TYPE absent.enum_missing AS ENUM ('ok')", "3F000", false, nil},
			{"SELECT NULL::enum_missing", "42704", true, nil},
			{"SELECT 'ok'::mood(3)", "42601", true, nil},
			{"SELECT 'ok'::mood COLLATE \"C\"", "42804", true, nil},
			{"INSERT INTO enum_rows(id,m) VALUES (9,'ok'::text)", "42804", false, nil},
			{"SELECT 'ok'::mood = 'ok'::text", "42883", true, nil},
			{"CREATE TYPE enum_syntax AS ENUM ('ok',)", "42601", false, nil},
			{"CREATE TYPE enum_trailing AS ENUM ('ok') nonsense", "42601", false, nil},
		} {
			fixture.EnumReferenceError(t, p, conn, tc.query, tc.state, tc.rows, tc.args...)
		}
		fixture.EnumReferenceDuplicateLabelError(t, p, conn)
		// UTF8 byte limits, not character limits: 31 two-byte é plus x is 63.
		for _, label := range []string{strings.Repeat("x", 63), strings.Repeat("é", 31) + "x"} {
			name := "enum_ascii_limit"
			if strings.Contains(label, "é") {
				name = "enum_utf8_limit"
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TYPE "+name+" AS ENUM ('"+label+"')", 0))
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(fixture.Select("SELECT $1::"+name+" AS v", []string{"v"}, []string{""}, [][]any{{label}}, label)))
		}
		for _, label := range []string{strings.Repeat("x", 64), strings.Repeat("é", 32)} {
			fixture.EnumReferenceError(t, p, conn, "CREATE TYPE enum_overlong AS ENUM ('"+label+"')", "42602", false)
			fixture.EnumReferenceError(t, p, conn, "SELECT '"+label+"'::mood", "22P02", true)
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TYPE empty_enum AS ENUM ()", 0))
		fixture.EnumReferenceError(t, p, conn, "SELECT ''::empty_enum", "22P02", true)
		// Every failed declaration is followed by a successful same-name create:
		// no parser prefix or partial member publication may survive failure.
		for _, name := range []string{"enum_duplicate", "enum_syntax", "enum_trailing", "enum_overlong"} {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TYPE "+name+" AS ENUM ('ok')", 0))
		}
		fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT id,m,h FROM enum_rows ORDER BY id", []string{"id", "m", "h"}, []string{"INT4", "", ""}, [][]any{{int64(1), "happy", "sad"}, {int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(4), nil, nil}}))
	})
}

func TestReferenceEnumNamespaceOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"enum_creator"})
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		defer func() {
			if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
			for _, schema := range []string{"enum_left", "enum_right", "enum_renamed", "enum_owned", "enum_denied"} {
				if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		run := func(s fixture.Step) { fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA enum_left", "CREATE SCHEMA enum_right",
			"CREATE TYPE enum_left.mood AS ENUM ('left','ok')",
			"CREATE TYPE enum_right.mood AS ENUM ('right','ok')",
			"CREATE TYPE enum_left.\"Odd Type\" AS ENUM ('it''s','ok')",
			"SET search_path TO enum_left,enum_right,public",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT 'left'::mood AS v", []string{"v"}, []string{""}, [][]any{{"left"}}))
		fixture.EnumReferenceError(t, p, conn, "SELECT 'right'::mood", "22P02", true)
		fixture.EnumReferenceError(t, p, conn, "SELECT 'nope'::enum_right.mood", "22P02", true)
		fixture.EnumReferenceError(t, p, conn, "SELECT 'ok'::mood = 'ok'::enum_right.mood", "42883", true)
		fixture.EnumReferenceError(t, p, conn, "SELECT 'nope'::enum_left.\"Odd Type\"", "22P02", true)
		run(fixture.Exec("SET search_path TO enum_right,enum_left,public", 0))
		run(fixture.Prepared(fixture.Select("SELECT 'right'::mood AS v", []string{"v"}, []string{""}, [][]any{{"right"}})))
		query := "SELECT $1::enum_left.mood AS v"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("ALTER SCHEMA enum_left RENAME TO enum_renamed", 0))
		// Both backends reanalyze the held source: the obsolete schema spelling
		// fails at execution rather than silently retargeting the retained type.
		rows, err := stmt.QueryContext(p.Ctx, "left")
		if rows != nil {
			rows.Close()
			t.Fatal("held renamed-schema error exposed cursor")
		}
		fixture.EnumReferenceAssertDiagnostic(t, err, fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000",
			Message: `schema "enum_left" does not exist`, Position: "12",
		})
		run(fixture.Prepared(fixture.Select("SELECT 'left'::enum_renamed.mood AS v", []string{"v"}, []string{""}, [][]any{{"left"}})))
		fixture.EnumReferenceError(t, p, conn, "SELECT 'nope'::enum_renamed.mood", "22P02", true)
		fixture.EnumReferenceError(t, p, conn, "SELECT 'ok'::enum_renamed.mood = 'ok'::enum_right.mood", "42883", true)
		run(fixture.Exec("CREATE DOMAIN enum_right.taken AS integer", 0))
		run(fixture.Exec("CREATE TABLE enum_right.table_taken(v integer)", 0))
		fixture.EnumReferenceError(t, p, conn, "CREATE TYPE enum_right.taken AS ENUM ('ok')", "42710", false)
		fixture.EnumReferenceError(t, p, conn, "CREATE TYPE enum_right.table_taken AS ENUM ('ok')", "42710", false)
		run(fixture.Exec("CREATE TYPE enum_right.type_taken AS ENUM ('ok')", 0))
		fixture.EnumReferenceError(t, p, conn, "CREATE DOMAIN enum_right.type_taken AS integer", "42710", false)
		fixture.EnumReferenceError(t, p, conn, "CREATE TABLE enum_right.type_taken(v integer)", "42710", false)
		run(fixture.Exec("CREATE USER enum_creator", 0))
		run(fixture.Exec("CREATE SCHEMA enum_denied", 0))
		run(fixture.Exec("CREATE SCHEMA enum_owned AUTHORIZATION enum_creator", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION enum_creator", 0))
		fixture.EnumReferenceError(t, p, conn, "CREATE TYPE enum_denied.no_access AS ENUM ('ok')", "42501", false)
		run(fixture.Exec("CREATE TYPE enum_owned.creator_type AS ENUM ('ok')", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Superuser dependent creation proves wiring only. PUBLIC USAGE and
		// owner authority are independently covered by later internal units.
		run(fixture.Exec("CREATE TABLE enum_owned.consumer(v enum_owned.creator_type)", 0))
		run(fixture.Exec("INSERT INTO enum_owned.consumer VALUES ('ok')", 1))
		run(fixture.Select("SELECT v FROM enum_owned.consumer", []string{"v"}, []string{""}, [][]any{{"ok"}}))
		run(fixture.Exec("CREATE TYPE enum_denied.no_access AS ENUM ('ok')", 0))
	})
}

func TestReferenceEnumPreparedLifecycle(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, queryMode := range []bool{false, true} {
			func() {
				name := "enum_quiet_exec"
				if queryMode {
					name = "enum_quiet_query"
				}
				query := "CREATE TYPE " + name + " AS ENUM ('sad','ok','happy')"
				stmt, err := conn.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.EnumReferenceError(t, p, conn, "CREATE TABLE "+name+"_rows(v "+name+")", "42704", false)
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
				if queryMode {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, nil, nil, nil))
				} else {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 0))
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE "+name+"_rows(v "+name+")", 0))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO "+name+"_rows VALUES ($1),(NULL)", 2, "ok")))
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT v FROM "+name+"_rows ORDER BY v", []string{"v"}, []string{""}, [][]any{{"ok"}, {nil}}))
				// Repeat the SAME utility handle, not a fresh prepare.
				var repeatErr error
				if queryMode {
					rows, err := stmt.QueryContext(p.Ctx)
					repeatErr = err
					if rows != nil {
						rows.Close()
						t.Fatal("duplicate utility exposed rows")
					}
				} else {
					result, err := stmt.ExecContext(p.Ctx)
					repeatErr = err
					if result != nil {
						t.Fatal("duplicate utility exposed result")
					}
				}
				fixture.EnumReferenceAssertDiagnostic(t, repeatErr, fixture.Diagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710",
					Message: `type "` + name + `" already exists`,
				})
			}()
		}
	})
}
