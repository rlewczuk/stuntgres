package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitstringBinds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			table, decl string
			good, empty any
			want        string
			width       int64
		}{
			{"bs_bind_fixed", "BIT(3)", "001", nil, "001", 3},
			{"bs_bind_varying", "VARBIT(5)", "B10", []byte{}, "10", 5},
		} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE "+tc.table+"(id INTEGER,v "+tc.decl+")", 0))
			q := "INSERT INTO " + tc.table + " VALUES($1,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
				for i, arg := range []any{tc.good, nil, tc.empty, []byte(tc.good.(string))} {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, i+1, arg))
				}
				for _, value := range []string{"2", "xG", " 01", "01 ", "é", "界", "😀"} {
					family, digit := "binary", value
					if value == "xG" {
						family, digit = "hexadecimal", "G"
					}
					if value == " 01" || value == "01 " {
						digit = " "
					}
					for _, arg := range []any{value, []byte(value)} {
						for _, exec := range []fixture.Executor{backend.DB, held} {
							bitstringReferenceError(t, p, backend, exec, q, []any{9, arg}, false, false, "22P02", fmt.Sprintf("%q is not a valid %s digit", digit, family), "")
						}
					}
				}
				bad, state, message := "10", "22026", "bit string length 2 does not match type bit(3)"
				if tc.width == 5 {
					bad, state, message = "101010", "22001", "bit string too long for type bit varying(5)"
				}
				for _, exec := range []fixture.Executor{backend.DB, held} {
					bitstringReferenceError(t, p, backend, exec, q, []any{9, bad}, false, false, state, message, "")
				}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 5, tc.good))
			}()
			empty := any(nil)
			if tc.width == 5 {
				empty = ""
			}
			s := fixture.Select("SELECT v FROM "+tc.table+" ORDER BY id", []string{"v"}, []string{map[bool]string{true: "BIT", false: "VARBIT"}[tc.width == 3]}, [][]any{{tc.want}, {nil}, {empty}, {tc.want}, {tc.want}})
			s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{tc.width}, []bool{true})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		// Hex parameters and empty binary/hex markers are input syntax, not casts.
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_bind_input(v VARBIT)", 0))
		for _, prepared := range []bool{false, true} {
			for _, arg := range []any{"xA", []byte("X0f"), "B", "X", "", []byte{}} {
				s := fixture.Exec("INSERT INTO bs_bind_input VALUES($1)", 1, arg)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
		inputRows := fixture.Select("SELECT v FROM bs_bind_input", []string{"v"}, []string{"VARBIT"}, [][]any{{"1010"}, {"00001111"}, {""}, {""}, {""}, {""}, {"1010"}, {"00001111"}, {""}, {""}, {""}, {""}})
		inputRows.Bag = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, inputRows)
		// Typed nil bytes are SQL NULL; nonnil empty bytes above are empty bits.
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_typed_nil(v VARBIT)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_typed_nil VALUES($1)", 1, []byte(nil)))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM bs_typed_nil", []string{"v"}, []string{"VARBIT"}, [][]any{{nil}}))
		// Mixed-family inference retains the prerequisite's bounded native
		// capability boundary, independently of PostgreSQL's exact diagnostic.
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_repeated(a BIT(3),b BIT(3),c VARBIT(5))", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO bs_repeated(a,b) VALUES($1,$1)", 1, "001")
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			want := fixture.ControlsReferenceDiagnostic("0A000", "parameter reused with different destination types")
			if backend.PG {
				want = fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
				want.Detail, want.Position = "bit versus bit varying", "40"
			}
			failure := fixture.ErrorDiagnostic("INSERT INTO bs_repeated(a,c) VALUES($1,$1)", want, "001")
			failure.Prepared, failure.ErrorStage = prepared, "execute"
			if prepared {
				failure.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
		}
		for _, table := range []string{"bs_bind_fixed", "bs_bind_varying"} {
			q := "INSERT INTO " + table + "(v) VALUES($1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
				for _, exec := range []fixture.Executor{backend.DB, held} {
					bitstringReferenceError(t, p, backend, exec, q, []any{"2"}, false, false, "22P02", `"2" is not a valid binary digit`, "")
					bitstringReferenceError(t, p, backend, exec, q, []any{"xG"}, false, false, "22P02", `"G" is not a valid hexadecimal digit`, "")
					bad, state, message := "10", "22026", "bit string length 2 does not match type bit(3)"
					if table == "bs_bind_varying" {
						bad, state, message = "101010", "22001", "bit string too long for type bit varying(5)"
					}
					bitstringReferenceError(t, p, backend, exec, q, []any{bad}, false, false, state, message, "")
				}
			}()
		}
	}
}

func TestReferenceBitstringAssignmentAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE bs_src(id INTEGER,f BIT(3),v VARBIT(5))", 0))
		run(fixture.Exec("CREATE TABLE bs_dst(id INTEGER,f BIT(3),v VARBIT(5))", 0))
		run(fixture.Exec("INSERT INTO bs_src VALUES(1,B'001',B'001'),(2,B'100',B'10'),(3,B'101',B'1010')", 3))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"INSERT INTO bs_dst(id,v) SELECT id,f FROM bs_src WHERE id=1", "INSERT INTO bs_dst(id,f) SELECT id,v FROM bs_src WHERE id=1"} {
				s := fixture.Exec(q, 1)
				s.Prepared = prepared
				run(s)
			}
			for _, tc := range []struct {
				q              string
				args           []any
				state, message string
			}{
				{"INSERT INTO bs_dst(id,f) SELECT id,v FROM bs_src WHERE id=2", nil, "22026", "bit string length 2 does not match type bit(3)"},
				{"INSERT INTO bs_dst(id,f) SELECT id,v FROM bs_src WHERE id=3", nil, "22026", "bit string length 4 does not match type bit(3)"},
				{"INSERT INTO bs_dst(id,f) VALUES(9,B'001'),(10,B'10')", nil, "22026", "bit string length 2 does not match type bit(3)"},
				{"INSERT INTO bs_dst(id,f) VALUES(9,B'001'),(10,B'1010')", nil, "22026", "bit string length 4 does not match type bit(3)"},
				{"INSERT INTO bs_dst(id,f) VALUES(9,B'001'),(10,B'2')", nil, "22P02", `"2" is not a valid binary digit`},
				{"INSERT INTO bs_dst(id,f) VALUES(9,$1),(10,$2)", []any{"001", "10"}, "22026", "bit string length 2 does not match type bit(3)"},
				{"INSERT INTO bs_dst(id,f) VALUES(9,$1),(10,$2)", []any{"001", "2"}, "22P02", `"2" is not a valid binary digit`},
			} {
				position := ""
				if tc.state == "22P02" && len(tc.args) == 0 {
					position = "47"
				}
				bitstringReferenceError(t, p, backend, backend.DB, tc.q, tc.args, prepared, false, tc.state, tc.message, position)
				s := fixture.Select("SELECT id,f,v FROM bs_dst", []string{"id", "f", "v"}, []string{"INT4", "BIT", "VARBIT"}, [][]any{{int64(1), nil, "001"}, {int64(1), "001", nil}})
				if prepared {
					s.Rows = append(s.Rows, []any{int64(1), nil, "001"}, []any{int64(1), "001", nil})
				}
				s.Bag = true
				run(s)
			}
		}
		// Varying bounds still apply after a cross-family column conversion.
		run(fixture.Exec("CREATE TABLE bs_narrow(v VARBIT(2))", 0))
		for _, prepared := range []bool{false, true} {
			bitstringReferenceError(t, p, backend, backend.DB, "INSERT INTO bs_narrow SELECT f FROM bs_src WHERE id=1", nil, prepared, false, "22001", "bit string too long for type bit varying(2)", "")
		}
		tx, err := backend.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO bs_dst VALUES(20,B'001',B'1')", 1))
			bitstringReferenceError(t, p, backend, tx, "INSERT INTO bs_dst VALUES(21,B'10',B'1')", nil, false, false, "22026", "bit string length 2 does not match type bit(3)", "")
			want := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ErrorDiagnostic("INSERT INTO bs_dst VALUES(22,B'001',B'1')", want))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}()
		run(fixture.Select("SELECT id FROM bs_dst WHERE id=20", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO bs_dst VALUES(30,B'001',B'1')", 1))
		run(fixture.Select("SELECT f,v FROM bs_dst WHERE id=30", []string{"f", "v"}, []string{"BIT", "VARBIT"}, [][]any{{"001", "1"}}))
	}
}

func TestReferenceBitstringMetadataOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_metadata(id INTEGER,f BIT(4) NOT NULL,v VARBIT(5),u VARBIT)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT f,v,u FROM bs_metadata", []string{"f", "v", "u"}, []string{"BIT", "VARBIT", "VARBIT"}, nil)
			s.Prepared = prepared
			s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{4, 5, -1}, []bool{false, true, true})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		buf := []byte("X0f")
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_metadata VALUES(1,$1,$2,$3),(2,B'0010',NULL,NULL)", 2, "xA", []byte{}, buf))
		copy(buf, []byte("B11"))
		q := "SELECT f,v,u FROM bs_metadata ORDER BY id"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			check := func(want [][]any) {
				s := fixture.Select(q, []string{"f", "v", "u"}, []string{"BIT", "VARBIT", "VARBIT"}, want)
				s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{4, 5, -1}, []bool{false, true, true})
				s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
					var f string
					var v []byte
					var u sql.NullString
					if err := rows.Scan(&f, &v, &u); err != nil {
						t.Fatal(err)
					}
					if f != want[index][0] {
						t.Fatal(f, want[index])
					}
					if want[index][1] == nil {
						if v != nil {
							t.Fatal("NULL bytes", v)
						}
					} else if v == nil || string(v) != want[index][1] {
						t.Fatal("empty/bit bytes", v, want[index][1])
					}
					if u.Valid != (want[index][2] != nil) || u.Valid && u.String != want[index][2] {
						t.Fatal(u, want[index][2])
					}
				}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
			check([][]any{{"1010", "", "00001111"}, {"0010", nil, nil}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_metadata VALUES(3,B'1000',B'01',B'000')", 1))
			check([][]any{{"1010", "", "00001111"}, {"0010", nil, nil}, {"1000", "01", "000"}})
		}()
		rows, err := backend.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		before := make([]int64, len(types))
		for i, typ := range types {
			before[i], _ = typ.Length()
		}
		if !backend.PG {
			names, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			names[0] = "mutated"
			again, err := rows.Columns()
			if err != nil || again[0] != "f" {
				t.Fatal("Columns aliases caller buffer", again, err)
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_metadata VALUES(4,B'0001',B'',NULL)", 1))
		want := [][]any{{"1010", "", "00001111"}, {"0010", nil, nil}, {"1000", "01", "000"}}
		index := 0
		for rows.Next() {
			var f, v, u any
			if err := rows.Scan(&f, &v, &u); err != nil {
				t.Fatal(err)
			}
			values := []any{f, v, u}
			for i, value := range values {
				fixture.AssertRawValue(t, backend.PG, []string{"BIT", "VARBIT", "VARBIT"}[i], value)
				values[i] = fixture.Normalize(value)
			}
			if index >= len(want) || !reflect.DeepEqual(values, want[index]) {
				t.Fatal("retained cursor rows", index, values, want)
			}
			index++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if index != len(want) {
			t.Fatal("retained cursor count", index, len(want))
		}
		for i, typ := range types {
			n, known := typ.Length()
			if !known || n != before[i] || typ.ScanType() != reflect.TypeFor[string]() {
				t.Fatal("metadata changed after EOF", typ)
			}
		}
		fresh := fixture.Select("SELECT f,v,u FROM bs_metadata WHERE id=4", []string{"f", "v", "u"}, []string{"BIT", "VARBIT", "VARBIT"}, [][]any{{"0001", "", nil}})
		fresh.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{4, 5, -1}, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fresh)
	}
}
