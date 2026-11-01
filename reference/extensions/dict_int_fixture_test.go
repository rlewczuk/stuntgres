package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

const dictIntLexizeSQL = "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"

func dictIntCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"dict_int_owner", "dict_int_denied"})
	fixture.SchemaReferenceCleanup(t, "dict_int_addon", "dict_int_moved")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func dictIntRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func dictIntExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, count int64, args ...any) {
	t.Helper()
	dictIntRun(t, p, b, e, fixture.Exec(q, count, args...))
}

func dictIntSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, columns, types []string, rows [][]any, args ...any) {
	if b.PG {
		owned := make([][]any, len(rows))
		for i, row := range rows {
			owned[i] = append([]any(nil), row...)
			for j, typ := range types {
				if typ == "OID" {
					if oid, ok := row[j].(int64); ok {
						owned[i][j] = strconv.FormatInt(oid, 10)
					}
				}
			}
		}
		rows = owned
	}
	t.Helper()
	s := fixture.Select(q, columns, types, rows, args...)
	dictIntRun(t, p, b, e, s)
	dictIntRun(t, p, b, e, fixture.Prepared(s))
}

func dictIntLexemes(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, want any, dictionary, input any) {
	t.Helper()
	fixture.TSTemplateLexemes(t, p, e, b.Recorder, b.PG, dictIntLexizeSQL, want, dictionary, input)
}

func dictIntHeld(t *testing.T, p *fixture.Pair, c *sql.Conn) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, dictIntLexizeSQL)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: dictIntLexizeSQL}
}

func dictIntOptionText(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary string, options any) {
	t.Helper()
	dictIntSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary::oid", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{options}}, dictionary)
}

func dictIntError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, code, message string, prepared bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message))
	s.Prepared, s.ErrorStage = prepared, "execute"
	dictIntRun(t, p, b, e, s)
}

// Snapshot public catalog state, not a runtime-derived expected lexeme. The
// independent baseline assertions at each callsite precede this atomicity guard.
func dictIntRows(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func dictIntUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, before [][]any, q string, args ...any) {
	t.Helper()
	if after := dictIntRows(t, p, c, q, args...); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed operation changed public state: before=%#v after=%#v", before, after)
	}
}

func dictIntID(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, q, args...).Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("object identity", oid, err)
	}
	return oid
}
