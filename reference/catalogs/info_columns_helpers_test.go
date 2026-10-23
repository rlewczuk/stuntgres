package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var icColumns = strings.Fields(`table_catalog table_schema table_name column_name ordinal_position column_default is_nullable data_type character_maximum_length character_octet_length numeric_precision numeric_precision_radix numeric_scale datetime_precision interval_type interval_precision character_set_catalog character_set_schema character_set_name collation_catalog collation_schema collation_name domain_catalog domain_schema domain_name udt_catalog udt_schema udt_name scope_catalog scope_schema scope_name maximum_cardinality dtd_identifier is_self_referencing is_identity identity_generation identity_start identity_increment identity_maximum identity_minimum identity_cycle is_generated generation_expression is_updatable`)
var icDomains = strings.Fields(`S S S S N C Y C N N N N N N C N S S S S S S S S S S S S S S S N S Y Y C C C C C Y C C Y`)
var icTypes = func() []string {
	out := make([]string, len(icDomains))
	for i, domain := range icDomains {
		out[i] = map[string]string{"S": "NAME", "N": "INT4", "C": "VARCHAR", "Y": "VARCHAR"}[domain]
	}
	return out
}()
var icRoles = []string{"ic_owner", "ic_reader", "ic_member", "ic_set", "ic_group", "ic_super"}

type icCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func icScenario(t *testing.T, body func(*icCase)) {
	t.Helper()
	icConfiguredScenario(t, nil, body)
}

func icConfiguredScenario(t *testing.T, configure func(*stuntgres.Driver) error, body func(*icCase)) {
	t.Helper()
	for _, schema := range []string{"ic_scope", "ic_moved", "ic_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, icRoles)
	p := fixture.NewScenarioWithDriverConfig(t, configure)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS ic_scope CASCADE", "DROP SCHEMA IF EXISTS ic_moved CASCADE", "DROP SCHEMA IF EXISTS ic_private CASCADE", "DROP SERVER IF EXISTS ic_live CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ic_fdw CASCADE", "DROP EXTENSION IF EXISTS postgres_fdw CASCADE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Errorf("cleanup %s: %v", q, err)
					}
				}
			})
			c := &icCase{t: t, p: p, b: backend, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			c.exec("CREATE SCHEMA ic_scope")
			body(c)
		})
	}
}

func (c *icCase) run(x fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, x, c.b.Recorder, s)
}
func (c *icCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *icCase) row(table, column string, ordinal int, dataType, udt string, fields map[string]any) []any {
	return icRow(c.database, "ic_scope", table, column, ordinal, dataType, "pg_catalog", udt, fields)
}

// Every unspecified field is independently expected NULL, not copied from output.
// Ordinary identity_cycle=NO follows the released expression, including serial.
func icRow(database, schema, table, column string, ordinal int, dataType, udtSchema, udt string, fields map[string]any) []any {
	out := make([]any, 44)
	fixed := map[string]any{"table_catalog": database, "table_schema": schema, "table_name": table, "column_name": column, "ordinal_position": int64(ordinal), "is_nullable": "YES", "data_type": dataType, "udt_catalog": database, "udt_schema": udtSchema, "udt_name": udt, "dtd_identifier": strconv.Itoa(ordinal), "is_self_referencing": "NO", "is_identity": "NO", "identity_cycle": "NO", "is_generated": "NEVER", "is_updatable": "YES"}
	for name, value := range fields {
		fixed[name] = value
	}
	for name, value := range fixed {
		found := false
		for i, column := range icColumns {
			if column == name {
				out[i], found = value, true
				break
			}
		}
		if !found {
			panic("unknown columns expectation: " + name)
		}
	}
	return out
}
func icIntFields() map[string]any {
	return map[string]any{"numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}
}
func (c *icCase) integer(table, column string, ordinal int) []any {
	return c.row(table, column, ordinal, "integer", "int4", icIntFields())
}
func icPatch(row []any, fields map[string]any) []any {
	out := append([]any(nil), row...)
	for name, value := range fields {
		found := false
		for i, column := range icColumns {
			if column == name {
				out[i], found = value, true
				break
			}
		}
		if !found {
			panic("unknown columns patch: " + name)
		}
	}
	return out
}
func icProjection(pg bool, where string, rows [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(icColumns, ",")+" FROM information_schema.columns WHERE "+where+" ORDER BY table_schema,table_name,ordinal_position", icColumns, icTypes, rows)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		icMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		want := reflect.TypeFor[string]()
		if icDomains[column] == "N" {
			want = reflect.TypeFor[int64]()
		}
		if pg && icDomains[column] == "S" {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw row %d column %s=%T(%v), want %v", row, icColumns[column], value, value, want)
		}
		return true
	}
	return s
}
func icMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 44 {
		t.Fatalf("columns=%d want44", len(columns))
	}
	for i, col := range columns {
		scan := reflect.TypeFor[string]()
		length, lengthOK := int64(0), false
		switch icDomains[i] {
		case "S":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "N":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "C":
			length, lengthOK = math.MaxInt64, true
			if pg {
				length = -5
			}
		case "Y":
			length, lengthOK = 3, true
		}
		gotLength, gotLengthOK := col.Length()
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != icColumns[i] || col.DatabaseTypeName() != icTypes[i] || col.ScanType() != scan || gotLength != length || gotLengthOK != lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("metadata %d %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t; want %s/%s scan=%v length=%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), gotLength, gotLengthOK, precision, scale, decimalOK, nullable, nullableOK, icColumns[i], icTypes[i], scan, length, lengthOK, !pg, !pg)
		}
	}
}
func (c *icCase) hold(query string) fixture.HeldStatement {
	c.t.Helper()
	s, err := c.conn.PrepareContext(c.p.Ctx, query)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = s.Close() })
	return fixture.HeldStatement{Executor: c.conn, Statement: s, SQL: query}
}
func (c *icCase) check(table string, want ...[]any) {
	c.t.Helper()
	s := icProjection(c.b.PG, "table_schema='ic_scope' AND table_name='"+table+"'", want)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
func (c *icCase) consume(rows *sql.Rows, want [][]any) {
	c.t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	icMetadata(c.t, columns, c.b.PG)
	var got [][]any
	for rows.Next() {
		cells, dest := make([]any, 44), make([]any, 44)
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			c.t.Fatal(err)
		}
		for i := range cells {
			cells[i] = fixture.Normalize(cells[i])
		}
		got = append(got, cells)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
		c.t.Fatalf("rows=%#v want=%#v", got, want)
	}
	icMetadata(c.t, columns, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	icMetadata(c.t, columns, c.b.PG)
}
