package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func infoSQLIdentifierScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			for _, q := range []string{
				"SET search_path TO public",
				"CREATE TABLE public.info_sql_identifier_witness(id integer,v information_schema.sql_identifier)",
			} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
			}
			body(t, p, backend, conn, recorder)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %#v", notices)
			}
		})
	}
}

func infoSQLIdentifierColumnTypes(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
		t.Fatalf("metadata name=%s type=%s scan=%v length=%d,%v decimal=%d,%d,%v nullable=%v,%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
	}
}

func infoSQLIdentifierRows(pg bool, query string, want [][]any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"NAME"}, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoSQLIdentifierColumnTypes(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		t.Helper()
		expected := want[row][column]
		if expected == nil {
			if value != nil {
				t.Fatalf("raw NULL=%T(%v)", value, value)
			}
			return true
		}
		if pg {
			got, ok := value.([]byte)
			if !ok || string(got) != expected.(string) {
				t.Fatalf("raw NAME=%T(%v) want []byte(%q)", value, value, expected)
			}
		} else if got, ok := value.(string); !ok || got != expected.(string) {
			t.Fatalf("raw NAME=%T(%v) want string(%q)", value, value, expected)
		}
		return true
	}
	return s
}

func infoSQLIdentifierBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func infoSQLIdentifierStored(pg bool, want [][]any) fixture.Step {
	return infoSQLIdentifierRows(pg, "SELECT v FROM public.info_sql_identifier_witness ORDER BY id", want)
}

type infoSQLIdentifierValuer struct {
	value driver.Value
	calls *int
}

func (v infoSQLIdentifierValuer) Value() (driver.Value, error) {
	*v.calls++
	return v.value, nil
}

func TestReferenceInfoSQLIdentifierValues(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, prepared := range []bool{false, true} {
			run := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
			}
			run(fixture.Exec("INSERT INTO public.info_sql_identifier_witness VALUES (1,'MiXeD'),(2,NULL)", 2))
			run(fixture.Exec("INSERT INTO public.info_sql_identifier_witness VALUES (3,$1)", 1, nil))
			run(fixture.Exec("INSERT INTO public.info_sql_identifier_witness(id) VALUES (4)", 1))
			want := [][]any{{"MiXeD"}, {nil}, {nil}, {nil}}
			q := "INSERT INTO public.info_sql_identifier_witness VALUES ($1,$2)"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			id := 5
			for _, tc := range []struct{ input, output string }{
				{"", ""}, {"MiXeD", "MiXeD"}, {" x  ", " x  "}, {"O'Brien\\tail\n", "O'Brien\\tail\n"}, {"世界", "世界"},
				{strings.Repeat("a", 63), strings.Repeat("a", 63)},
				{strings.Repeat("a", 64), strings.Repeat("a", 63)},
				{strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"},
				{strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
				{strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)},
				{strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"},
				{strings.Repeat("é", 32), strings.Repeat("é", 31)},
			} {
				for mode := range 5 {
					calls := 0
					var input any = tc.input
					if mode == 1 {
						input = []byte(tc.input)
					} else if mode == 2 {
						input = infoSQLIdentifierValuer{[]byte(tc.input), &calls}
					}
					if mode == 3 {
						run(fixture.Exec("INSERT INTO public.info_sql_identifier_witness VALUES ($1,'"+strings.ReplaceAll(tc.input, "'", "''")+"')", 1, id))
					} else if mode == 4 {
						run(fixture.Exec(q, 1, id, input))
					} else {
						fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, id, input))
					}
					if mode == 2 && calls != 1 {
						t.Fatalf("Valuer calls=%d", calls)
					}
					want = append(want, []any{tc.output})
					id++
				}
			}
			owned := []byte("Owned")
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, id, owned))
			owned[0] = 'X'
			want = append(want, []any{"Owned"})
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			run(infoSQLIdentifierStored(backend.PG, want))
			run(fixture.Exec("DELETE FROM public.info_sql_identifier_witness", int64(len(want))))
		}
	})
}

func TestReferenceInfoSQLIdentifierMetadata(t *testing.T) {
	infoSQLIdentifierScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_sql_identifier_witness VALUES (1,'MiXeD'),(2,NULL)", 2))
		for _, s := range []fixture.Step{
			infoSQLIdentifierStored(backend.PG, [][]any{{"MiXeD"}, {nil}}),
			infoSQLIdentifierRows(backend.PG, "SELECT v FROM public.info_sql_identifier_witness WHERE id=2", [][]any{{nil}}),
			infoSQLIdentifierRows(backend.PG, "SELECT v FROM public.info_sql_identifier_witness WHERE id=99", nil),
		} {
			infoSQLIdentifierBoth(t, p, conn, recorder, s)
			stmt, err := conn.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: s.Query}
				for range 2 {
					fixture.RunStep(t, p.Ctx, held, recorder, s)
				}
			}()
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, conn, s.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					infoSQLIdentifierColumnTypes(t, columns, backend.PG)
					count := 0
					for rows.Next() {
						var value any
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
						if count >= len(s.Rows) {
							t.Fatal("extra lifecycle row")
						}
						s.CheckRaw(t, 0, count, value)
						count++
					}
					if err := rows.Err(); err != nil || count != len(s.Rows) {
						t.Fatal("lifecycle rows", count, err)
					}
					infoSQLIdentifierColumnTypes(t, columns, backend.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					infoSQLIdentifierColumnTypes(t, columns, backend.PG)
				}()
			}
		}
	})
}
