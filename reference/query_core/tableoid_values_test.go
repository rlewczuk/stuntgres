package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDValues(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE toi_values(a INT)", "CREATE TABLE toi_zero()")
		oid := tableoidCell(b, tableoidIdentity(t, p, c, "toi_values"))
		zero := tableoidCell(b, tableoidIdentity(t, p, c, "toi_zero"))
		metadata := func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range columns {
				if column.DatabaseTypeName() != "OID" {
					continue
				}
				length, lengthKnown := column.Length()
				precision, scale, decimalKnown := column.DecimalSize()
				nullable, nullableKnown := column.Nullable()
				if length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown || nullable || nullableKnown {
					t.Fatalf("computed OID descriptor: length=%d/%t decimal=%d,%d/%t nullable=%t/%t", length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown)
				}
			}
		}
		for _, q := range []string{"SELECT tableoid FROM toi_values", "SELECT q.tableoid FROM toi_values q"} {
			s := fixture.Select(q, []string{"tableoid"}, []string{"OID"}, nil)
			s.CheckMetadata = metadata
			tableoidRoutes(t, p, b, c, s)
		}
		query := "SELECT q.a,q.tableoid FROM toi_values q ORDER BY q.a NULLS LAST"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		empty := fixture.Select(query, []string{"a", "tableoid"}, []string{"INT4", "OID"}, nil)
		empty.CheckMetadata = metadata
		fixture.RunStep(t, p.Ctx, held, b.Recorder, empty)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_values VALUES(1),(NULL)", 2))
		want := [][]any{{int64(1), oid}, {nil, oid}}
		populated := fixture.Select(query, []string{"a", "tableoid"}, []string{"INT4", "OID"}, want)
		populated.CheckMetadata = metadata
		tableoidRoutes(t, p, b, c, populated)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, populated)
		for _, q := range []string{"SELECT * FROM toi_values ORDER BY a NULLS LAST", "SELECT q.* FROM toi_values q(x) ORDER BY x NULLS LAST"} {
			name := "a"
			if q == "SELECT q.* FROM toi_values q(x) ORDER BY x NULLS LAST" {
				name = "x"
			}
			tableoidRoutes(t, p, b, c, fixture.Select(q, []string{name}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_zero DEFAULT VALUES", 1))
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT z.tableoid FROM toi_zero z", []string{"tableoid"}, []string{"OID"}, [][]any{{zero}}))
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT z.* FROM toi_zero z", []string{}, []string{}, [][]any{{}}))
	})
}
