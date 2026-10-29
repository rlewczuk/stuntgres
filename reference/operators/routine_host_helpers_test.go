package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hostScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	hostNativeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *hostNativeRuntime) {
		body(t, p, b, c)
	})
}

func hostExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("host SQL: %s", q)
		}
	}()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
}

func hostRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("host query: %s; args=%v", q, args)
		}
	}()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, names, types, want, args...))
}

func hostFunction(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, program, python, options string, transform bool) {
	t.Helper()
	language, source, clause := "go_host", program, ""
	if b.PG {
		language, source = "plpython3u", python
		if transform {
			clause = " TRANSFORM FOR TYPE hstore"
		}
	}
	q := "CREATE OR REPLACE FUNCTION " + name + "(v hstore) RETURNS hstore LANGUAGE " + language + clause + " " + options + " AS '" + strings.ReplaceAll(source, "'", "''") + "'"
	hostExec(t, p, b, c, q)
}

const hostIncrementPython = "assert isinstance(v, dict)\nv['a'] = str(int(v.get('a', '0')) + 1)\nv['c'] = '3'\nreturn v"
const hostDoublePython = "assert isinstance(v, dict)\nv['a'] = str(int(v['a']) * 2)\nv['d'] = 'second'\nreturn v"

// Compare each(hstore) as an ordered key/value bag, never serialized key order.
// The routine is evaluated once via the subquery; None entries remain SQL NULL.
func hostBag(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, expression string, want [][]any, args ...any) {
	t.Helper()
	q := "SELECT key,value FROM each(" + expression + ") ORDER BY key"
	hostRows(t, p, b, c, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, want, args...)
}

func hostMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 {
		t.Fatalf("columns=%v", columns)
	}
	col := columns[0]
	typeName, scan := "HSTORE", reflect.TypeFor[string]()
	if b.PG {
		typeName, scan = "", reflect.TypeFor[any]()
	}
	if col.Name() != "v" || col.DatabaseTypeName() != typeName || col.ScanType() != scan {
		t.Errorf("metadata name=%q type=%q scan=%v", col.Name(), col.DatabaseTypeName(), col.ScanType())
	}
	// These callers select computed expressions, not declared table/view columns.
	// Match the existing hstore metadata contract: both drivers report unknown.
	if n, known := col.Nullable(); n || known {
		t.Errorf("computed nullable=%v/%v want false/false", n, known)
	}
	if n, ok := col.Length(); n != 0 || ok {
		t.Errorf("length=%d/%v", n, ok)
	}
	if n, s, ok := col.DecimalSize(); n != 0 || s != 0 || ok {
		t.Errorf("decimal=%d/%d/%v", n, s, ok)
	}
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw != nil {
			if b.PG {
				if _, ok := raw.([]byte); !ok {
					t.Errorf("raw=%T", raw)
				}
			} else if _, ok := raw.(string); !ok {
				t.Errorf("raw=%T", raw)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// ColumnType values retained across cursor closure must not change.
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if col.Name() != "v" || col.DatabaseTypeName() != typeName || col.ScanType() != scan {
		t.Fatal("retained metadata changed")
	}
}

// All diagnostic fields, including empty fields, are independently expected.
// Discovery mismatch prints the full observed record and continues other arms;
// root resolves exact Python traceback/context before authorizing production.
func hostError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	hostErrorAtStage(t, p, c, q, want, "execute", args...)
}

func hostErrorAtStage(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, want fixture.Diagnostic, preparedStage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var value any
		var err error
		stage := "execute"
		if prepared {
			stmt, prepareErr := c.PrepareContext(p.Ctx, q)
			if prepareErr != nil {
				err, stage = prepareErr, "prepare"
			} else {
				err = stmt.QueryRowContext(p.Ctx, args...).Scan(&value)
				stmt.Close()
			}
		} else {
			err = c.QueryRowContext(p.Ctx, q, args...).Scan(&value)
		}
		if err == nil {
			t.Errorf("%q prepared=%v unexpectedly returned %#v", q, prepared, value)
			continue
		}
		wantStage := "execute"
		if prepared {
			wantStage = preparedStage
		}
		if stage != wantStage {
			t.Errorf("%q prepared=%v error stage=%s want %s", q, prepared, stage, wantStage)
		}
		got := fixture.ExtractDiagnostic(t, err)
		if got != want {
			t.Errorf("%s %q prepared=%v diagnostic=%#v; independently expected=%#v", stage, q, prepared, got, want)
		}
	}
}

func hostPythonError(code, message, name string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Context = fmt.Sprintf("PL/Python function \"%s\"", name)
	return d
}

func hostRuntimeLanguage(b fixture.Backend) string {
	if b.PG {
		return "plpython3u"
	}
	return "go_host"
}
