package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func regObjectScenario(t *testing.T, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
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

func regObjectBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	if !b.PG && s.SelectRows && s.State == "" {
		prior := s.CheckMetadata
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			if prior != nil {
				prior(t, rows)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range columns {
				if column.DatabaseTypeName() == "REGCOLLATION" || column.DatabaseTypeName() == "REGNAMESPACE" {
					if column.ScanType() != reflect.TypeOf("") {
						t.Errorf("%s native ScanType = %v, want string", column.Name(), column.ScanType())
					}
				}
			}
		}
	}
	regObjectRunStep(t, p, b, c, s)
	regObjectRunStep(t, p, b, c, fixture.Prepared(s))
}

func regObjectRunStep(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	if b.PG && s.Types != nil {
		// pq has no catalog type-name entry for regcollation. Preserve its
		// observed empty metadata separately from the declared raw codec family.
		if s.RawTypes == nil {
			s.RawTypes = s.Types
		}
		s.Types = append([]string(nil), s.Types...)
		for i, name := range s.Types {
			if name == "REGCOLLATION" {
				s.Types[i] = ""
			}
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func regObjectExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, queries ...string) {
	t.Helper()
	for _, query := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, 0))
	}
}

func regObjectSelect(query string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, names, types, rows, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			if types[i] == "REGCOLLATION" || types[i] == "REGNAMESPACE" {
				// pq reports interface{}; native aliases promise the public string codec.
				if column.ScanType() != reflect.TypeOf((*any)(nil)).Elem() && column.ScanType() != reflect.TypeOf("") {
					t.Errorf("%s ScanType = %v, want string (native) or interface{} (pq)", names[i], column.ScanType())
				}
			}
		}
	}
	return s
}

func regObjectLiteral(input string) string {
	return "'" + strings.ReplaceAll(input, "'", "''") + "'"
}

func TestReferenceRegCollationInput(t *testing.T) {
	regObjectScenario(t, []string{"rc_input", "rc.Space"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA rc_input", `CREATE SCHEMA "rc.Space"`,
			`CREATE COLLATION rc_input.folded FROM pg_catalog."C"`,
			`CREATE COLLATION "rc.Space"."MiX" FROM pg_catalog."C"`,
			`CREATE COLLATION rc_input."a""b" FROM pg_catalog."C"`,
			`CREATE COLLATION rc_input."select" FROM pg_catalog."C"`,
			`CREATE COLLATION rc_input."10" FROM pg_catalog."C"`,
			"SET search_path TO rc_input,public")
		for _, s := range []fixture.Step{
			regObjectSelect(`SELECT 'pg_catalog."C"'::regcollation AS c,'951'::regcollation AS p,'default'::regcollation AS d`, []string{"c", "p", "d"}, []string{"REGCOLLATION", "REGCOLLATION", "REGCOLLATION"}, [][]any{{`"C"`, `"POSIX"`, `"default"`}}),
			regObjectSelect(`SELECT ' RC_INPUT . FOLDED '::regcollation AS f,'"rc.Space"."MiX"'::regcollation AS m,'rc_input."a""b"'::regcollation AS q,'rc_input."select"'::regcollation AS k,'rc_input."10"'::regcollation AS n`, []string{"f", "m", "q", "k", "n"}, []string{"REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION"}, [][]any{{"folded", `"rc.Space"."MiX"`, `"a""b"`, `"select"`, `"10"`}}),
			regObjectSelect(`SELECT '950'::regcollation AS c,'0'::regcollation AS z,'-'::regcollation AS d,'4294967295'::regcollation AS u,NULL::regcollation AS n`, []string{"c", "z", "d", "u", "n"}, []string{"REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION", "REGCOLLATION"}, [][]any{{`"C"`, "-", "-", "4294967295", nil}}),
			fixture.Select(`SELECT '010'::regcollation::bigint AS octal,'pg_catalog."C"'::regcollation::oid::regcollation::text AS roundtrip,(-1)::integer::regcollation::bigint AS unsigned`, []string{"octal", "roundtrip", "unsigned"}, []string{"INT8", "TEXT", "INT8"}, [][]any{{int64(8), `"C"`, int64(4294967295)}}),
			fixture.Select(`SELECT ('rc_input.folded'::regcollation::oid=oid) AS catalog_identity FROM pg_catalog.pg_collation WHERE collname='folded' AND collnamespace='rc_input'::regnamespace::oid`, []string{"catalog_identity"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			regObjectBoth(t, p, b, c, s)
		}
		var current string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&current); err != nil {
			t.Fatal(err)
		}
		regObjectBoth(t, p, b, c, regObjectSelect("SELECT $1::regcollation AS c", []string{"c"}, []string{"REGCOLLATION"}, [][]any{{`"C"`}}, fixture.RegtypeIdentifier(current)+`.pg_catalog."C"`))
		// Catalog identity is independent of whether the comparison provider is executable.
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT 'pg_catalog.unicode'::regcollation::bigint AS identity`, []string{"identity"}, []string{"INT8"}, [][]any{{int64(963)}}))
	})
}

func TestReferenceRegNamespaceInput(t *testing.T) {
	regObjectScenario(t, []string{"rn_input", "rn.Space", `rn"quote`, "select", "10"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rn_input", `CREATE SCHEMA "rn.Space"`, `CREATE SCHEMA "rn""quote"`, `CREATE SCHEMA "select"`, `CREATE SCHEMA "10"`)
		for _, s := range []fixture.Step{
			regObjectSelect(`SELECT 'pg_catalog'::regnamespace AS c,'11'::regnamespace AS n,' RN_INPUT '::regnamespace AS f,'"rn.Space"'::regnamespace AS d,'"rn""quote"'::regnamespace AS q,'"select"'::regnamespace AS k,'"10"'::regnamespace AS digits`, []string{"c", "n", "f", "d", "q", "k", "digits"}, []string{"REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE"}, [][]any{{"pg_catalog", "pg_catalog", "rn_input", `"rn.Space"`, `"rn""quote"`, `"select"`, `"10"`}}),
			regObjectSelect(`SELECT '0'::regnamespace AS z,'-'::regnamespace AS d,'4294967295'::regnamespace AS u,NULL::regnamespace AS n`, []string{"z", "d", "u", "n"}, []string{"REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE", "REGNAMESPACE"}, [][]any{{"-", "-", "4294967295", nil}}),
			fixture.Select(`SELECT '010'::regnamespace::bigint AS octal,'pg_catalog'::regnamespace::oid::regnamespace::text AS roundtrip,('RN_INPUT'::regnamespace::oid='"rn_input"'::regnamespace::oid) AS folded`, []string{"octal", "roundtrip", "folded"}, []string{"INT8", "TEXT", "BOOL"}, [][]any{{int64(8), "pg_catalog", true}}),
		} {
			regObjectBoth(t, p, b, c, s)
		}
		regObjectExec(t, p, b, c, "SET search_path TO pg_catalog")
		regObjectBoth(t, p, b, c, regObjectSelect("SELECT 'rn_input'::regnamespace AS n", []string{"n"}, []string{"REGNAMESPACE"}, [][]any{{"rn_input"}}))
	})
}
