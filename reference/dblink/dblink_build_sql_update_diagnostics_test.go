package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLUpdateDiagnostics(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		updateKeys(t, p, b, c)
		held := updateHeld(t, p, c, updateCall)
		for _, tc := range []struct {
			relation, vector              string
			count                         int64
			source, target, code, message string
		}{
			{"update_keys", "1", 0, "{}", "{}", "22023", "number of key attributes must be > 0"},
			{"update_keys", "1", -1, "{1}", "{9}", "22023", "number of key attributes must be > 0"},
			{"update_keys", "", 1, "{}", "{}", "22023", "number of key attributes must be > 0"},
			{"update_keys", "0", 1, "{}", "{}", "22023", "invalid attribute number 0"},
			{"update_keys", "-1", 1, "{1}", "{9}", "22023", "invalid attribute number -1"},
			{"update_keys", "4", 1, "{1}", "{9}", "22023", "invalid attribute number 4"},
			{"update_keys", "1 4", 2, "{}", "{}", "22023", "invalid attribute number 4"},
			{"update_keys", "1 2", 2, "{1}", "{}", "2202E", "source key array length must match number of key attributes"},
			{"update_keys", "1", 1, "{1,a}", "{}", "2202E", "source key array length must match number of key attributes"},
			{"update_keys", "1", 1, "{}", "{9}", "2202E", "source key array length must match number of key attributes"},
			{"update_keys", "1", 1, "{1}", "{}", "2202E", "target key array length must match number of key attributes"},
			{"update_keys", "1", 1, "{1}", "{9,b}", "2202E", "target key array length must match number of key attributes"},
			{"update_keys", "1", 1, "{99}", "{9}", "21000", "source row not found"},
			{"update_keys", "1 1", 2, "{1,2}", "{9,99}", "21000", "source row not found"},
			{"missing_update_relation", "0", 0, "{}", "{}", "42P01", `relation "missing_update_relation" does not exist`},
			{"bad..name", "1", 1, "{1}", "{9}", "42602", "invalid name syntax"},
		} {
			// ImplicitError asserts every exposed field, including absent detail,
			// hint, positions, internal query, context and object fields.
			s := updateError(updateCall, tc.code, tc.message, tc.relation, tc.vector, tc.count, tc.source, tc.target)
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("INSERT INTO update_keys VALUES(3,'a','duplicate')", 1))
		s := updateError(updateCall, "21000", "source criteria matched more than one record", "update_keys", "2", int64(1), "{a}", "{b}")
		updateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, tc := range []struct{ vector, code, message string }{
			{"1 x", "22P02", `invalid input syntax for type smallint: "x"`},
			{"32768", "22003", `value "32768" is out of range for type smallint`},
		} {
			q := "SELECT dbupd_ext.dblink_build_sql_update('update_keys','" + tc.vector + "'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
			d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(strings.Index(q, "'"+tc.vector+"'") + 1)}
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			d.Position, d.Context = "", "unnamed portal parameter $2 = '...'"
			s = fixture.ImplicitError(updateCall, d, "update_keys", tc.vector, int64(1), "{1}", "{9}")
			s.SelectRows, s.ErrorStage = true, "execute"
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			s.Args[0] = nil
			updateBoth(t, p, b, c, s)
		}
		q := "SELECT dbupd_ext.dblink_build_sql_update(NULL::text,'1 x'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
		s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type smallint: "x"`, Position: fixture.Position(strings.Index(q, "'1 x'") + 1)})
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		for _, arg := range []int{3, 4} {
			d := fixture.Diagnostic{Code: "22P02", Message: `malformed array literal: "not-array"`, Detail: `Array value must start with "{" or dimension information.`, Context: "unnamed portal parameter $" + []string{"4", "5"}[arg-3] + " = '...'"}
			args := []any{"update_keys", "1", int64(1), "{1}", "{9}"}
			args[arg] = "not-array"
			s := fixture.ImplicitError(updateCall, d, args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			s.Args[0] = nil
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			parts := []string{"'update_keys'", "'1'::int2vector", "1", "'{1}'::text[]", "'{9}'::text[]"}
			parts[arg] = "'not-array'::text[]"
			q := "SELECT dbupd_ext.dblink_build_sql_update(" + strings.Join(parts, ",") + ") AS sql"
			d.Context = ""
			d.Position = fixture.Position(strings.Index(q, "'not-array'") + 1)
			s = fixture.ImplicitError(q, d)
			s.SelectRows = true
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
		// The physical dropped slot survives in the SPI query, but never in
		// UPDATE output. Position is relative to that exact internal SELECT.
		run(fixture.Exec("CREATE TABLE update_dropped(gone text,id integer,payload text)", 0))
		run(fixture.Exec("ALTER TABLE update_dropped DROP COLUMN gone", 0))
		run(fixture.Exec("INSERT INTO update_dropped VALUES(1,'live')", 1))
		internal := "SELECT NULL, id, payload FROM update_dropped WHERE id = 'bad'"
		s = updateError(updateCall, "22P02", `invalid input syntax for type integer: "bad"`, "update_dropped", "1", int64(1), "{bad}", "{9}")
		s.Diagnostic.InternalQuery = internal
		s.Diagnostic.InternalPosition = fixture.Position(strings.Index(internal, "'bad'") + 1)
		updateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SAVEPOINT update_error", 0))
			s := updateError(updateCall, "21000", "source row not found", "update_keys", "1", int64(1), "{99}", "{9}")
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("ROLLBACK TO update_error", 0))
			run(fixture.Exec("RELEASE update_error", 0))
			run(updateSelect(updateCall, "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'", "update_keys", "1", int64(1), "{1}", "{9}"))
		}
		run(fixture.Exec("ROLLBACK", 0))
	})
}
