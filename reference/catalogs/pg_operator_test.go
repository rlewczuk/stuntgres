package reference_test

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgOperatorColumns = []string{"oid", "oprname", "oprnamespace", "oprowner", "oprkind", "oprcanmerge", "oprcanhash", "oprleft", "oprright", "oprresult", "oprcom", "oprnegate", "oprcode", "oprrest", "oprjoin"}
var pgOperatorTypes = []string{"OID", "NAME", "OID", "OID", "CHAR", "BOOL", "BOOL", "OID", "OID", "OID", "OID", "OID", "REGPROC", "REGPROC", "REGPROC"}

func pgOperatorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgOperatorFacts(t *testing.T) [][]any {
	t.Helper()
	f, err := os.Open("../../docs/design/pg-operator-pg18-supported.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = '\t', 15, true
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 547 || !reflect.DeepEqual(records[0], pgOperatorColumns) {
		t.Fatalf("released inventory: %d records, want header plus 546", len(records))
	}
	var result [][]any
	var previous int64
	for _, record := range records[1:] {
		row := make([]any, 15)
		for i, cell := range record {
			switch i {
			case 1, 4:
				row[i] = cell
			case 5, 6:
				if cell != "t" && cell != "f" {
					t.Fatal("invalid released boolean", record)
				}
				row[i] = cell == "t"
			default:
				n, err := strconv.ParseInt(cell, 10, 64)
				if err != nil || n < 0 {
					t.Fatal("invalid released OID", record, err)
				}
				row[i] = n
			}
		}
		if row[0].(int64) <= previous {
			t.Fatal("unordered or duplicate released OID", record)
		}
		previous = row[0].(int64)
		result = append(result, row)
	}
	return result
}

func pgOperatorPredicate(facts [][]any) string {
	ids := make([]string, len(facts))
	for i, r := range facts {
		ids[i] = fmt.Sprint(r[0])
	}
	return "oid IN (" + strings.Join(ids, ",") + ")"
}

func pgOperatorInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	facts := pgOperatorFacts(t)
	q := "SELECT oid,oprname,oprnamespace,oprowner,oprkind,oprcanmerge,oprcanhash,oprleft,oprright,oprresult,oprcom,oprnegate,oprcode::oid AS oprcode,oprrest::oid AS oprrest,oprjoin::oid AS oprjoin FROM pg_catalog.pg_operator"
	if b.PG {
		q += " WHERE " + pgOperatorPredicate(facts)
	}
	q += " ORDER BY oid"
	types := append([]string(nil), pgOperatorTypes...)
	for i := 12; i < 15; i++ {
		types[i] = "OID"
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q, pgOperatorColumns, types, facts)
		s.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, s)
	}
}

func TestReferencePGOperatorInventory(t *testing.T) { pgOperatorScenario(t, pgOperatorInventory) }

func pgOperatorMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgOperatorColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 15 {
		t.Fatal(types, err)
	}
	pgOperatorSavedMetadata(t, types, pg)
}

func pgOperatorSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 15 {
		t.Fatal("saved metadata width", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[int64]()
		if i == 1 || i == 4 || i >= 12 {
			scan = reflect.TypeFor[string]()
		}
		if i == 5 || i == 6 {
			scan = reflect.TypeFor[bool]()
		}
		if pg && i != 5 && i != 6 {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != pgOperatorColumns[i] || c.DatabaseTypeName() != pgOperatorTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %q %q %v; want %q %q %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), pgOperatorColumns[i], pgOperatorTypes[i], scan)
		}
		if n, known := c.Nullable(); n || known == pg {
			t.Fatal("nullability", i, n, known)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("decimal", i, precision, scale, known)
		}
	}
}

func pgOperatorShape(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{"SELECT * FROM pg_catalog.pg_operator WHERE FALSE", "SELECT * FROM pg_operator WHERE oid=0", "SELECT * FROM pg_operator WHERE oid=551"} {
		var want [][]any
		if strings.HasSuffix(q, "551") {
			want = [][]any{{int64(551), "+", int64(11), int64(10), "b", false, false, int64(23), int64(23), int64(23), int64(551), int64(0), "int4pl", "-", "-"}}
		}
		s := fixture.Select(q, pgOperatorColumns, pgOperatorTypes, want)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgOperatorMetadata(t, rows, b.PG) }
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferencePGOperatorShape(t *testing.T) { pgOperatorScenario(t, pgOperatorShape) }

func TestReferencePGOperatorMetadata(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var want [][]any
		for i, name := range pgOperatorColumns {
			typ, collation := int64(26), int64(0)
			switch i {
			case 1:
				typ, collation = 19, 950
			case 4:
				typ = 18
			case 5, 6:
				typ = 16
			case 12, 13, 14:
				typ = 24
			}
			want = append(want, []any{int64(i + 1), name, typ, true, collation, int64(-1)})
		}
		s := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,attcollation,atttypmod FROM pg_attribute WHERE attrelid=2617 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "attcollation", "atttypmod"}, []string{"INT2", "NAME", "OID", "BOOL", "OID", "INT4"}, want)
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	})
}

func TestReferencePGOperatorCatalogLinks(t *testing.T) {
	pgOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		s := fixture.Select("SELECT oid,relnamespace,relowner,reltype,relnatts,relkind,relhasindex FROM pg_class WHERE oid=2617", []string{"oid", "relnamespace", "relowner", "reltype", "relnatts", "relkind", "relhasindex"}, []string{"OID", "OID", "OID", "OID", "INT2", "CHAR", "BOOL"}, [][]any{{int64(2617), int64(11), int64(10), int64(10009), int64(15), "r", true}})
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	})
}

func pgOperatorAliasText(t *testing.T) map[int64]string {
	t.Helper()
	f, err := os.Open("../../docs/design/regoperator-pg18-inventory.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.Comma, reader.FieldsPerRecord, reader.LazyQuotes = '\t', 8, true
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 800 {
		t.Fatal("incomplete alias evidence", len(rows))
	}
	names := map[int64]string{0: "0"}
	for _, row := range rows[1:] {
		oid, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := names[oid]; exists {
			t.Fatal("duplicate alias", oid)
		}
		names[oid] = row[7]
	}
	return names
}
