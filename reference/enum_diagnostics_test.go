package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceEnumDiagnostics(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
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
			enumReferenceError(t, p, conn, tc.query, tc.state, tc.rows, tc.args...)
		}
		enumReferenceDuplicateLabelError(t, p, conn)
		// UTF8 byte limits, not character limits: 31 two-byte é plus x is 63.
		for _, label := range []string{strings.Repeat("x", 63), strings.Repeat("é", 31) + "x"} {
			name := "enum_ascii_limit"
			if strings.Contains(label, "é") {
				name = "enum_utf8_limit"
			}
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TYPE "+name+" AS ENUM ('"+label+"')", 0))
			enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceSelect("SELECT $1::"+name+" AS v", []string{"v"}, []string{""}, [][]any{{label}}, label)))
		}
		for _, label := range []string{strings.Repeat("x", 64), strings.Repeat("é", 32)} {
			enumReferenceError(t, p, conn, "CREATE TYPE enum_overlong AS ENUM ('"+label+"')", "42602", false)
			enumReferenceError(t, p, conn, "SELECT '"+label+"'::mood", "22P02", true)
		}
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TYPE empty_enum AS ENUM ()", 0))
		enumReferenceError(t, p, conn, "SELECT ''::empty_enum", "22P02", true)
		// Every failed declaration is followed by a successful same-name create:
		// no parser prefix or partial member publication may survive failure.
		for _, name := range []string{"enum_duplicate", "enum_syntax", "enum_trailing", "enum_overlong"} {
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TYPE "+name+" AS ENUM ('ok')", 0))
		}
		enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT id,m,h FROM enum_rows ORDER BY id", []string{"id", "m", "h"}, []string{"INT4", "", ""}, [][]any{{int64(1), "happy", "sad"}, {int64(2), "sad", "happy"}, {int64(3), "ok", "ok"}, {int64(4), nil, nil}}))
	})
}

func TestReferenceEnumNamespaceOwnership(t *testing.T) {
	roleReferenceCleanup(t, []string{"enum_creator"})
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		defer func() {
			if _, err := conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
			for _, schema := range []string{"enum_left", "enum_right", "enum_renamed", "enum_owned", "enum_denied"} {
				if _, err := conn.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		run := func(s referenceStep) { enumReferenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA enum_left", "CREATE SCHEMA enum_right",
			"CREATE TYPE enum_left.mood AS ENUM ('left','ok')",
			"CREATE TYPE enum_right.mood AS ENUM ('right','ok')",
			"CREATE TYPE enum_left.\"Odd Type\" AS ENUM ('it''s','ok')",
			"SET search_path TO enum_left,enum_right,public",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceSelect("SELECT 'left'::mood AS v", []string{"v"}, []string{""}, [][]any{{"left"}}))
		enumReferenceError(t, p, conn, "SELECT 'right'::mood", "22P02", true)
		enumReferenceError(t, p, conn, "SELECT 'nope'::enum_right.mood", "22P02", true)
		enumReferenceError(t, p, conn, "SELECT 'ok'::mood = 'ok'::enum_right.mood", "42883", true)
		enumReferenceError(t, p, conn, "SELECT 'nope'::enum_left.\"Odd Type\"", "22P02", true)
		run(referenceExec("SET search_path TO enum_right,enum_left,public", 0))
		run(referencePrepared(referenceSelect("SELECT 'right'::mood AS v", []string{"v"}, []string{""}, [][]any{{"right"}})))
		query := "SELECT $1::enum_left.mood AS v"
		stmt, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(referenceExec("ALTER SCHEMA enum_left RENAME TO enum_renamed", 0))
		// Both backends reanalyze the held source: the obsolete schema spelling
		// fails at execution rather than silently retargeting the retained type.
		rows, err := stmt.QueryContext(p.ctx, "left")
		if rows != nil {
			rows.Close()
			t.Fatal("held renamed-schema error exposed cursor")
		}
		enumReferenceAssertDiagnostic(t, err, referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000",
			Message: `schema "enum_left" does not exist`, Position: "12",
		})
		run(referencePrepared(referenceSelect("SELECT 'left'::enum_renamed.mood AS v", []string{"v"}, []string{""}, [][]any{{"left"}})))
		enumReferenceError(t, p, conn, "SELECT 'nope'::enum_renamed.mood", "22P02", true)
		enumReferenceError(t, p, conn, "SELECT 'ok'::enum_renamed.mood = 'ok'::enum_right.mood", "42883", true)
		run(referenceExec("CREATE DOMAIN enum_right.taken AS integer", 0))
		run(referenceExec("CREATE TABLE enum_right.table_taken(v integer)", 0))
		enumReferenceError(t, p, conn, "CREATE TYPE enum_right.taken AS ENUM ('ok')", "42710", false)
		enumReferenceError(t, p, conn, "CREATE TYPE enum_right.table_taken AS ENUM ('ok')", "42710", false)
		run(referenceExec("CREATE TYPE enum_right.type_taken AS ENUM ('ok')", 0))
		enumReferenceError(t, p, conn, "CREATE DOMAIN enum_right.type_taken AS integer", "42710", false)
		enumReferenceError(t, p, conn, "CREATE TABLE enum_right.type_taken(v integer)", "42710", false)
		run(referenceExec("CREATE USER enum_creator", 0))
		run(referenceExec("CREATE SCHEMA enum_denied", 0))
		run(referenceExec("CREATE SCHEMA enum_owned AUTHORIZATION enum_creator", 0))
		run(referenceExec("SET SESSION AUTHORIZATION enum_creator", 0))
		enumReferenceError(t, p, conn, "CREATE TYPE enum_denied.no_access AS ENUM ('ok')", "42501", false)
		run(referenceExec("CREATE TYPE enum_owned.creator_type AS ENUM ('ok')", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Superuser dependent creation proves wiring only. PUBLIC USAGE and
		// owner authority are independently covered by later internal units.
		run(referenceExec("CREATE TABLE enum_owned.consumer(v enum_owned.creator_type)", 0))
		run(referenceExec("INSERT INTO enum_owned.consumer VALUES ('ok')", 1))
		run(referenceSelect("SELECT v FROM enum_owned.consumer", []string{"v"}, []string{""}, [][]any{{"ok"}}))
		run(referenceExec("CREATE TYPE enum_denied.no_access AS ENUM ('ok')", 0))
	})
}

func TestReferenceEnumPreparedLifecycle(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		for _, queryMode := range []bool{false, true} {
			func() {
				name := "enum_quiet_exec"
				if queryMode {
					name = "enum_quiet_query"
				}
				query := "CREATE TYPE " + name + " AS ENUM ('sad','ok','happy')"
				stmt, err := conn.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				enumReferenceError(t, p, conn, "CREATE TABLE "+name+"_rows(v "+name+")", "42704", false)
				held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
				if queryMode {
					referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(query, nil, nil, nil))
				} else {
					referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(query, 0))
				}
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TABLE "+name+"_rows(v "+name+")", 0))
				referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceExec("INSERT INTO "+name+"_rows VALUES ($1),(NULL)", 2, "ok")))
				enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT v FROM "+name+"_rows ORDER BY v", []string{"v"}, []string{""}, [][]any{{"ok"}, {nil}}))
				// Repeat the SAME utility handle, not a fresh prepare.
				var repeatErr error
				if queryMode {
					rows, err := stmt.QueryContext(p.ctx)
					repeatErr = err
					if rows != nil {
						rows.Close()
						t.Fatal("duplicate utility exposed rows")
					}
				} else {
					result, err := stmt.ExecContext(p.ctx)
					repeatErr = err
					if result != nil {
						t.Fatal("duplicate utility exposed result")
					}
				}
				enumReferenceAssertDiagnostic(t, repeatErr, referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710",
					Message: `type "` + name + `" already exists`,
				})
			}()
		}
	})
}
