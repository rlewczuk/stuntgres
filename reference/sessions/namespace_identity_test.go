package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Namespace identity is physical-session state. Retire each case-owned connection
// rather than allowing a later fresh-session witness to inherit its TEMP state.
func namespaceConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Raw(func(any) error { return driver.ErrBadConn }); _ = c.Close() })
	return c
}

func namespaceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

// lib/pq exports OID as text bytes (normalized by the fixture to string),
// whereas the native driver exports its typed OID as int64.
func namespaceOIDCarrier(b fixture.Backend, oid int64) any {
	if b.PG {
		return strconv.FormatInt(oid, 10)
	}
	return oid
}

func namespaceMetadata(b fixture.Backend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			var scan reflect.Type
			switch column.DatabaseTypeName() {
			case "NAME", "_NAME":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "OID":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			default:
				continue
			}
			if column.ScanType() != scan {
				t.Fatal("namespace ScanType", column.Name(), column.ScanType(), scan)
			}
			switch column.Name() {
			case "current_schema", "current_schemas", "pg_my_temp_schema", "pg_is_other_temp_schema":
				nullable, known := column.Nullable()
				if b.PG {
					if nullable || known {
						t.Fatal("pq computed nullability", nullable, known)
					}
				} else if !known || nullable != (column.Name() != "pg_my_temp_schema") {
					t.Fatal("native namespace nullability", column.Name(), nullable, known)
				}
			}
		}
	}
}

func namespaceValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q, label, typ string, want any, args ...any) {
	t.Helper()
	if typ == "OID" && want != nil {
		want = namespaceOIDCarrier(b, want.(int64))
	}
	s := fixture.Select(q, []string{label}, []string{typ}, [][]any{{want}}, args...)
	s.CheckMetadata = namespaceMetadata(b)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func namespaceOID(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_my_temp_schema()").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

// Discover the descriptor independently of the function's returned schema name.
func namespacePair(t *testing.T, p *fixture.Pair, c *sql.Conn, oid int64) (string, int64) {
	t.Helper()
	if oid <= 0 {
		t.Fatal("missing TEMP identity", oid)
	}
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", oid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	suffix, ok := strings.CutPrefix(name, "pg_temp_")
	if !ok {
		t.Fatal("not a TEMP namespace", name)
	}
	if n, err := strconv.ParseUint(suffix, 10, 64); err != nil || n == 0 {
		t.Fatal("invalid TEMP suffix", name, err)
	}
	var toast int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname=$1", "pg_toast_temp_"+suffix).Scan(&toast); err != nil {
		t.Fatal(err)
	}
	if toast <= 0 || toast == oid {
		t.Fatal("invalid TEMP pair", oid, toast)
	}
	return name, toast
}

func TestReferenceNamespaceIdentitySignatures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := namespaceConn(t, p, b)
			namespaceExec(t, p, b, c, "SET search_path TO public")
			for _, tc := range []struct {
				q, label, typ string
				want          any
			}{
				{"SELECT current_schema", "current_schema", "NAME", "public"},
				{"SELECT current_schema()", "current_schema", "NAME", "public"},
				{"SELECT pg_catalog.current_schema()", "current_schema", "NAME", "public"},
				{"SELECT current_schemas(false)", "current_schemas", "_NAME", "{public}"},
				{"SELECT current_schemas(true)", "current_schemas", "_NAME", "{pg_catalog,public}"},
				{"SELECT current_schemas(NULL)", "current_schemas", "_NAME", nil},
				{"SELECT pg_my_temp_schema()", "pg_my_temp_schema", "OID", int64(0)},
				{"SELECT pg_catalog.pg_my_temp_schema()", "pg_my_temp_schema", "OID", int64(0)},
				{"SELECT pg_is_other_temp_schema(NULL)", "pg_is_other_temp_schema", "BOOL", nil},
				{"SELECT pg_catalog.pg_is_other_temp_schema(11)", "pg_is_other_temp_schema", "BOOL", false},
			} {
				namespaceValue(t, p, b, c, tc.q, tc.label, tc.typ, tc.want)
			}
			for _, oid := range []int64{0, 11, 2200, 4294967295} {
				var count int64
				if oid == 4294967295 {
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_namespace WHERE oid=$1", oid).Scan(&count); err != nil || count != 0 {
						t.Fatal("absent OID precondition", count, err)
					}
				}
				namespaceValue(t, p, b, c, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", false, oid)
			}
			held, err := c.PrepareContext(p.Ctx, "SELECT current_schemas($1),pg_is_other_temp_schema($2)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct{ flag, oid, paths, other any }{{false, int64(0), "{public}", false}, {true, int64(11), "{pg_catalog,public}", false}, {nil, nil, nil, nil}} {
				rows, err := held.QueryContext(p.Ctx, tc.flag, tc.oid)
				if err != nil {
					t.Fatal(err)
				}
				fixture.ArrayReferenceConsumeHeld(t, rows, [][]any{{tc.paths, tc.other}}, []string{"_NAME", "BOOL"})
			}
			namespaceExec(t, p, b, c, `SET search_path TO ''`)
			namespaceValue(t, p, b, c, "SELECT current_schema", "current_schema", "NAME", nil)
			namespaceValue(t, p, b, c, "SELECT current_schemas(false)", "current_schemas", "_NAME", "{}")
			namespaceValue(t, p, b, c, "SELECT current_schemas(true)", "current_schemas", "_NAME", "{pg_catalog}")
			empty := fixture.Select("SELECT pg_my_temp_schema() FROM pg_namespace WHERE oid=0", []string{"pg_my_temp_schema"}, []string{"OID"}, nil)
			empty.CheckMetadata = namespaceMetadata(b)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, empty)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(empty))
		})
	}
}

func TestReferenceNamespaceIdentityFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := namespaceConn(t, p, b), namespaceConn(t, p, b)
			for _, c := range []*sql.Conn{a, peer} {
				namespaceValue(t, p, b, c, "SELECT pg_my_temp_schema()", "pg_my_temp_schema", "OID", int64(0))
				namespaceExec(t, p, b, c, "SET search_path TO missing_namespace_identity,pg_temp,public")
				namespaceValue(t, p, b, c, "SELECT current_schemas(NULL)", "current_schemas", "_NAME", nil)
				if got := namespaceOID(t, p, c); got != 0 {
					t.Fatal("strict NULL initialized TEMP", got)
				}
			}
			// Different fresh sessions establish both causal initializer routes.
			var first string
			if err := a.QueryRowContext(p.Ctx, "SELECT current_schema").Scan(&first); err != nil {
				t.Fatal(err)
			}
			var paths string
			if err := peer.QueryRowContext(p.Ctx, "SELECT current_schemas(false)").Scan(&paths); err != nil {
				t.Fatal(err)
			}
			aid, bid := namespaceOID(t, p, a), namespaceOID(t, p, peer)
			an, at := namespacePair(t, p, a, aid)
			bn, bt := namespacePair(t, p, peer, bid)
			if aid == bid || first != an || paths != "{"+bn+",public}" {
				t.Fatal("initializer identity", aid, bid, first, paths, an, bn)
			}
			for _, tc := range []struct {
				c                                *sql.Conn
				name                             string
				own, ownToast, other, otherToast int64
			}{{a, an, aid, at, bid, bt}, {peer, bn, bid, bt, aid, at}} {
				namespaceValue(t, p, b, tc.c, "SELECT current_schema()", "current_schema", "NAME", tc.name)
				namespaceValue(t, p, b, tc.c, "SELECT current_schemas(false)", "current_schemas", "_NAME", "{"+tc.name+",public}")
				namespaceValue(t, p, b, tc.c, "SELECT current_schemas(true)", "current_schemas", "_NAME", "{pg_catalog,"+tc.name+",public}")
				for _, id := range []int64{tc.own, tc.ownToast, tc.other, tc.otherToast} {
					namespaceValue(t, p, b, tc.c, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", id == tc.other || id == tc.otherToast, id)
				}
			}
			namespaceExec(t, p, b, a, `SET search_path TO ''`)
			namespaceValue(t, p, b, a, "SELECT current_schema", "current_schema", "NAME", nil)
		})
	}
}
