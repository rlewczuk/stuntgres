package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// A column added inside a transaction keeps a journal fill: the value attached
// to rows that predate the ADD COLUMN. Replacing that fill's enum type in the
// same transaction - create a replacement enum, convert the column, drop the old
// type and rename the replacement into its place - must retype the fill onto the
// surviving column type. PostgreSQL 18.6 accepts every scenario below; the
// defect aborted the process on the RENAME. Rows, column names, type names and
// diagnostics are the pinned PostgreSQL 18.6 oracle captures, never native
// output.
func TestReferenceEnumAlterFillReown(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, scenario := range enumFillScenarios {
			t.Run(scenario.name, func(t *testing.T) {
				for _, route := range enumFillRoutes {
					t.Run(route.name, func(t *testing.T) {
						for _, step := range scenario.steps(enumFillNames(route.suffix)) {
							// Transaction control stays direct; every DDL/DML or
							// SELECT step runs through the route under test.
							if route.prepared && !enumFillControl(step.Query) {
								step.Prepared = true
							}
							fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, step)
						}
					})
				}
			})
		}
	})
}

// enumFillObject resolves one logical object name to the current route's name,
// so direct and prepared routes share a schema without colliding.
type enumFillObject func(string) string

func enumFillNames(suffix string) enumFillObject {
	return func(base string) string { return base + suffix }
}

type enumFillRoute struct {
	name, suffix string
	prepared     bool
}

var enumFillRoutes = []enumFillRoute{
	{"direct", "", false},
	{"prepared", "_p", true},
}

type enumFillScenario struct {
	name  string
	steps func(enumFillObject) []fixture.Step
}

var enumFillScenarios = []enumFillScenario{
	{"replacement", enumFillReplacement},
	{"labels", enumFillLabels},
	{"default", enumFillDefaultValue},
	{"later_projection", enumFillLaterProjection},
	{"array", enumFillArray},
	{"array_default", enumFillArrayDefault},
	{"replacement_add_value", enumFillReplacementAddValue},
}

func enumFillControl(query string) bool {
	switch strings.ToUpper(strings.TrimSpace(query)) {
	case "BEGIN", "COMMIT", "ROLLBACK":
		return true
	}
	return false
}

func enumFillExec(query string, affected int64) fixture.Step {
	return fixture.Exec(query, affected)
}

func enumFillSelect(query string, columns, types []string, rows [][]any) fixture.Step {
	return fixture.Select(query, columns, types, rows)
}

// Case A: the exact round-2 repro. The replacement enum is renamed into the
// dropped type's name, so the added column's NULL fill must follow it.
func enumFillReplacement(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("grin_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("grin_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("grin_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("grin_pu")+" ADD COLUMN x "+o("grin_lt"), 0),
		enumFillExec("CREATE TYPE "+o("grin_new_lt")+" AS ENUM ('a','b','c')", 0),
		enumFillExec("ALTER TABLE "+o("grin_pu")+" ALTER COLUMN x TYPE "+o("grin_new_lt")+" USING (x::text::"+o("grin_new_lt")+")", 0),
		enumFillExec("DROP TYPE "+o("grin_lt"), 0),
		enumFillExec("ALTER TYPE "+o("grin_new_lt")+" RENAME TO "+o("grin_lt"), 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing FROM "+o("grin_pu")+" ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("grin_lt")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{a,b,c}"}}),
		enumFillSelect("SELECT pg_typeof(x)::text AS v FROM "+o("grin_pu"), []string{"v"}, []string{"TEXT"}, [][]any{{o("grin_lt")}}),
	}
}

// Case B: a NULL fill survives ALTER TYPE ADD VALUE and RENAME VALUE on the
// relation carrying it.
func enumFillLabels(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("fv_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("fv_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("fv_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("fv_pu")+" ADD COLUMN x "+o("fv_lt"), 0),
		enumFillExec("ALTER TYPE "+o("fv_lt")+" ADD VALUE 'c'", 0),
		enumFillExec("ALTER TYPE "+o("fv_lt")+" RENAME VALUE 'a' TO 'z'", 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing FROM "+o("fv_pu")+" ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("fv_lt")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,b,c}"}}),
	}
}

// Case C: a non-NULL default fill remains the converted default after label and
// type renames.
func enumFillDefaultValue(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("nv_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("nv_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("nv_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("nv_pu")+" ADD COLUMN x "+o("nv_lt")+" DEFAULT 'a'", 0),
		enumFillExec("ALTER TYPE "+o("nv_lt")+" ADD VALUE 'c'", 0),
		enumFillExec("ALTER TYPE "+o("nv_lt")+" RENAME VALUE 'a' TO 'z'", 0),
		enumFillExec("ALTER TYPE "+o("nv_lt")+" RENAME TO "+o("nv_renamed"), 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing FROM "+o("nv_pu")+" ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "z", false}}),
		enumFillSelect("SELECT pg_typeof(x)::text AS v FROM "+o("nv_pu"), []string{"v"}, []string{"TEXT"}, [][]any{{o("nv_renamed")}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("nv_renamed")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,b,c}"}}),
	}
}

// Case D: a later projection must consume the reowned fill rather than fail the
// added-column projection invariant.
func enumFillLaterProjection(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("lp_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("lp_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("lp_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("lp_pu")+" ADD COLUMN x "+o("lp_lt"), 0),
		enumFillExec("CREATE TYPE "+o("lp_new_lt")+" AS ENUM ('a','b','c')", 0),
		enumFillExec("ALTER TABLE "+o("lp_pu")+" ALTER COLUMN x TYPE "+o("lp_new_lt")+" USING (x::text::"+o("lp_new_lt")+")", 0),
		enumFillExec("DROP TYPE "+o("lp_lt"), 0),
		enumFillExec("ALTER TYPE "+o("lp_new_lt")+" RENAME TO "+o("lp_lt"), 0),
		enumFillExec("ALTER TABLE "+o("lp_pu")+" ADD COLUMN w int DEFAULT 7", 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing, w FROM "+o("lp_pu")+" ORDER BY id", []string{"id", "v", "missing", "w"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(1), nil, true, int64(7)}}),
		enumFillSelect("SELECT pg_typeof(x)::text AS tv, pg_typeof(w)::text AS tw FROM "+o("lp_pu"), []string{"tv", "tw"}, []string{"TEXT", "TEXT"}, [][]any{{o("lp_lt"), "integer"}}),
	}
}

// Case F: the same replacement through an enum array type. The fill's element
// type is what makes this a separate reowning path.
func enumFillArray(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("ave_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("ave_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("ave_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("ave_pu")+" ADD COLUMN x "+o("ave_lt")+"[]", 0),
		enumFillExec("CREATE TYPE "+o("ave_new_lt")+" AS ENUM ('a','b','c')", 0),
		enumFillExec("ALTER TABLE "+o("ave_pu")+" ALTER COLUMN x TYPE "+o("ave_new_lt")+"[] USING (x::text[]::"+o("ave_new_lt")+"[])", 0),
		enumFillExec("DROP TYPE "+o("ave_lt"), 0),
		enumFillExec("ALTER TYPE "+o("ave_new_lt")+" RENAME TO "+o("ave_lt"), 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing FROM "+o("ave_pu")+" ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}}),
		enumFillSelect("SELECT pg_typeof(x)::text AS v FROM "+o("ave_pu"), []string{"v"}, []string{"TEXT"}, [][]any{{o("ave_lt") + "[]"}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("ave_lt")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{a,b,c}"}}),
	}
}

// Case G: a non-NULL array default fill converted by label across generation
// changes.
func enumFillArrayDefault(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("avd_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("avd_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("avd_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("avd_pu")+" ADD COLUMN x "+o("avd_lt")+"[] DEFAULT '{a}'", 0),
		enumFillExec("ALTER TYPE "+o("avd_lt")+" ADD VALUE 'c'", 0),
		enumFillExec("ALTER TYPE "+o("avd_lt")+" RENAME VALUE 'a' TO 'z'", 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v FROM "+o("avd_pu")+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "{z}"}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("avd_lt")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,b,c}"}}),
	}
}

// Case J: ADD VALUE after the replacement and rename publishes on the relation
// still carrying the reowned fill.
func enumFillReplacementAddValue(o enumFillObject) []fixture.Step {
	return []fixture.Step{
		enumFillExec("BEGIN", 0),
		enumFillExec("CREATE TYPE "+o("ja_lt")+" AS ENUM ('a','b')", 0),
		enumFillExec("CREATE TABLE "+o("ja_pu")+" (id int PRIMARY KEY)", 0),
		enumFillExec("INSERT INTO "+o("ja_pu")+" VALUES (1)", 1),
		enumFillExec("ALTER TABLE "+o("ja_pu")+" ADD COLUMN x "+o("ja_lt"), 0),
		enumFillExec("CREATE TYPE "+o("ja_new_lt")+" AS ENUM ('a','b','c')", 0),
		enumFillExec("ALTER TABLE "+o("ja_pu")+" ALTER COLUMN x TYPE "+o("ja_new_lt")+" USING (x::text::"+o("ja_new_lt")+")", 0),
		enumFillExec("DROP TYPE "+o("ja_lt"), 0),
		enumFillExec("ALTER TYPE "+o("ja_new_lt")+" RENAME TO "+o("ja_lt"), 0),
		enumFillExec("ALTER TYPE "+o("ja_lt")+" ADD VALUE 'd'", 0),
		enumFillExec("COMMIT", 0),
		enumFillSelect("SELECT id, x::text AS v, x IS NULL AS missing FROM "+o("ja_pu")+" ORDER BY id", []string{"id", "v", "missing"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, true}}),
		enumFillSelect("SELECT enum_range(NULL::"+o("ja_lt")+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{a,b,c,d}"}}),
		enumFillSelect("SELECT pg_typeof(x)::text AS v FROM "+o("ja_pu"), []string{"v"}, []string{"TEXT"}, [][]any{{o("ja_lt")}}),
	}
}
