package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const siiTable = "information_schema.sql_implementation_info"
const siiQuery = "SELECT implementation_info_id,implementation_info_name,integer_value,character_value,comments FROM " + siiTable

var siiTypes = []string{"VARCHAR", "VARCHAR", "INT4", "VARCHAR", "VARCHAR"}

// Independently specified product identities, not native-derived oracle goldens.
func siiRows(pg bool) [][]any {
	product, version := "stuntgres", "PostgreSQL 18.0 compatible; embedded stuntgres"
	if pg {
		product, version = "PostgreSQL", "18.06.0000)"
	}
	return [][]any{
		{"10003", "CATALOG NAME", nil, "Y", nil},
		{"10004", "COLLATING SEQUENCE", nil, "C", nil},
		{"23", "CURSOR COMMIT BEHAVIOR", int64(1), nil, "close cursors and retain prepared statements"},
		{"2", "DATA SOURCE NAME", nil, "", nil},
		{"17", "DBMS NAME", nil, product, nil},
		{"18", "DBMS VERSION", nil, version, nil},
		{"26", "DEFAULT TRANSACTION ISOLATION", int64(2), nil, "READ COMMITTED; user-settable"},
		{"28", "IDENTIFIER CASE", int64(3), nil, "stored in mixed case - case sensitive"},
		{"85", "NULL COLLATION", int64(0), nil, "nulls higher than non-nulls"},
		{"13", "SERVER NAME", nil, "", nil},
		{"94", "SPECIAL CHARACTERS", nil, "", "all non-ASCII characters allowed"},
		{"46", "TRANSACTION CAPABLE", int64(2), nil, "both DML and DDL"},
	}
}

func siiItem(rows [][]any, id any) [][]any {
	return sfFilter(rows, func(row []any) bool { return row[0] == id })
}

type siiCase struct {
	t    *testing.T
	p    *fixture.Pair
	b    fixture.Backend
	conn *sql.Conn
	want [][]any
}

func siiScenario(t *testing.T, roles bool, body func(*siiCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"sii_owner", "sii_reader"})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
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
			body(&siiCase{t: t, p: p, b: b, conn: conn, want: siiRows(b.PG)})
		})
	}
}

func (c *siiCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *siiCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *siiCase) hold(q string) fixture.HeldStatement {
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

func siiMetadata(t *testing.T, columns []*sql.ColumnType, names []string, pg bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, col := range columns {
		typ, scan, length, lengthOK := "VARCHAR", reflect.TypeFor[string](), int64(9223372036854775807), true
		if pg {
			length = -5
		}
		if names[i] == "integer_value" {
			typ, scan, length, lengthOK = "INT4", reflect.TypeFor[int64](), 0, false
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		gotLength, gotLengthOK := col.Length()
		nullable, nullableOK := col.Nullable()
		precision, scale, decimalOK := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != typ || col.ScanType() != scan || gotLength != length || gotLengthOK != lengthOK || nullable != !pg || nullableOK != !pg || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("column %d: name=%q type=%q scan=%v length=%d/%t nullable=%t/%t decimal=%d/%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), gotLength, gotLengthOK, nullable, nullableOK, precision, scale, decimalOK)
		}
	}
}

func (c *siiCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.SIIColumns, siiTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		siiMetadata(t, columns, fixture.SIIColumns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value != nil {
			if column == 2 {
				if _, ok := value.(int64); !ok {
					t.Fatalf("integer raw=%T(%v)", value, value)
				}
			} else if _, ok := value.(string); !ok {
				t.Fatalf("character raw=%T(%v)", value, value)
			}
		}
		return true
	}
	return s
}

func (c *siiCase) check(e fixture.Executor, q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.projection(q, want, args...)
	c.run(e, s)
	c.run(c.conn, fixture.Prepared(s))
}

func siiPrivateSeed(c *siiCase) {
	c.t.Helper()
	c.exec("CREATE ROLE sii_owner", "CREATE ROLE sii_reader", "GRANT USAGE ON SCHEMA public TO sii_owner, sii_reader", "GRANT CREATE ON SCHEMA public TO sii_owner", "SET SESSION AUTHORIZATION sii_owner", "CREATE TABLE public.sii_private(id integer)", "RESET SESSION AUTHORIZATION")
}

func (c *siiCase) privateDenied() {
	c.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT * FROM public.sii_private", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sii_private"))
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		c.run(c.conn, s)
	}
}
