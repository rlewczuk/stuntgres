package reference_test

import (
	"database/sql"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const endpointSQL = "SELECT inet_client_addr(),inet_client_port(),inet_server_addr(),inet_server_port()"

var endpointNames = []string{"inet_client_addr", "inet_client_port", "inet_server_addr", "inet_server_port"}
var endpointTypes = []string{"INET", "INT4", "INET", "INT4"}

type endpointSnapshot [4]any

func endpointScenario(t *testing.T, fn func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fn(t, p, b, c)
		})
	}
}

func endpointMetadata(t *testing.T, rows *sql.Rows, names []string, pg, computed bool) {
	t.Helper()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatalf("names %v: %v", got, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 4 {
		t.Fatalf("columns %v: %v", columns, err)
	}
	for i, col := range columns {
		if col.DatabaseTypeName() != endpointTypes[i] {
			t.Fatalf("type %d: %s", i, col.DatabaseTypeName())
		}
		typ := reflect.TypeFor[string]()
		if i%2 == 1 {
			typ = reflect.TypeFor[int64]()
		}
		if pg {
			typ = reflect.TypeFor[any]()
			if i%2 == 1 {
				typ = reflect.TypeFor[int32]()
			}
		}
		if col.ScanType() != typ {
			t.Fatalf("scan type %d: %v want %v", i, col.ScanType(), typ)
		}
		if computed {
			if nullable, known := col.Nullable(); nullable || known {
				t.Fatalf("computed nullable %d: %v %v", i, nullable, known)
			}
		}
		if n, ok := col.Length(); ok || n != 0 {
			t.Fatalf("length %d: %d %v", i, n, ok)
		}
		if precision, scale, ok := col.DecimalSize(); ok || precision != 0 || scale != 0 {
			t.Fatalf("decimal %d: %d %d %v", i, precision, scale, ok)
		}
	}
}

func endpointRead(t *testing.T, rows *sql.Rows, names []string, pg, computed bool) endpointSnapshot {
	t.Helper()
	defer rows.Close()
	endpointMetadata(t, rows, names, pg, computed)
	if !rows.Next() {
		t.Fatalf("missing endpoint row: %v", rows.Err())
	}
	var raw endpointSnapshot
	if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
		t.Fatal(err)
	}
	for i, value := range raw {
		if !pg {
			if value != nil {
				t.Fatalf("embedded endpoint %d: %#v", i, value)
			}
			continue
		}
		if i%2 == 0 {
			text, ok := value.([]byte)
			if !ok {
				t.Fatalf("TCP address %d: %#v", i, value)
			}
			address, err := netip.ParseAddr(string(text))
			if err != nil {
				prefix, e := netip.ParsePrefix(string(text))
				if e != nil || prefix.Bits() != prefix.Addr().BitLen() {
					t.Fatalf("TCP address %q: %v", text, err)
				}
				address = prefix.Addr()
			}
			if !address.IsValid() {
				t.Fatalf("invalid TCP address %q", text)
			}
			raw[i] = string(text)
		} else {
			port, ok := value.(int64)
			if !ok || port < 1 || port > 65535 {
				t.Fatalf("TCP port %d: %#v", i, value)
			}
		}
	}
	if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
		t.Fatalf("trailing endpoint result: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return raw
}
func endpointCapture(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) endpointSnapshot {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, endpointSQL)
	if err != nil {
		t.Fatal(err)
	}
	return endpointRead(t, rows, endpointNames, b.PG, true)
}
func endpointSame(t *testing.T, a, b endpointSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("endpoints changed: %#v -> %#v", a, b)
	}
}
func endpointQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, names []string, computed bool) endpointSnapshot {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	return endpointRead(t, rows, names, b.PG, computed)
}

func TestReferenceSessionEndpointsValues(t *testing.T) {
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, endpointSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for range 2 {
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			endpointSame(t, first, endpointRead(t, rows, endpointNames, b.PG, true))
			endpointSame(t, first, endpointCapture(t, p, b, c))
		}
		for _, exec := range []func() (sql.Result, error){func() (sql.Result, error) { return c.ExecContext(p.Ctx, endpointSQL) }, func() (sql.Result, error) { return stmt.ExecContext(p.Ctx) }} {
			result, err := exec()
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatalf("affected %d: %v", n, err)
			}
		}
		empty := endpointSQL + " WHERE false"
		emptyStmt, err := c.PrepareContext(p.Ctx, empty)
		if err != nil {
			t.Fatal(err)
		}
		defer emptyStmt.Close()
		for _, query := range []func() (*sql.Rows, error){func() (*sql.Rows, error) { return c.QueryContext(p.Ctx, empty) }, func() (*sql.Rows, error) { return emptyStmt.QueryContext(p.Ctx) }} {
			rows, err := query()
			if err != nil {
				t.Fatal(err)
			}
			endpointMetadata(t, rows, endpointNames, b.PG, true)
			if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
				t.Fatalf("nonempty result %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, exec := range []func() (sql.Result, error){func() (sql.Result, error) { return c.ExecContext(p.Ctx, empty) }, func() (sql.Result, error) { return emptyStmt.ExecContext(p.Ctx) }} {
			result, err := exec()
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 0 {
				t.Fatalf("empty affected %d %v", n, err)
			}
		}
		var a, s sql.NullString
		var ap, sp sql.NullInt64
		if err := c.QueryRowContext(p.Ctx, endpointSQL).Scan(&a, &ap, &s, &sp); err != nil {
			t.Fatal(err)
		}
		if a.Valid != b.PG || ap.Valid != b.PG || s.Valid != b.PG || sp.Valid != b.PG {
			t.Fatalf("optional endpoints %#v %#v %#v %#v", a, ap, s, sp)
		}
	})
}

func endpointConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, first endpointSnapshot) {
	t.Helper()
	var types [4]string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_typeof(inet_client_addr())::text,pg_typeof(inet_client_port())::text,pg_typeof(inet_server_addr())::text,pg_typeof(inet_server_port())::text").Scan(&types[0], &types[1], &types[2], &types[3]); err != nil {
		t.Fatal(err)
	}
	if types != [4]string{"inet", "integer", "inet", "integer"} {
		t.Fatal(types)
	}
	var nulls [4]bool
	if err := c.QueryRowContext(p.Ctx, "SELECT inet_client_addr() IS NULL,inet_client_port() IS NULL,inet_server_addr() IS NULL,inet_server_port() IS NULL").Scan(&nulls[0], &nulls[1], &nulls[2], &nulls[3]); err != nil {
		t.Fatal(err)
	}
	for _, v := range nulls {
		if v == b.PG {
			t.Fatalf("NULL predicates %v", nulls)
		}
	}
	fallback := endpointSnapshot{"192.0.2.1", int64(5432), "192.0.2.1", int64(5432)}
	rows, err := c.QueryContext(p.Ctx, "SELECT COALESCE(inet_client_addr(),'192.0.2.1'::inet) AS inet_client_addr,COALESCE(inet_client_port(),5432) AS inet_client_port,COALESCE(inet_server_addr(),'192.0.2.1'::inet) AS inet_server_addr,COALESCE(inet_server_port(),5432) AS inet_server_port")
	if err != nil {
		t.Fatal(err)
	}
	// COALESCE is non-NULL on both backends; inspect values independently of endpoint absence.
	endpointMetadata(t, rows, endpointNames, b.PG, true)
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var a, s string
	var ap, sp int64
	if err := rows.Scan(&a, &ap, &s, &sp); err != nil {
		t.Fatal(err)
	}
	got := endpointSnapshot{a, ap, s, sp}
	if b.PG {
		fallback = first
	}
	endpointSame(t, fallback, got)
	if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
		t.Fatalf("consumer trailing result %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceSessionEndpointsTypedConsumersAndQualifications(t *testing.T) {
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		endpointConsumers(t, p, b, c, first)
		for _, style := range []string{"%s()", "pg_catalog.%s()", "\"%s\"()", "pg_catalog.\"%s\"()", "upper"} {
			var calls []string
			for i, name := range endpointNames {
				call := fmt.Sprintf(style, name)
				if style == "upper" {
					call = strings.ToUpper(name) + "()"
				}
				calls = append(calls, call+" AS "+endpointNames[i])
			}
			endpointSame(t, first, endpointQuery(t, p, b, c, "SELECT "+strings.Join(calls, ","), endpointNames, true))
		}
		aliases := []string{"client_address", "client_port", "server_address", "server_port"}
		var calls []string
		for i, name := range endpointNames {
			calls = append(calls, name+"() AS "+aliases[i])
		}
		endpointSame(t, first, endpointQuery(t, p, b, c, "SELECT "+strings.Join(calls, ","), aliases, true))
		activityExec(t, p, c, "SET search_path = ''")
		defer activityExec(t, p, c, "RESET search_path")
		endpointSame(t, first, endpointCapture(t, p, b, c))
	})
}
