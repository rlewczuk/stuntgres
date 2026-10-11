//go:build reference

package reference_test

import (
	"fmt"
	refFixture "stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFixtures(t *testing.T) {
	// Deliberately serialized: every fixture shares the one PostgreSQL public schema.
	fixtures := []struct {
		name  string
		steps []refFixture.Step
	}{
		{"smoke", []refFixture.Step{
			refFixture.Exec("CREATE TABLE people (id INTEGER NOT NULL, name TEXT, active BOOLEAN NOT NULL)", 0),
			refFixture.Prepared(refFixture.Exec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 2, "O'Brien", false)),
			refFixture.Prepared(refFixture.Exec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 1, "Ada", true)),
			refFixture.Select("SELECT id, name, active FROM people ORDER BY id", []string{"id", "name", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "Ada", true}, {int64(2), "O'Brien", false}}),
			refFixture.Exec("SELECT id, name, active FROM people ORDER BY id", 2),
			refFixture.Prepared(refFixture.Exec("SELECT id FROM people WHERE id = $1", 1, 1)),
		}},
		{"quoted_and_omitted_columns", []refFixture.Step{
			refFixture.Exec(`CREATE TABLE "Odd" ("Id" BIGINT NOT NULL, "a""b" TEXT, flag BOOL)`, 0),
			refFixture.Exec(`INSERT INTO "Odd" ("a""b", "Id") VALUES ('line
O''Brien\tail', 2), ('', 1)`, 2),
			refFixture.Select(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", `a"b`, "flag"}, []string{"INT8", "TEXT", "BOOL"}, [][]any{{int64(1), "", nil}, {int64(2), "line\nO'Brien\\tail", nil}}),
		}},
		{"null_equality_and_order", []refFixture.Step{
			refFixture.Exec("CREATE TABLE ordering (id INTEGER, label TEXT, flag BOOLEAN)", 0),
			refFixture.Exec("INSERT INTO ordering VALUES (NULL, NULL, NULL), (2, 'z', TRUE), (1, 'A', FALSE)", 3),
			refFixture.Select("SELECT id FROM ordering WHERE id = NULL", []string{"id"}, []string{"INT4"}, nil),
			refFixture.Prepared(refFixture.Select("SELECT id FROM ordering WHERE id = $1", []string{"id"}, []string{"INT4"}, nil, nil)),
			refFixture.Select("SELECT id FROM ordering ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}),
			refFixture.Select("SELECT id FROM ordering ORDER BY id DESC", []string{"id"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}}),
			refFixture.Select("SELECT label FROM ordering ORDER BY label", []string{"label"}, []string{"TEXT"}, [][]any{{"A"}, {"z"}, {nil}}),
			refFixture.Select("SELECT flag FROM ordering ORDER BY flag DESC", []string{"flag"}, []string{"BOOL"}, [][]any{{nil}, {true}, {false}}),
		}},
		{"integer_and_text_assignment", []refFixture.Step{
			refFixture.Exec("CREATE TABLE numbers (small INT4, big INT8, textvalue TEXT)", 0),
			refFixture.Exec("INSERT INTO numbers VALUES (-2147483648, -9223372036854775808, TRUE), (2147483647, 9223372036854775807, 42)", 2),
			refFixture.Prepared(refFixture.Exec("INSERT INTO numbers VALUES ($1, $2, $3)", 1, " +0007 ", []byte(" -8 "), false)),
			refFixture.Select("SELECT * FROM numbers ORDER BY small", []string{"small", "big", "textvalue"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true"}, {int64(7), int64(-8), "false"}, {int64(2147483647), int64(9223372036854775807), "42"}}),
			refFixture.Select("SELECT small FROM numbers WHERE small = ' +7 '", []string{"small"}, []string{"INT4"}, [][]any{{int64(7)}}),
		}},
		{"boolean_inputs", []refFixture.Step{
			refFixture.Exec("CREATE TABLE booleans (id INTEGER, value BOOLEAN)", 0),
			refFixture.Exec("INSERT INTO booleans VALUES (1, 't'), (2, 'F'), (3, ' ye '), (4, 'of'), (5, '1'), (6, '0'), (7, 'ON'), (8, 'n')", 8),
			refFixture.Prepared(refFixture.Exec("INSERT INTO booleans VALUES ($1, $2)", 1, 9, []byte(" true "))),
			refFixture.Select("SELECT * FROM booleans ORDER BY id", []string{"id", "value"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), true}, {int64(4), false}, {int64(5), true}, {int64(6), false}, {int64(7), true}, {int64(8), false}, {int64(9), true}}),
			refFixture.Error("INSERT INTO booleans VALUES (10, 'o')", "22P02"),
			refFixture.Error("INSERT INTO booleans VALUES (10, 'truth')", "22P02"),
			refFixture.Error("INSERT INTO booleans VALUES (10, 1)", "42804"),
			refFixture.Step{Query: "SELECT id FROM booleans WHERE value = 1", SelectRows: true, State: "42883"},
		}},
		{"catalog_errors", []refFixture.Step{
			refFixture.Error("SELECT missing FROM absent", "42P01"),
			refFixture.Exec("CREATE TABLE empty (id INTEGER)", 0),
			refFixture.Error("CREATE TABLE empty (other TEXT)", "42P07"),
			refFixture.Error("CREATE TABLE duplicate (x INTEGER, x TEXT)", "42701"),
			refFixture.Error("INSERT INTO empty (id, id) VALUES (1, 2)", "42701"),
			refFixture.Error("INSERT INTO empty (absent) VALUES (1)", "42703"),
			refFixture.Error("SELECT absent FROM empty", "42703"),
			refFixture.Error("SELECT id FROM empty WHERE absent = 1", "42703"),
			refFixture.Error("SELECT id FROM empty ORDER BY absent", "42703"),
			refFixture.Select("SELECT * FROM empty", []string{"id"}, []string{"INT4"}, nil),
		}},
		{"atomic_insert_errors", []refFixture.Step{
			refFixture.Exec("CREATE TABLE atomic_rows (id INTEGER NOT NULL, big BIGINT, flag BOOLEAN)", 0),
			refFixture.Error("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (NULL, 2, FALSE)", "23502"),
			refFixture.Error("INSERT INTO atomic_rows (big) VALUES (1)", "23502"),
			refFixture.Error("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2147483648, 2, FALSE)", "22003"),
			refFixture.Error("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2, '9223372036854775808', FALSE)", "22003"),
			refFixture.Error("INSERT INTO atomic_rows VALUES (-2147483649, 1, TRUE)", "22003"),
			refFixture.Error("INSERT INTO atomic_rows VALUES (1, '-9223372036854775809', TRUE)", "22003"),
			refFixture.Error("INSERT INTO atomic_rows (id, big, flag) VALUES (1, 2)", "42601"),
			refFixture.Step{Query: "SELECT id FROM atomic_rows WHERE id = TRUE", SelectRows: true, State: "42883"},
			refFixture.Error("INSERT INTO atomic_rows VALUES ('not an integer', 1, TRUE)", "22P02"),
			refFixture.Error("INSERT INTO atomic_rows VALUES ('1.2', 1, TRUE)", "22P02"),
			refFixture.Error("INSERT INTO atomic_rows VALUES (TRUE, 1, TRUE)", "42804"),
			refFixture.Prepared(refFixture.Error("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22003", "2147483648", 1, true)),
			refFixture.Prepared(refFixture.Error("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22P02", "x", 1, true)),
			refFixture.Select("SELECT * FROM atomic_rows ORDER BY id", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, nil),
			refFixture.Exec("INSERT INTO atomic_rows VALUES (-2147483648, -9223372036854775808, FALSE), (2147483647, 9223372036854775807, TRUE)", 2),
		}},
		{"repeated_parameters", []refFixture.Step{
			refFixture.Exec("CREATE TABLE repeated (a INTEGER, b INTEGER, label TEXT)", 0),
			refFixture.Exec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 7, "direct"),
			refFixture.Prepared(refFixture.Exec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 9, "prepared")),
			refFixture.Prepared(refFixture.Select("SELECT a, b, label FROM repeated WHERE b = $1 ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(9), "prepared"}}, "9")),
			refFixture.Select("SELECT * FROM repeated ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(7), "direct"}, {int64(9), int64(9), "prepared"}}),
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			pair := refFixture.NewPair(t)
			for i, step := range fixture.steps {
				t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) { pair.Step(t, step) })
			}
		})
	}
}
