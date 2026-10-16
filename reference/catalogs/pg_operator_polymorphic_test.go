package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGOperatorPolymorphicRelationships(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgOperatorInventory(t, p, b)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_compatible(sa SMALLINT[],ia INTEGER[],sv SMALLINT,iv INTEGER,txt TEXT,ta TEXT[])", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_compatible VALUES('{1}','{2}',1,2,'x','{y,z}')", 1))
			for _, tc := range []struct {
				name, expression, typ, value string
				oid                          int64
			}{
				{"concat-promotes-left", "sa || ia", "_INT4", "{1,2}", 375},
				{"concat-promotes-right", "ia || sa", "_INT4", "{2,1}", 375},
				{"append-promotes-array", "sa || iv", "_INT4", "{1,2}", 349},
				{"append-promotes-element", "ia || sv", "_INT4", "{2,1}", 349},
				{"prepend-promotes-array", "iv || sa", "_INT4", "{2,1}", 374},
				{"prepend-promotes-element", "sv || ia", "_INT4", "{1,2}", 374},
				{"text-nonarray", "txt || iv", "TEXT", "x2", 2779},
				{"nonarray-text", "iv || txt", "TEXT", "2x", 2780},
				{"text-array-prepend", "txt || ta", "_TEXT", "{x,y,z}", 374},
				{"array-text-append", "ta || txt", "_TEXT", "{y,z,x}", 349},
			} {
				t.Run(tc.name, func(t *testing.T) {
					pgOperatorSignature(t, p, b, tc.oid)
					query := "SELECT (" + tc.expression + ") AS value FROM operator_compatible"
					pgOperatorExecutionRoutes(t, p, b, pgOperatorExecutionResult(t, b, query, tc.typ, tc.value))
				})
			}
			// Runtime common types never replace these declared pseudo identities.
			declarations := fixture.Select("SELECT oid,oprleft,oprright,oprresult FROM pg_operator WHERE oid IN (349,374,375,2779,2780) ORDER BY oid", []string{"oid", "oprleft", "oprright", "oprresult"}, []string{"OID", "OID", "OID", "OID"}, [][]any{
				{int64(349), int64(5078), int64(5077), int64(5078)},
				{int64(374), int64(5077), int64(5078), int64(5078)},
				{int64(375), int64(5078), int64(5078), int64(5078)},
				{int64(2779), int64(25), int64(2776), int64(25)},
				{int64(2780), int64(2776), int64(25), int64(25)},
			})
			fixture.PgDescriptionRows(t, p, b, declarations)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(declarations))
			for _, q := range []string{
				"CREATE TABLE operator_invalid_arrays(a INTEGER[],b TEXT[])",
				"INSERT INTO operator_invalid_arrays VALUES('{1}','{1}')",
				"CREATE TYPE operator_left_enum AS ENUM('a','b')",
				"CREATE TYPE operator_right_enum AS ENUM('a','b')",
				"CREATE TABLE operator_invalid_enums(a operator_left_enum,b operator_right_enum)",
				"INSERT INTO operator_invalid_enums VALUES('a','a')",
				"CREATE TABLE operator_invalid_ranges(a INT4RANGE,b BOOLEAN,d DATERANGE)",
				"INSERT INTO operator_invalid_ranges VALUES('[1,3)',true,'[2024-01-01,2024-01-03)')",
			} {
				count := int64(0)
				if len(q) >= 6 && q[:6] == "INSERT" {
					count = 1
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, count))
			}
			for _, tc := range []struct{ query, message string }{
				{"SELECT a @> b AS value FROM operator_invalid_arrays", "operator does not exist: integer[] @> text[]"},
				{"SELECT a = b AS value FROM operator_invalid_enums", "operator does not exist: operator_left_enum = operator_right_enum"},
				{"SELECT a @> b AS value FROM operator_invalid_ranges", "operator does not exist: int4range @> boolean"},
				{"SELECT a && d AS value FROM operator_invalid_ranges", "operator does not exist: int4range && daterange"},
			} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s.Prepared, s.ErrorStage = true, "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			pgOperatorInventory(t, p, b)
		})
	}
}
