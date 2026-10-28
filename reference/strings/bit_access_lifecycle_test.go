package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func bitAccessMetadata(t *testing.T, cols []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(cols) != 2 {
		t.Fatal(cols)
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if i == 0 {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		if col.Name() != []string{"g", "s"}[i] || col.DatabaseTypeName() != []string{"INT4", "BIT"}[i] || col.ScanType() != scan {
			t.Fatal("descriptor", col.Name(), col.DatabaseTypeName(), col.ScanType())
		}
		n, known := col.Length()
		if i == 0 && (n != 0 || known) || i == 1 && (n != -1 || !known) {
			t.Fatal("Length", n, known)
		}
		if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("DecimalSize", p, s, known)
		}
		if n, known := col.Nullable(); n || known {
			t.Fatal("Nullable", n, known)
		}
	}
}

func TestReferenceBitAccessNullMetadata(t *testing.T) {
	fixture.BitAccessScenario(t, bitAccessNullMetadata)
}
func bitAccessNullMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ba_meta(id INTEGER,f BIT(3),v VARBIT(5),n INTEGER,k INTEGER)", 0),
		fixture.Exec("INSERT INTO ba_meta VALUES(1,B'101',B'10110',0,0),(2,NULL,NULL,0,2),(3,B'101',B'10110',NULL,2),(4,B'101',B'10110',0,NULL)", 4),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	for _, source := range []string{"f", "v"} {
		for _, prepared := range []bool{false, true} {
			for _, id := range []int{1, 2, 3, 4, 99} {
				var want [][]any
				switch id {
				case 1:
					value := "001"
					if source == "v" {
						value = "00110"
					}
					want = [][]any{{int64(1), value}}
				case 2, 3:
					want = [][]any{{nil, nil}}
				case 4:
					want = [][]any{{int64(1), nil}}
				}
				s := fixture.Select("SELECT get_bit("+source+",n) AS g,set_bit("+source+",n,k) AS s FROM ba_meta WHERE id=$1", []string{"g", "s"}, []string{"INT4", "BIT"}, want, id)
				s.Prepared = prepared
				var saved []*sql.ColumnType
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					var err error
					saved, err = r.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					bitAccessMetadata(t, saved, b.PG)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				bitAccessMetadata(t, saved, b.PG)
			}
		}
	}
	for _, q := range []string{"SELECT get_bit(f,0) AS g,set_bit(f,0,2) AS s FROM ba_meta WHERE id=2", "SELECT get_bit(B'',NULL::integer) AS g,set_bit(B'',NULL::integer,2) AS s", "SELECT get_bit(f,2147483647) AS g,set_bit(B'',0,NULL::integer) AS s FROM ba_meta WHERE id=2"} {
		bitStringFunctionsBoth(t, p, b, c, fixture.Select(q, []string{"g", "s"}, []string{"INT4", "BIT"}, [][]any{{nil, nil}}))
	}
}

func TestReferenceBitAccessOwnership(t *testing.T) { fixture.BitAccessScenario(t, bitAccessOwnership) }
func bitAccessOwnership(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE ba_owned(v VARBIT)", 0))
	source := []byte("100000001")
	run(fixture.Exec("INSERT INTO ba_owned VALUES($1)", 1, source))
	source[0] = '0'
	q := "SELECT v AS original,set_bit(v,0,0) AS changed,v AS again FROM ba_owned"
	s := fixture.Select(q, []string{"original", "changed", "again"}, []string{"VARBIT", "BIT", "VARBIT"}, [][]any{{"100000001", "000000001", "100000001"}})
	s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
		var a, changed, again []byte
		if err := r.Scan(&a, &changed, &again); err != nil {
			t.Fatal(err)
		}
		changed[0], a[0] = '1', '0'
		if string(again) != "100000001" {
			t.Fatal("aliased scan buffers", string(again))
		}
	}
	bitStringFunctionsBoth(t, p, b, c, s)
	run(fixture.Select("SELECT v FROM ba_owned", []string{"v"}, []string{"VARBIT"}, [][]any{{"100000001"}}))
	q = "SELECT get_bit($1,8) AS g,set_bit($1,8,0) AS s"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	input := []byte("100000001")
	r, err := stmt.QueryContext(p.Ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cols, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	bitAccessMetadata(t, cols, b.PG)
	input[0], input[8] = '0', '0'
	if !b.PG {
		run(fixture.Exec("UPDATE ba_owned SET v=B'000000000'", 1))
	}
	if !r.Next() {
		t.Fatal(r.Err())
	}
	var g, setter any
	if err := r.Scan(&g, &setter); err != nil {
		t.Fatal(err)
	}
	fixture.AssertRawValue(t, b.PG, "INT4", g)
	fixture.AssertRawValue(t, b.PG, "BIT", setter)
	if g != int64(1) || fixture.Normalize(setter) != "100000000" {
		t.Fatal("unowned frame", g, setter)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal(r.Err())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	bitAccessMetadata(t, cols, b.PG)
	if !b.PG {
		run(fixture.Select("SELECT v FROM ba_owned", []string{"v"}, []string{"VARBIT"}, [][]any{{"000000000"}}))
	}
}
