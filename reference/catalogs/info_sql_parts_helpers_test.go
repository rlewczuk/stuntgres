package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const spTable = "information_schema.sql_parts"
const spQuery = "SELECT feature_id,feature_name,is_supported,is_verified_by,comments FROM " + spTable

var spColumns = []string{"feature_id", "feature_name", "is_supported", "is_verified_by", "comments"}
var spTypes = []string{"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}

// Independently prescribed by REL_18_6 information_schema.sql, section 6.60.
// NO describes whole-part support; verification NULL and empty comments differ.
func spRows() [][]any {
	return [][]any{
		{"1", "Framework (SQL/Framework)", "NO", nil, ""},
		{"2", "Foundation (SQL/Foundation)", "NO", nil, ""},
		{"3", "Call-Level Interface (SQL/CLI)", "NO", nil, ""},
		{"4", "Persistent Stored Modules (SQL/PSM)", "NO", nil, ""},
		{"9", "Management of External Data (SQL/MED)", "NO", nil, ""},
		{"10", "Object Language Bindings (SQL/OLB)", "NO", nil, ""},
		{"11", "Information and Definition Schema (SQL/Schemata)", "NO", nil, ""},
		{"13", "Routines and Types Using the Java Programming Language (SQL/JRT)", "NO", nil, ""},
		{"14", "XML-Related Specifications (SQL/XML)", "NO", nil, ""},
		{"15", "Multi-Dimensional Arrays (SQL/MDA)", "NO", nil, ""},
		{"16", "Property Graph Queries (SQL/PGQ)", "NO", nil, ""},
	}
}

func spItem(id any) [][]any {
	for _, row := range spRows() {
		if row[0] == id {
			return [][]any{row}
		}
	}
	return nil
}

type spCase struct {
	t    *testing.T
	p    *fixture.Pair
	b    fixture.Backend
	conn *sql.Conn
}

func spScenario(t *testing.T, roles bool, body func(*spCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"sp_reader", "sp_super"})
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			body(&spCase{t: t, p: p, b: backend, conn: conn})
		})
	}
}

func (c *spCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *spCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *spCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: q}
}

func spMetadata(t *testing.T, columns []*sql.ColumnType, names []string, pg bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, col := range columns {
		length := int64(9223372036854775807)
		if pg {
			length = -5
		}
		if names[i] == "is_supported" {
			length = 3
		}
		gotLength, lengthOK := col.Length()
		nullable, nullableOK := col.Nullable()
		precision, scale, decimalOK := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != "VARCHAR" || col.ScanType() != reflect.TypeFor[string]() || gotLength != length || !lengthOK || nullable != !pg || nullableOK != !pg || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("column %d: name=%q type=%q scan=%v length=%d/%t nullable=%t/%t decimal=%d/%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), gotLength, lengthOK, nullable, nullableOK, precision, scale, decimalOK)
		}
	}
}

func (c *spCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, spColumns, spTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		spMetadata(t, columns, spColumns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 3 {
			if value != nil {
				t.Fatalf("verification raw=%T(%v), want nil", value, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("character raw=%T(%v), want string", value, value)
		}
		return true
	}
	return s
}

func (c *spCase) check(q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.projection(q, want, args...)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
