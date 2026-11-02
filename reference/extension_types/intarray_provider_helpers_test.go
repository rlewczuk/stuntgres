package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarrayScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ia_provider")
	fixture.SchemaReferenceCleanup(t, "ia_collision")
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

func intarrayInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA ia_provider", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION intarray SCHEMA ia_provider VERSION '1.5'", 0))
}

func intarrayBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Prepared(s))
}

// Nullable is nil for expressions and contains declared nullability for tables.
// All public accessors are checked, including before an empty cursor is consumed.
func intarrayMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names, families []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("column count %d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		name, scan := families[i], reflect.TypeFor[string]()
		if families[i] == "INT4" {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		} else if pg {
			scan = reflect.TypeFor[any]()
			if families[i] == "QUERY_INT" || families[i] == "INTBIG_GKEY" || families[i] == "_INTBIG_GKEY" {
				name = ""
			}
		}
		n, nk := c.Nullable()
		length, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		if c.Name() != names[i] || c.DatabaseTypeName() != name || c.ScanType() != scan {
			t.Fatalf("metadata column %d got %q/%q/%v want %q/%q/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], name, scan)
		}
		wantNullable, wantKnown := false, false
		if !pg && nullable != nil {
			wantNullable, wantKnown = nullable[i], true
		}
		if n != wantNullable || nk != wantKnown || length != 0 || lk || precision != 0 || scale != 0 || dk {
			t.Fatalf("accessors %s nullable=%t/%t length=%d/%t decimal=%d/%d/%t", names[i], n, nk, length, lk, precision, scale, dk)
		}
	}
}

func intarraySelect(b fixture.Backend, query string, names, families []string, nullable []bool, values [][]any, args ...any) fixture.Step {
	types := append([]string(nil), families...)
	if b.PG {
		for i, typ := range types {
			if typ == "INTBIG_GKEY" || typ == "_INTBIG_GKEY" || typ == "QUERY_INT" {
				types[i] = ""
			}
		}
	}
	s := fixture.ArrayReferenceSelect(query, names, types, families, values, b.PG, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		intarrayMetadata(t, b.PG, columns, names, families, nullable)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		family := families[column]
		if family != "INTBIG_GKEY" && family != "_INTBIG_GKEY" && family != "QUERY_INT" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("raw column=%d row=%d got=%T want=%v", column, row, value, want)
			}
		}
		return true
	}
	return s
}

// Full records, including absent fields and driver preparation stages, are
// independently observed against the pinned PostgreSQL 18.6 fixture.
func intarrayError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, prepared bool, stage string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, strings.HasPrefix(query, "SELECT"), stage
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func intarrayOID(t *testing.T, p *fixture.Pair, c *sql.Conn, typ string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT ($1::regtype)::oid", typ).Scan(&oid); err != nil || oid <= 0 {
		t.Fatalf("type identity %s oid=%d error=%v", typ, oid, err)
	}
	return oid
}

func intarrayReject() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type intbig_gkey")
}

func intarrayParameterReject(input any) fixture.Diagnostic {
	return queryIntParameterDiagnostic("0A000", "cannot accept a value of type intbig_gkey", fixture.Normalize(input).(string), 1)
}

// Fresh installations use the independently observed fixture principal.
// Replayed dumps instead pass their source owner explicitly to every verifier.
func intarrayFixtureOwner(b fixture.Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return "postgres"
}

func intarrayIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) [4]int64 {
	t.Helper()
	var ids [4]int64
	for i, name := range []string{"ia_provider.query_int", "ia_provider._query_int", "ia_provider.intbig_gkey", "ia_provider._intbig_gkey"} {
		ids[i] = intarrayOID(t, p, c, name)
		for j := range i {
			if ids[i] == ids[j] {
				t.Fatalf("distinct provider types alias: %v", ids)
			}
		}
	}
	for _, tc := range []struct{ name, args, result string }{
		{"bqarr_in", "2275", fmt.Sprint(ids[0])},
		{"bqarr_out", fmt.Sprint(ids[0]), "2275"},
		{"_intbig_in", "2275", fmt.Sprint(ids[2])},
		{"_intbig_out", fmt.Sprint(ids[2]), "2275"},
	} {
		s := fixture.Select("SELECT p.proname,p.proargtypes::text AS args,p.prorettype::text AS result,p.proisstrict,p.provolatile,p.proparallel,l.lanname,p.prosrc,p.probin,r.rolname FROM (SELECT * FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace AND proname=$1) p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner", []string{"proname", "args", "result", "proisstrict", "provolatile", "proparallel", "lanname", "prosrc", "probin", "rolname"}, []string{"NAME", "TEXT", "TEXT", "BOOL", "CHAR", "CHAR", "NAME", "TEXT", "TEXT", "NAME"}, [][]any{{tc.name, tc.args, tc.result, true, "i", "s", "c", tc.name, "$libdir/_int", owner}}, tc.name)
		intarrayBoth(t, p, b, c, s)
	}
	return ids
}
