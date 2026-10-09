package reference_test

import (
	"strings"
	"testing"
)

// Complete logical diagnostics and prepared stages observed in root tests-3/4
// (pinned PostgreSQL18.0). Omitted struct fields mean independently expected empty.
func enumReferenceErrorOracle(t *testing.T, query string) (referenceDiagnostic, string) {
	t.Helper()
	const operatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	type expectation struct{ query, code, message, position, detail, hint, context, stage string }
	entries := []expectation{
		{"SELECT 'nope'::mood", "22P02", `invalid input value for enum mood: "nope"`, "8", "", "", "", "prepare"},
		{"SELECT /*é*/ 'nope'::mood", "22P02", `invalid input value for enum mood: "nope"`, "14", "", "", "", "prepare"},
		{"SELECT $1::mood", "22P02", `invalid input value for enum mood: "nope"`, "", "", "", "unnamed portal parameter $1 = '...'", "execute"},
		{"INSERT INTO enum_rows VALUES (9,$1,'ok')", "22P02", `invalid input value for enum mood: "nope"`, "", "", "", "unnamed portal parameter $1 = '...'", "execute"},
		{"INSERT INTO enum_rows VALUES (9,'ok','ok'),(10,'nope','ok')", "22P02", `invalid input value for enum mood: "nope"`, "48", "", "", "", "prepare"},
		{"CREATE TYPE mood AS ENUM ('new')", "42710", `type "mood" already exists`, "", "", "", "", "execute"},
		{"CREATE TYPE absent.enum_missing AS ENUM ('ok')", "3F000", `schema "absent" does not exist`, "", "", "", "", "execute"},
		{"SELECT NULL::enum_missing", "42704", `type "enum_missing" does not exist`, "14", "", "", "", "prepare"},
		{"SELECT 'ok'::mood(3)", "42601", `type modifier is not allowed for type "mood"`, "14", "", "", "", "prepare"},
		{"SELECT 'ok'::mood COLLATE \"C\"", "42804", "collations are not supported by type mood", "19", "", "", "", "prepare"},
		{"INSERT INTO enum_rows(id,m) VALUES (9,'ok'::text)", "42804", `column "m" is of type mood but expression is of type text`, "39", "", "You will need to rewrite or cast the expression.", "", "prepare"},
		{"SELECT 'ok'::mood = 'ok'::text", "42883", "operator does not exist: mood = text", "19", "", operatorHint, "", "prepare"},
		{"CREATE TYPE enum_syntax AS ENUM ('ok',)", "42601", `syntax error at or near ")"`, "39", "", "", "", "prepare"},
		{"CREATE TYPE enum_trailing AS ENUM ('ok') nonsense", "42601", `syntax error at or near "nonsense"`, "42", "", "", "", "prepare"},
		{"SELECT ''::empty_enum", "22P02", `invalid input value for enum empty_enum: ""`, "8", "", "", "", "prepare"},
		{"SELECT 'right'::mood", "22P02", `invalid input value for enum mood: "right"`, "8", "", "", "", "prepare"},
		{"SELECT 'nope'::enum_right.mood", "22P02", `invalid input value for enum enum_right.mood: "nope"`, "8", "", "", "", "prepare"},
		{"SELECT 'ok'::mood = 'ok'::enum_right.mood", "42883", "operator does not exist: mood = enum_right.mood", "19", "", operatorHint, "", "prepare"},
		{"SELECT 'nope'::enum_left.\"Odd Type\"", "22P02", `invalid input value for enum "Odd Type": "nope"`, "8", "", "", "", "prepare"},
		{"SELECT 'nope'::enum_renamed.mood", "22P02", `invalid input value for enum enum_renamed.mood: "nope"`, "8", "", "", "", "prepare"},
		{"SELECT 'ok'::enum_renamed.mood = 'ok'::enum_right.mood", "42883", "operator does not exist: enum_renamed.mood = mood", "32", "", operatorHint, "", "prepare"},
		{"CREATE TYPE enum_right.taken AS ENUM ('ok')", "42710", `type "taken" already exists`, "", "", "", "", "execute"},
		{"CREATE TYPE enum_right.table_taken AS ENUM ('ok')", "42710", `type "table_taken" already exists`, "", "", "", "", "execute"},
		{"CREATE DOMAIN enum_right.type_taken AS integer", "42710", `type "type_taken" already exists`, "", "", "", "", "execute"},
		{"CREATE TABLE enum_right.type_taken(v integer)", "42710", `type "type_taken" already exists`, "", "", "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type.", "", "execute"},
		{"CREATE TYPE enum_denied.no_access AS ENUM ('ok')", "42501", "permission denied for schema enum_denied", "", "", "", "", "execute"},
		{"CREATE TABLE enum_quiet_exec_rows(v enum_quiet_exec)", "42704", `type "enum_quiet_exec" does not exist`, "37", "", "", "", "execute"},
		{"CREATE TABLE enum_quiet_query_rows(v enum_quiet_query)", "42704", `type "enum_quiet_query" does not exist`, "38", "", "", "", "execute"},
		{"SELECT 'sad'::mood = 'sad'::happiness", "42883", "operator does not exist: mood = happiness", "20", "", operatorHint, "", "prepare"},
	}
	for _, label := range []string{"Sad", " sad", "sad ", strings.Repeat("x", 64), strings.Repeat("é", 32)} {
		entries = append(entries, expectation{"SELECT '" + label + "'::mood", "22P02", `invalid input value for enum mood: "` + label + `"`, "8", "", "", "", "prepare"})
	}
	for _, label := range []string{strings.Repeat("x", 64), strings.Repeat("é", 32)} {
		entries = append(entries, expectation{"CREATE TYPE enum_overlong AS ENUM ('" + label + "')", "42602", `invalid enum label "` + label + `"`, "", "Labels must be 63 bytes or less.", "", "", "execute"})
	}
	for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		for _, input := range []struct{ query, position string }{
			{"SELECT 'ok'::mood " + op + " 'ok'::happiness", "19"},
			{"SELECT NULL::mood " + op + " NULL::happiness", "19"},
			{"SELECT m " + op + " h FROM enum_rows WHERE id=99", "10"},
		} {
			entries = append(entries, expectation{input.query, "42883", "operator does not exist: mood " + op + " happiness", input.position, "", operatorHint, "", "prepare"})
		}
	}
	for _, e := range entries {
		if query == e.query {
			return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: e.code, Message: e.message, Position: e.position, Detail: e.detail, Hint: e.hint, Context: e.context}, e.stage
		}
	}
	t.Fatalf("missing independently observed enum diagnostic for %q", query)
	return referenceDiagnostic{}, ""
}
