package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// nullable is nil for expressions and points to the physical column contract otherwise.
func macaddrMetadata(t *testing.T, rows *sql.Rows, pg bool, nullable *bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	c := columns[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "MACADDR" || c.ScanType() != scan {
		t.Fatalf("MACADDR metadata name=%s type=%s scan=%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if length, ok := c.Length(); length != 0 || ok {
		t.Fatal("unexpected MACADDR length", length, ok)
	}
	if precision, scale, ok := c.DecimalSize(); precision != 0 || scale != 0 || ok {
		t.Fatal("unexpected MACADDR decimal size", precision, scale, ok)
	}
	n, known := c.Nullable()
	if pg || nullable == nil {
		if n || known {
			t.Fatal("unknown nullable", n, known)
		}
	} else if !known || n != *nullable {
		t.Fatal("column nullable", n, known, *nullable)
	}
}

func TestReferenceMACADDRTransport(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_transport(id integer,v macaddr,required macaddr NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO mac_transport VALUES(1,'08002b010203','08002b010203'),(2,NULL,'ffffffffffff')", 2))
		yes, no := true, false
		for _, tc := range []struct {
			q        string
			rows     [][]any
			nullable *bool
		}{
			{"SELECT v FROM mac_transport ORDER BY id", [][]any{{macaddrSample}, {nil}}, &yes},
			{"SELECT v FROM mac_transport WHERE id=0", nil, &yes},
			{"SELECT v FROM mac_transport WHERE id=2", [][]any{{nil}}, &yes},
			{"SELECT required AS v FROM mac_transport WHERE id=1", [][]any{{macaddrSample}}, &no},
			{"SELECT NULL::macaddr AS v", [][]any{{nil}}, nil},
			{"SELECT '08002b010203'::macaddr AS v", [][]any{{macaddrSample}}, nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tc.q, []string{"v"}, []string{"MACADDR"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { macaddrMetadata(t, r, b.PG, tc.nullable) }
				run(s)
			}
		}
		q := "SELECT $1::macaddr AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ arg, want any }{
			{macaddrSample, macaddrSample}, {[]byte("0800.2b01.0203"), macaddrSample}, {macaddrValuer{"08002b010203"}, macaddrSample},
			{nil, nil}, {[]byte(nil), nil}, {macaddrValuer{nil}, nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"v"}, []string{"MACADDR"}, [][]any{{frame.want}}, frame.arg)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { macaddrMetadata(t, r, b.PG, nil) }
				run(s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"MACADDR"}, [][]any{{frame.want}}, frame.arg))
		}
		for _, arg := range []any{"bad", []byte{}, "100:00:00:00:00:00"} {
			input := "bad"
			code := "22P02"
			switch v := arg.(type) {
			case string:
				input = v
				if input != "bad" {
					code = "22003"
				}
			case []byte:
				input = string(v)
			}
			d := macaddrDiagnostic(input, code)
			d.Context = "unnamed portal parameter $1 = '...'"
			if input == "" {
				d.Context = "unnamed portal parameter $1 = ''"
			}
			s := fixture.ErrorDiagnostic(q, d, arg)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"MACADDR"}, [][]any{{nil}}, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddrValue(q, macaddrSample, macaddrSample))
		}
		for _, kind := range []string{"string", "bytes", "nullable", "null"} {
			arg := any(macaddrSample)
			if kind == "null" {
				arg = nil
			}
			row := stmt.QueryRowContext(p.Ctx, arg)
			switch kind {
			case "string":
				var got string
				if err := row.Scan(&got); err != nil || got != macaddrSample {
					t.Fatal(got, err)
				}
			case "bytes":
				var got []byte
				if err := row.Scan(&got); err != nil || string(got) != macaddrSample {
					t.Fatal(got, err)
				}
			default:
				var got sql.NullString
				if err := row.Scan(&got); err != nil || got.Valid != (arg != nil) || (got.Valid && got.String != macaddrSample) {
					t.Fatal(got, err)
				}
			}
		}
		source := []byte("08002b010203")
		rows, err := stmt.QueryContext(p.Ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		source[0] = 'f'
		macaddrMetadata(t, rows, b.PG, nil)
		description, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing retained row", rows.Err())
		}
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, b.PG, "MACADDR", raw)
		if fixture.Normalize(raw) != macaddrSample {
			t.Fatal("input buffer changed result", raw)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		// ColumnTypes is not callable on closed database/sql Rows. The detached
		// description, unlike the cursor, remains inspectable after EOF/Close.
		if description[0].Name() != "v" || description[0].DatabaseTypeName() != "MACADDR" {
			t.Fatal("closed metadata changed", description[0])
		}
	})
}
