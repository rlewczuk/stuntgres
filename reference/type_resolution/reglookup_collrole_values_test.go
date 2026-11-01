package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupScenario(t *testing.T, schemas, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	// Cleanup is LIFO: close the scenario, drop schemas (including ACLs), then roles.
	fixture.RoleReferenceCleanup(t, roles)
	for _, schema := range schemas {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			conn, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

// The existing regObject runner owns pq's missing REGCOLLATION type name and
// raw alias codecs. Add exact computed-result metadata for both lookup kinds.
func regLookupSelect(b fixture.Backend, query string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := regObjectSelect(query, names, types, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			if types[i] != "REGCOLLATION" && types[i] != "REGROLE" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
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

func regLookupHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func TestReferenceRegLookupCollRoleValues(t *testing.T) {
	regLookupScenario(t, []string{"cl_values", "cl.Space"}, []string{"cl_role", "Mixed.Lookup.Role", `cl"quote`, "select", "10"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA cl_values", `CREATE SCHEMA "cl.Space"`,
			`CREATE COLLATION cl_values.folded FROM pg_catalog."C"`,
			`CREATE COLLATION "cl.Space"."MiX" FROM pg_catalog."C"`,
			`CREATE COLLATION cl_values."a""b" FROM pg_catalog."C"`,
			`CREATE COLLATION cl_values."select" FROM pg_catalog."C"`,
			`CREATE COLLATION cl_values."10" FROM pg_catalog."C"`,
			"CREATE ROLE cl_role", `CREATE ROLE "Mixed.Lookup.Role"`, `CREATE ROLE "cl""quote"`, `CREATE ROLE "select"`, `CREATE ROLE "10"`,
			"SET search_path TO cl_values,public")
		for _, s := range []fixture.Step{
			regLookupSelect(b, `SELECT to_regcollation('pg_catalog."C"') AS c,to_regcollation('"POSIX"') AS p,to_regcollation('default') AS d,to_regrole('pg_monitor') AS r`, []string{"c", "p", "d", "r"}, []string{"REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGROLE"}, [][]any{{`"C"`, `"POSIX"`, `"default"`, "pg_monitor"}}),
			fixture.Select(`SELECT to_regcollation('pg_catalog."C"')::oid::bigint AS c,to_regcollation('"POSIX"')::oid::bigint AS p,to_regcollation('default')::oid::bigint AS d,to_regrole('pg_monitor')::oid::bigint AS r,to_regrole(current_user::text)::oid::bigint AS bootstrap`, []string{"c", "p", "d", "r", "bootstrap"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(950), int64(951), int64(100), int64(3373), int64(10)}}),
			regLookupSelect(b, `SELECT to_regcollation(' CL_VALUES . FOLDED ') AS f,to_regcollation('"cl.Space"."MiX"') AS m,to_regcollation('cl_values."a""b"') AS q,to_regcollation('cl_values."select"') AS k,to_regcollation('cl_values."10"') AS n`, []string{"f", "m", "q", "k", "n"}, []string{"REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION"}, [][]any{{"folded", `"cl.Space"."MiX"`, `"a""b"`, `"select"`, `"10"`}}),
			regLookupSelect(b, `SELECT to_regrole(' CL_ROLE ') AS f,to_regrole('"Mixed.Lookup.Role"') AS m,to_regrole('"cl""quote"') AS q,to_regrole('"select"') AS k,to_regrole('"10"') AS n`, []string{"f", "m", "q", "k", "n"}, []string{"REGROLE", "REGROLE", "REGROLE", "REGROLE", "REGROLE"}, [][]any{{"cl_role", `"Mixed.Lookup.Role"`, `"cl""quote"`, `"select"`, `"10"`}}),
			fixture.Select(`SELECT to_regcollation('cl_values."10"')::oid='cl_values."10"'::regcollation::oid AS c,to_regrole('"10"')::oid='"10"'::regrole::oid AS r,to_regrole('10')::oid::bigint AS numeric`, []string{"c", "r", "numeric"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, true, int64(10)}}),
			regLookupSelect(b, `SELECT to_regcollation(NULL) AS c,to_regrole(NULL) AS r,to_regcollation(NULL::text) AS ct,to_regrole(NULL::text) AS rt`, []string{"c", "r", "ct", "rt"}, []string{"REGCOLLATION", "REGROLE", "REGCOLLATION", "REGROLE"}, [][]any{{nil, nil, nil, nil}}),
			regLookupSelect(b, `SELECT to_regcollation('cr_absent') AS c,to_regrole('cr_absent') AS r WHERE FALSE`, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, nil),
		} {
			regObjectBoth(t, p, b, c, s)
		}
		q := "SELECT to_regcollation($1::text) AS c,to_regrole($2::text) AS r"
		held := regLookupHeld(t, p, c, q)
		defer held.Statement.Close()
		for _, tc := range []struct {
			c, r any
			cv, rv any
		}{
			{`pg_catalog."C"`, "pg_monitor", `"C"`, "pg_monitor"},
			{"0", "0", "-", "-"}, {"-", "-", "-", "-"},
			{"4294967295", "4294967295", "4294967295", "4294967295"},
			{"cl_values.folded", "cl_role", "folded", "cl_role"},
			{"cl_values.\"10\"", `"10"`, `"10"`, `"10"`},
			{` cl_values . "10" `, " 10 ", `"10"`, `"10"`},
			{nil, nil, nil, nil}, {"cr_absent", "cr_absent", nil, nil},
			{"C", "Mixed.Lookup.Role", nil, nil},
			{"cl_missing_schema.c", "foo.bar", nil, nil},
			{"cr_absent", "x.y.z.w", nil, nil},
			{`pg_catalog."C"`, "pg_monitor", `"C"`, "pg_monitor"},
		} {
			s := regLookupSelect(b, q, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{tc.cv, tc.rv}}, tc.c, tc.r)
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, held, s)
		}
		for _, input := range []string{"0", "-", "4294967295", "010"} {
			id := map[string]int64{"0": 0, "-": 0, "4294967295": 4294967295, "010": 8}[input]
			regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regcollation($1::text)::oid::bigint AS c,to_regrole($1::text)::oid::bigint AS r`, []string{"c", "r"}, []string{"INT8", "INT8"}, [][]any{{id, id}}, input))
		}
		for _, input := range []string{"", " \t ", ".s", "s.", "s..t", `"s`, `"s"tail`, `""`, "08", "4294967296", "-1", "+10", "0x10"} {
			s := regLookupSelect(b, q, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{nil, nil}}, input, input)
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, held, s)
			literal := "SELECT to_regcollation("+regObjectLiteral(input)+") AS c,to_regrole("+regObjectLiteral(input)+") AS r"
			regObjectBoth(t, p, b, c, regLookupSelect(b, literal, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{nil, nil}}))
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT to_regcollation($1::text) AS c`, []string{"c"}, []string{"REGCOLLATION"}, [][]any{{`"C"`}}, fixture.RegtypeIdentifier(database)+`.pg_catalog."C"`))
	})
}
