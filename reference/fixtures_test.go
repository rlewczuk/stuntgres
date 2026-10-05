//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceFixtures(t *testing.T) {
	// Deliberately serialized: every fixture shares the one PostgreSQL public schema.
	fixtures := []struct {
		name  string
		steps []referenceStep
	}{
		{"smoke", []referenceStep{
			referenceExec("CREATE TABLE people (id INTEGER NOT NULL, name TEXT, active BOOLEAN NOT NULL)", 0),
			referencePrepared(referenceExec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 2, "O'Brien", false)),
			referencePrepared(referenceExec("INSERT INTO people (id, name, active) VALUES ($1, $2, $3)", 1, 1, "Ada", true)),
			referenceSelect("SELECT id, name, active FROM people ORDER BY id", []string{"id", "name", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "Ada", true}, {int64(2), "O'Brien", false}}),
			referenceExec("SELECT id, name, active FROM people ORDER BY id", 2),
			referencePrepared(referenceExec("SELECT id FROM people WHERE id = $1", 1, 1)),
		}},
		{"quoted_and_omitted_columns", []referenceStep{
			referenceExec(`CREATE TABLE "Odd" ("Id" BIGINT NOT NULL, "a""b" TEXT, flag BOOL)`, 0),
			referenceExec(`INSERT INTO "Odd" ("a""b", "Id") VALUES ('line
O''Brien\tail', 2), ('', 1)`, 2),
			referenceSelect(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", `a"b`, "flag"}, []string{"INT8", "TEXT", "BOOL"}, [][]any{{int64(1), "", nil}, {int64(2), "line\nO'Brien\\tail", nil}}),
		}},
		{"null_equality_and_order", []referenceStep{
			referenceExec("CREATE TABLE ordering (id INTEGER, label TEXT, flag BOOLEAN)", 0),
			referenceExec("INSERT INTO ordering VALUES (NULL, NULL, NULL), (2, 'z', TRUE), (1, 'A', FALSE)", 3),
			referenceSelect("SELECT id FROM ordering WHERE id = NULL", []string{"id"}, []string{"INT4"}, nil),
			referencePrepared(referenceSelect("SELECT id FROM ordering WHERE id = $1", []string{"id"}, []string{"INT4"}, nil, nil)),
			referenceSelect("SELECT id FROM ordering ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}),
			referenceSelect("SELECT id FROM ordering ORDER BY id DESC", []string{"id"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}}),
			referenceSelect("SELECT label FROM ordering ORDER BY label", []string{"label"}, []string{"TEXT"}, [][]any{{"A"}, {"z"}, {nil}}),
			referenceSelect("SELECT flag FROM ordering ORDER BY flag DESC", []string{"flag"}, []string{"BOOL"}, [][]any{{nil}, {true}, {false}}),
		}},
		{"integer_and_text_assignment", []referenceStep{
			referenceExec("CREATE TABLE numbers (small INT4, big INT8, textvalue TEXT)", 0),
			referenceExec("INSERT INTO numbers VALUES (-2147483648, -9223372036854775808, TRUE), (2147483647, 9223372036854775807, 42)", 2),
			referencePrepared(referenceExec("INSERT INTO numbers VALUES ($1, $2, $3)", 1, " +0007 ", []byte(" -8 "), false)),
			referenceSelect("SELECT * FROM numbers ORDER BY small", []string{"small", "big", "textvalue"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true"}, {int64(7), int64(-8), "false"}, {int64(2147483647), int64(9223372036854775807), "42"}}),
			referenceSelect("SELECT small FROM numbers WHERE small = ' +7 '", []string{"small"}, []string{"INT4"}, [][]any{{int64(7)}}),
		}},
		{"boolean_inputs", []referenceStep{
			referenceExec("CREATE TABLE booleans (id INTEGER, value BOOLEAN)", 0),
			referenceExec("INSERT INTO booleans VALUES (1, 't'), (2, 'F'), (3, ' ye '), (4, 'of'), (5, '1'), (6, '0'), (7, 'ON'), (8, 'n')", 8),
			referencePrepared(referenceExec("INSERT INTO booleans VALUES ($1, $2)", 1, 9, []byte(" true "))),
			referenceSelect("SELECT * FROM booleans ORDER BY id", []string{"id", "value"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), true}, {int64(4), false}, {int64(5), true}, {int64(6), false}, {int64(7), true}, {int64(8), false}, {int64(9), true}}),
			referenceError("INSERT INTO booleans VALUES (10, 'o')", "22P02"),
			referenceError("INSERT INTO booleans VALUES (10, 'truth')", "22P02"),
			referenceError("INSERT INTO booleans VALUES (10, 1)", "42804"),
			referenceStep{query: "SELECT id FROM booleans WHERE value = 1", selectRows: true, state: "42883"},
		}},
		{"catalog_errors", []referenceStep{
			referenceError("SELECT missing FROM absent", "42P01"),
			referenceExec("CREATE TABLE empty (id INTEGER)", 0),
			referenceError("CREATE TABLE empty (other TEXT)", "42P07"),
			referenceError("CREATE TABLE duplicate (x INTEGER, x TEXT)", "42701"),
			referenceError("INSERT INTO empty (id, id) VALUES (1, 2)", "42701"),
			referenceError("INSERT INTO empty (absent) VALUES (1)", "42703"),
			referenceError("SELECT absent FROM empty", "42703"),
			referenceError("SELECT id FROM empty WHERE absent = 1", "42703"),
			referenceError("SELECT id FROM empty ORDER BY absent", "42703"),
			referenceSelect("SELECT * FROM empty", []string{"id"}, []string{"INT4"}, nil),
		}},
		{"atomic_insert_errors", []referenceStep{
			referenceExec("CREATE TABLE atomic_rows (id INTEGER NOT NULL, big BIGINT, flag BOOLEAN)", 0),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (NULL, 2, FALSE)", "23502"),
			referenceError("INSERT INTO atomic_rows (big) VALUES (1)", "23502"),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2147483648, 2, FALSE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (1, 1, TRUE), (2, '9223372036854775808', FALSE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (-2147483649, 1, TRUE)", "22003"),
			referenceError("INSERT INTO atomic_rows VALUES (1, '-9223372036854775809', TRUE)", "22003"),
			referenceError("INSERT INTO atomic_rows (id, big, flag) VALUES (1, 2)", "42601"),
			referenceStep{query: "SELECT id FROM atomic_rows WHERE id = TRUE", selectRows: true, state: "42883"},
			referenceError("INSERT INTO atomic_rows VALUES ('not an integer', 1, TRUE)", "22P02"),
			referenceError("INSERT INTO atomic_rows VALUES ('1.2', 1, TRUE)", "22P02"),
			referenceError("INSERT INTO atomic_rows VALUES (TRUE, 1, TRUE)", "42804"),
			referencePrepared(referenceError("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22003", "2147483648", 1, true)),
			referencePrepared(referenceError("INSERT INTO atomic_rows VALUES ($1, $2, $3)", "22P02", "x", 1, true)),
			referenceSelect("SELECT * FROM atomic_rows ORDER BY id", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, nil),
			referenceExec("INSERT INTO atomic_rows VALUES (-2147483648, -9223372036854775808, FALSE), (2147483647, 9223372036854775807, TRUE)", 2),
		}},
		{"repeated_parameters", []referenceStep{
			referenceExec("CREATE TABLE repeated (a INTEGER, b INTEGER, label TEXT)", 0),
			referenceExec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 7, "direct"),
			referencePrepared(referenceExec("INSERT INTO repeated VALUES ($1, $1, $2)", 1, 9, "prepared")),
			referencePrepared(referenceSelect("SELECT a, b, label FROM repeated WHERE b = $1 ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(9), "prepared"}}, "9")),
			referenceSelect("SELECT * FROM repeated ORDER BY a", []string{"a", "b", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(7), "direct"}, {int64(9), int64(9), "prepared"}}),
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			pair := newReferencePair(t)
			for i, step := range fixture.steps {
				t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) { pair.step(t, step) })
			}
		})
	}
}
