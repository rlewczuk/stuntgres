package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func macaddr8Metadata(t *testing.T, rows *sql.Rows, pg bool, nullable *bool) {
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
	if c.Name() != "v" || c.DatabaseTypeName() != macaddr8TypeName(pg) || c.ScanType() != scan {
		t.Fatalf("MACADDR8 metadata name=%s type=%s scan=%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if n, ok := c.Length(); n != 0 || ok {
		t.Fatal("unexpected length", n, ok)
	}
	if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
		t.Fatal("unexpected decimal size", p, s, ok)
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

func TestReferenceMACADDR8Transport(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac8_transport(id integer,v macaddr8,required macaddr8 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO mac8_transport VALUES(1,'08002b010203','08002b010203'),(2,NULL,'ffffffffffffffff')", 2))
		yes, no := true, false
		for _, tc := range []struct {
			q        string
			rows     [][]any
			nullable *bool
		}{
			{"SELECT v FROM mac8_transport ORDER BY id", [][]any{{macaddr8Six}, {nil}}, &yes},
			{"SELECT v FROM mac8_transport WHERE id=0", nil, &yes},
			{"SELECT v FROM mac8_transport WHERE id=2", [][]any{{nil}}, &yes},
			{"SELECT required AS v FROM mac8_transport WHERE id=1", [][]any{{macaddr8Six}}, &no},
			{"SELECT NULL::macaddr8 AS v", [][]any{{nil}}, nil},
			{"SELECT '08002b0102030405'::macaddr8 AS v", [][]any{{macaddr8Eight}}, nil},
		} {
			for _, prepared := range []bool{false, true} {
				s := macaddr8Select(b.PG, tc.q, []string{"v"}, []string{"MACADDR8"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { macaddr8Metadata(t, r, b.PG, tc.nullable) }
				run(s)
			}
		}
		q := "SELECT $1::macaddr8 AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ arg, want any }{{"08002b010203", macaddr8Six}, {[]byte("0800.2b01.0203.0405"), macaddr8Eight}, {macaddrValuer{"08002b010203"}, macaddr8Six}, {nil, nil}, {[]byte(nil), nil}, {macaddrValuer{nil}, nil}, {macaddr8Eight, macaddr8Eight}} {
			s := macaddr8Select(b.PG, q, []string{"v"}, []string{"MACADDR8"}, [][]any{{frame.want}}, frame.arg)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { macaddr8Metadata(t, r, b.PG, nil) }
			run(s)
			s.Prepared = true
			run(s)
			s.Prepared = false
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, arg := range []any{"bad", []byte{}, "100:00:2b:01:02:03"} {
			input := ""
			switch v := arg.(type) {
			case string:
				input = v
			case []byte:
				input = string(v)
			}
			s := fixture.ErrorDiagnostic(q, macaddr8ParameterDiagnostic(input, "1"), arg)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddr8Select(b.PG, q, []string{"v"}, []string{"MACADDR8"}, [][]any{{nil}}, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddr8Value(b.PG, q, macaddr8Six, "08002b010203"))
		}
		for _, kind := range []string{"string", "bytes", "nullable", "null"} {
			arg := any("08002b010203")
			if kind == "null" {
				arg = nil
			}
			row := stmt.QueryRowContext(p.Ctx, arg)
			switch kind {
			case "string":
				var got string
				if err := row.Scan(&got); err != nil || got != macaddr8Six {
					t.Fatal(got, err)
				}
			case "bytes":
				var got []byte
				if err := row.Scan(&got); err != nil || string(got) != macaddr8Six {
					t.Fatal(got, err)
				}
			default:
				var got sql.NullString
				if err := row.Scan(&got); err != nil || got.Valid != (arg != nil) || (got.Valid && got.String != macaddr8Six) {
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
		macaddr8Metadata(t, rows, b.PG, nil)
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
		fixture.AssertRawValue(t, b.PG, "MACADDR8", raw)
		if fixture.Normalize(raw) != macaddr8Six {
			t.Fatal("input buffer changed result", raw)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(macaddr8Value(b.PG, q, macaddr8Eight, "08002b0102030405"))
		if fixture.Normalize(raw) != macaddr8Six || description[0].Name() != "v" || description[0].DatabaseTypeName() != macaddr8TypeName(b.PG) {
			t.Fatal("detached result changed", raw, description)
		}
	})
}
