package reference_test

import (
	"database/sql"
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const tablespaceAbsent = "/tmp/stuntgres_tablespace_contract_absent/location"
const tablespacePhysicalMessage = "CREATE TABLESPACE physical locations are unsupported in embedded mode"

func tablespaceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				tablespaceContainer(t, p, "test ! -e /tmp/stuntgres_tablespace_contract_absent")
			}
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			body(t, p, b, c)
		})
	}
}

func tablespaceContainer(t *testing.T, p *fixture.Pair, script string) {
	t.Helper()
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("tablespace container setup exit %d: %s", code, body)
	}
}

func tablespaceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
}

func tablespaceDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func tablespaceError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want fixture.Diagnostic, parse bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want)
		s.Prepared, s.ErrorStage = prepared, "execute"
		if prepared && parse {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func tablespacePhysical(b fixture.Backend, path string) fixture.Diagnostic {
	if b.PG {
		return tablespaceDiagnostic("58P01", "directory \""+path+"\" does not exist")
	}
	return tablespaceDiagnostic("0A000", tablespacePhysicalMessage)
}

func tablespaceSQL(name, owner, path string) string {
	if owner != "" {
		owner = " OWNER " + owner
	}
	return "CREATE TABLESPACE " + name + owner + " LOCATION '" + strings.ReplaceAll(path, "'", "''") + "'"
}

var tablespaceColumns = []string{"oid", "spcname", "spcowner", "spcacl", "spcoptions"}
var tablespaceTypes = []string{"OID", "NAME", "OID", "_ACLITEM", "_TEXT"}

func tablespaceRows(b fixture.Backend) [][]any {
	// pq exposes OID bytes (normalized to strings); native uses int64.
	if b.PG {
		return [][]any{{"1663", "pg_default", "10", nil, nil}, {"1664", "pg_global", "10", nil, nil}}
	}
	return [][]any{{int64(1663), "pg_default", int64(10), nil, nil}, {int64(1664), "pg_global", int64(10), nil, nil}}
}

func tablespaceStep(b fixture.Backend, query string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, tablespaceColumns, tablespaceTypes, rows, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		tablespaceMetadata(t, types, b.PG)
	}
	return s
}

func tablespaceMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != len(tablespaceColumns) {
		t.Fatal("tablespace descriptor width", len(types))
	}
	for i, typ := range types {
		if typ.Name() != tablespaceColumns[i] || typ.DatabaseTypeName() != tablespaceTypes[i] {
			t.Fatal("tablespace descriptor", i, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); known != !pg || nullable != (!pg && i >= 3) {
			t.Fatal("tablespace nullability", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatal("tablespace length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("tablespace decimal size", i, precision, scale, known)
		}
	}
}

func tablespaceBuiltins(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := tablespaceStep(b, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_catalog.pg_tablespace WHERE oid=1663 OR oid=1664 ORDER BY oid", tablespaceRows(b))
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func tablespaceHeldReads(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, tc := range []struct {
		column string
		values []any
	}{
		{"spcname", []any{"pg_default", "ts_contract_absent", nil, "pg_global", "pg_default"}},
		{"oid", []any{int64(1663), int64(0), nil, int64(1664), int64(1663)}},
	} {
		q := "SELECT * FROM pg_catalog.pg_tablespace WHERE " + tc.column + "=$1"
		stmt, err := e.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
		for i, value := range tc.values {
			want := [][]any{}
			switch i {
			case 0, 4:
				want = tablespaceRows(b)[:1]
			case 3:
				want = tablespaceRows(b)[1:]
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceStep(b, q, want, value))
		}
		stmt.Close()
	}
}

func tablespaceNoUserRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT spcname FROM pg_tablespace WHERE spcname LIKE 'ts_contract_%' ORDER BY spcname", []string{"spcname"}, []string{"NAME"}, [][]any{}))
}

func tablespaceRole(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, attributes string) {
	t.Helper()
	tablespaceExec(t, p, b, c, "CREATE ROLE "+name+" "+attributes)
	t.Cleanup(func() {
		if _, err := b.DB.ExecContext(p.Ctx, "DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
}
