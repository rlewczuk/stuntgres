package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent computed-column expectations extend, rather than replace, the
// existing sibling fixture. pq's INT4 ScanType follows docs/TESTING.md.
func regLookupRelTypeSelect(b fixture.Backend, query string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := regLookupSelect(b, query, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "REGTYPE" {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, value)
		return true
	}
	prior := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		prior(t, rows)
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			var scan reflect.Type
			switch types[i] {
			case "REGCLASS", "REGNAMESPACE", "REGTYPE":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			default:
				continue
			}
			if column.ScanType() != scan {
				t.Errorf("%s ScanType=%v want %v", names[i], column.ScanType(), scan)
			}
			n, nk := column.Nullable()
			l, lk := column.Length()
			precision, scale, dk := column.DecimalSize()
			if n || nk || l != 0 || lk || precision != 0 || scale != 0 || dk {
				t.Errorf("%s computed flags nullable=%t/%t length=%d/%t decimal=%d,%d/%t", names[i], n, nk, l, lk, precision, scale, dk)
			}
		}
	}
	return s
}

// Each route is its own leaf: an expected missing-native failure or one unknown
// oracle diagnostic cannot prevent the other routes/backends from executing.
func regLookupRelTypeRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, input, kind string, output any) {
	t.Helper()
	t.Run("literal", func(t *testing.T) {
		q := "SELECT " + name + "(" + regObjectLiteral(input) + ") AS value"
		regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, q, []string{"value"}, []string{kind}, [][]any{{output}}))
	})
	for _, formal := range []string{"$1", "$1::text"} {
		t.Run(formal, func(t *testing.T) {
			q := "SELECT " + name + "(" + formal + ") AS value"
			s := regLookupRelTypeSelect(b, q, []string{"value"}, []string{kind}, [][]any{{output}}, input)
			regObjectBoth(t, p, b, c, s)
			held := regLookupHeld(t, p, c, q)
			defer held.Statement.Close()
			regObjectRunStep(t, p, b, held, s)
		})
	}
}

func TestReferenceRegLookupRelTypeValues(t *testing.T) {
	regLookupScenario(t, []string{"rl_values", "rl.Space"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rl_values", `CREATE SCHEMA "rl.Space"`,
			"CREATE TABLE rl_values.target(v integer)", `CREATE TABLE "rl.Space"."MiX"(v integer)`,
			`CREATE TABLE rl_values."a""b"(v integer)`, `CREATE TABLE rl_values."select"(v integer)`,
			`CREATE TABLE rl_values."23"(v integer)`, "CREATE VIEW rl_values.row_view AS SELECT v FROM rl_values.target",
			"CREATE INDEX rl_values_index ON rl_values.target(v)", "CREATE SEQUENCE rl_values.seq",
			"CREATE DOMAIN rl_values.domain_type AS varchar(12)", "CREATE TYPE rl_values.enum_type AS ENUM ('one','two')",
			"SET search_path TO rl_values,public")
		for _, tc := range []struct {
			name, input, kind string
			output            any
		}{
			{"to_regclass", "missing", "REGCLASS", nil},
			{"to_regclass", " RL_VALUES . TARGET ", "REGCLASS", "target"},
			{"to_regclass", `"rl.Space"."MiX"`, "REGCLASS", `"rl.Space"."MiX"`},
			{"to_regclass", `rl_values."a""b"`, "REGCLASS", `"a""b"`},
			{"to_regclass", `rl_values."select"`, "REGCLASS", `"select"`},
			{"to_regclass", `"23"`, "REGCLASS", `"23"`},
			{"to_regclass", "row_view", "REGCLASS", "row_view"},
			{"to_regclass", "rl_values_index", "REGCLASS", "rl_values_index"},
			{"to_regclass", "seq", "REGCLASS", "seq"},
			{"to_regnamespace", "rl_values", "REGNAMESPACE", "rl_values"},
			{"to_regnamespace", `"rl.Space"`, "REGNAMESPACE", `"rl.Space"`},
			{"to_regnamespace", "rl_missing", "REGNAMESPACE", nil},
			{"to_regtype", "integer", "REGTYPE", "integer"},
			{"to_regtype", "target", "REGTYPE", "target"},
			{"to_regtype", "row_view", "REGTYPE", "row_view"},
			{"to_regtype", "seq", "REGTYPE", nil},
			{"to_regtype", "domain_type", "REGTYPE", "domain_type"},
			{"to_regtype", "enum_type", "REGTYPE", "enum_type"},
			{"to_regtype", `"rl.Space"."MiX"`, "REGTYPE", `"rl.Space"."MiX"`},
			{"to_regtype", "023", "REGTYPE", "name"},
			{"to_regclass", " 23 ", "REGCLASS", `"23"`},
			{"to_regtype", `"23"`, "REGTYPE", `"23"`},
			{"to_regtype", `rl_values."a""b"`, "REGTYPE", `"a""b"`},
			{"to_regtypemod", "varchar(12)", "INT4", int64(16)},
			{"to_regtypemod", "integer", "INT4", int64(-1)},
		} {
			t.Run(tc.name+"/"+tc.input, func(t *testing.T) { regLookupRelTypeRoutes(t, p, b, c, tc.name, tc.input, tc.kind, tc.output) })
		}
		for _, tc := range []struct {
			name, kind, valid string
			output            any
		}{
			{"to_regclass", "REGCLASS", "target", "target"},
			{"to_regnamespace", "REGNAMESPACE", "rl_values", "rl_values"},
			{"to_regtype", "REGTYPE", "integer", "integer"},
			{"to_regtypemod", "INT4", "varchar(12)", int64(16)},
		} {
			t.Run(tc.name+"/held", func(t *testing.T) {
				for _, formal := range []string{"$1", "$1::text"} {
					q := "SELECT " + tc.name + "(" + formal + ") AS value"
					held := regLookupHeld(t, p, c, q)
					for _, v := range []struct{ input, output any }{{tc.valid, tc.output}, {[]byte(tc.valid), tc.output}, {"rl_missing", nil}, {nil, nil}, {tc.valid, tc.output}} {
						s := regLookupRelTypeSelect(b, q, []string{"value"}, []string{tc.kind}, [][]any{{v.output}}, v.input)
						regObjectBoth(t, p, b, c, s)
						regObjectRunStep(t, p, b, held, s)
					}
					held.Statement.Close()
				}
				for _, expr := range []string{"NULL", "NULL::text"} {
					q := "SELECT " + tc.name + "(" + expr + ")"
					regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, q, []string{tc.name}, []string{tc.kind}, [][]any{{nil}}))
				}
				q := "SELECT " + tc.name + "('rl_missing') AS empty WHERE FALSE"
				regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, q, []string{"empty"}, []string{tc.kind}, nil))
			})
		}
		for _, input := range []string{"0", "-", "4294967295", "010"} {
			t.Run("numeric/"+input, func(t *testing.T) {
				id := map[string]int64{"0": 0, "-": 0, "4294967295": 4294967295, "010": 8}[input]
				regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regclass($1)::oid::bigint AS c,to_regnamespace($1)::oid::bigint AS n,to_regtype($1)::oid::bigint AS t`, []string{"c", "n", "t"}, []string{"INT8", "INT8", "INT8"}, [][]any{{id, id, id}}, input))
			})
		}
		for _, tc := range []struct{ name, kind string }{{"to_regclass", "REGCLASS"}, {"to_regnamespace", "REGNAMESPACE"}, {"to_regtype", "REGTYPE"}} {
			for _, input := range []string{"0", "-", "4294967295"} {
				t.Run(tc.name+"/numeric-output/"+input, func(t *testing.T) {
					output := "-"
					if input == "4294967295" {
						output = input
					}
					regLookupRelTypeRoutes(t, p, b, c, tc.name, input, tc.kind, output)
				})
			}
		}
		for _, input := range []string{"", " \t ", ".s", "s.", "s..t", `"s`, `"s"tail`, `""`, "08", "4294967296", "-1", "+10", "0x10"} {
			for _, tc := range []struct{ name, kind string }{{"to_regclass", "REGCLASS"}, {"to_regnamespace", "REGNAMESPACE"}} {
				t.Run(tc.name+"/soft/"+input, func(t *testing.T) { regLookupRelTypeRoutes(t, p, b, c, tc.name, input, tc.kind, nil) })
			}
		}
		for _, input := range []string{"a.b", "x.y.z.w", "pg_temp"} {
			t.Run("namespace/"+input, func(t *testing.T) { regLookupRelTypeRoutes(t, p, b, c, "to_regnamespace", input, "REGNAMESPACE", nil) })
		}
		for _, input := range []string{"", " \t ", "SETOF integer", "rl_missing", "rl_missing[]", "unknown[]", "rl_absent.type", "rl_missing(1+2)", "08", "4294967296"} {
			t.Run("type/soft/"+input, func(t *testing.T) { regLookupRelTypeRoutes(t, p, b, c, "to_regtype", input, "REGTYPE", nil) })
		}
		for _, input := range []string{"", " \t ", "SETOF integer", "rl_missing", "rl_missing[]", "unknown[]", "rl_absent.type", "rl_missing(1+2)"} {
			t.Run("typmod/soft/"+input, func(t *testing.T) { regLookupRelTypeRoutes(t, p, b, c, "to_regtypemod", input, "INT4", nil) })
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		input := fixture.RegtypeIdentifier(database) + ".rl_values.target"
		regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass($1) AS c,to_regtype($1) AS t,to_regtypemod($1) AS m`, []string{"c", "t", "m"}, []string{"REGCLASS", "REGTYPE", "INT4"}, [][]any{{"target", "target", int64(-1)}}, input))
	})
}
