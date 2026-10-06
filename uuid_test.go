package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"sync"
	"testing"
)

const uuidA = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
const uuidZ = "00000000-0000-0000-0000-000000000000"
const uuidF = "ffffffff-ffff-ffff-ffff-ffffffffffff"

func TestUUIDCreateInputAndOrder(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-input")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	execSQL(t, db, "INSERT INTO u(v) VALUES(NULL),('ffffffff-ffff-ffff-ffff-ffffffffffff'),('{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}'),('00000000000000000000000000000000'),('a0eebc999c0b4ef8bb6d6bb9bd380a11')")
	wantTuples(t, db, "SELECT v FROM u ORDER BY v", [][]any{{uuidZ}, {uuidA}, {uuidA}, {uuidF}, {nil}})
	wantTuples(t, db, "SELECT v FROM u ORDER BY v DESC", [][]any{{nil}, {uuidF}, {uuidA}, {uuidA}, {uuidZ}})
	wantTuples(t, db, "SELECT v FROM u WHERE v=$1", [][]any{{uuidA}, {uuidA}}, "{A0EEBC999C0B4EF8BB6D6BB9BD380A11}")
}

func TestUUIDNoncanonicalGroupedForms(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-grouped-forms")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	for _, prepared := range []bool{false, true} {
		for _, form := range []string{
			"a0ee-bc99-9c0b-4ef8-bb6d-6bb9-bd38-0a11",
			"{a0eebc99-9c0b4ef8-bb6d6bb9-bd380a11}",
		} {
			for _, tc := range []struct {
				query string
				args  []any
			}{
				{"INSERT INTO u VALUES('" + form + "')", nil},
				{"INSERT INTO u VALUES($1)", []any{form}},
				{"INSERT INTO u VALUES($1)", []any{[]byte(form)}},
			} {
				var result sql.Result
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(tc.query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					result, err = stmt.Exec(tc.args...)
					stmt.Close()
				} else {
					result, err = db.Exec(tc.query, tc.args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
		}
	}
	want := make([][]any, 12)
	for i := range want {
		want[i] = []any{uuidA}
	}
	wantTuples(t, db, "SELECT v FROM u ORDER BY v", want)
	for _, prepared := range []bool{false, true} {
		for _, form := range []string{
			"a0ee-bc99-9c0b-4ef8-bb6d-6bb9-bd38-0a11",
			"{a0eebc99-9c0b4ef8-bb6d6bb9-bd380a11}",
		} {
			for _, tc := range []struct {
				query string
				args  []any
			}{
				{"SELECT v FROM u WHERE v='" + form + "'", nil},
				{"SELECT v FROM u WHERE v=$1", []any{form}},
				{"SELECT v FROM u WHERE v=$1", []any{[]byte(form)}},
			} {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(tc.query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					rows, err = stmt.Query(tc.args...)
					stmt.Close()
				} else {
					rows, err = db.Query(tc.query, tc.args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var got string
					if err := rows.Scan(&got); err != nil || got != uuidA {
						t.Fatal(got, err)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if count != 12 {
					t.Fatalf("matched %d canonical UUID rows, want 12", count)
				}
			}
		}
	}
}

type uuidValuer string

func (v uuidValuer) Value() (driver.Value, error) { return string(v), nil }

func TestUUIDBindingsMetadataAndScans(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-bind")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	stmt, err := db.Prepare("INSERT INTO u VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	source := []byte("{A0EEBC999C0B4EF8BB6D6BB9BD380A11}")
	for _, arg := range []any{source, uuidValuer(uuidF), nil} {
		r, err := stmt.Exec(arg)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	source[1] = '0'
	query, err := db.Prepare("SELECT v FROM u WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	for _, tc := range []struct {
		arg  any
		want []any
	}{{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", []any{uuidA}}, {uuidF, []any{uuidF}}, {nil, nil}} {
		rows, err := query.Query(tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		var got []any
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		nullable, known := types[0].Nullable()
		if types[0].Name() != "v" || types[0].DatabaseTypeName() != "UUID" || types[0].ScanType() != reflect.TypeFor[string]() || !known || !nullable {
			t.Fatal(types[0])
		}
		if _, ok := types[0].Length(); ok {
			t.Fatal("UUID length exposed")
		}
		if _, _, ok := types[0].DecimalSize(); ok {
			t.Fatal("UUID decimal size exposed")
		}
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			got = append(got, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc.want)
		}
	}
	var text string
	var buf []byte
	var nullable sql.NullString
	if err := db.QueryRow("SELECT v FROM u WHERE v=$1", uuidA).Scan(&text); err != nil || text != uuidA {
		t.Fatal(text, err)
	}
	if err := db.QueryRow("SELECT v FROM u WHERE v=$1", uuidA).Scan(&buf); err != nil || string(buf) != uuidA {
		t.Fatal(buf, err)
	}
	buf[0] = '0'
	if err := db.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&nullable); err != nil || nullable.Valid {
		t.Fatal(nullable, err)
	}
	if err := db.QueryRow("SELECT v FROM u ORDER BY v DESC").Scan(&text); err == nil {
		t.Fatal("NULL scanned into string")
	}
	wantTuples(t, db, "SELECT v FROM u WHERE v=$1", [][]any{{uuidA}}, uuidA)
	execSQL(t, db, "CREATE TABLE repeats(a UUID,b UUID)")
	repeat, err := db.Prepare("INSERT INTO repeats VALUES($1,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Close()
	for _, arg := range []any{uuidA, nil} {
		if _, err := repeat.Exec(arg); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := repeat.Exec(uuidF); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	wantTuples(t, db, "SELECT a,b FROM repeats WHERE a=$1", [][]any{{uuidA, uuidA}}, uuidA)
	execSQL(t, db, "CREATE TABLE required(v UUID NOT NULL)")
	rows, err := db.Query("SELECT v,v FROM required")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		n, k := typ.Nullable()
		if !k || n || typ.DatabaseTypeName() != "UUID" || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal(typ)
		}
	}
	if _, err := stmt.Exec([16]byte{}); err == nil {
		t.Fatal("binary array accepted")
	}
	_, err = db.Prepare("SELECT $1 FROM u WHERE v=$1")
	sqlState(t, err, "42P08")
}

func TestUUIDDiagnosticsAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-errors")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	for _, q := range []string{"INSERT INTO u(v) VALUES('bad')", "SELECT v FROM u WHERE v='bad'", "/*é*/ INSERT INTO u(v) VALUES('bad')", "/*é*/ SELECT v FROM u WHERE v='bad'"} {
		pos := "25"
		if strings.HasPrefix(q, "/*") {
			pos = "31"
		}
		want := stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: pos}
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("bad literal prepared")
		}
		typeCoreDiagnostic(t, err, want)
	}
	for _, q := range []string{"INSERT INTO u(v) VALUES($1)", "SELECT v FROM u WHERE v=$1"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{"bad", []byte("bad"), 1, true} {
			text := "bad"
			if arg == 1 {
				text = "1"
			}
			if arg == true {
				text = "true"
			}
			want := stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "` + text + `"`}
			_, err = db.Exec(q, arg)
			typeCoreDiagnostic(t, err, want)
			_, err = stmt.Exec(arg)
			typeCoreDiagnostic(t, err, want)
		}
		stmt.Close()
	}
	for _, tc := range []struct{ q, code, message, hint, pos string }{
		{"INSERT INTO u(v) VALUES(1)", "42804", `column "v" is of type uuid but expression is of type integer`, "You will need to rewrite or cast the expression.", "25"},
		{"INSERT INTO u(v) VALUES(TRUE)", "42804", `column "v" is of type uuid but expression is of type boolean`, "You will need to rewrite or cast the expression.", "25"},
		{"SELECT v FROM u WHERE v=1", "42883", "operator does not exist: uuid = integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "24"},
		{"SELECT v FROM u WHERE v=TRUE", "42883", "operator does not exist: uuid = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "24"},
		{"SELECT v FROM u WHERE v", "42804", "argument of WHERE must be type boolean, not type uuid", "", "23"},
		{`CREATE TABLE bad(v "UUID")`, "42704", `type "UUID" does not exist`, "", "20"},
	} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
		if !strings.HasPrefix(tc.q, "CREATE") {
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
		}
	}
	execSQL(t, db, "CREATE TABLE required(id INT NOT NULL,v UUID NOT NULL)")
	execSQL(t, db, "INSERT INTO required VALUES(99,$1)", uuidA)
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q    string
			args []any
			want stuntgres.Error
		}{
			{"INSERT INTO required VALUES(1,'00000000000000000000000000000000'),(2,'bad')", nil, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: "70"}},
			{"INSERT INTO required VALUES(1,$1),(2,$2)", []any{uuidZ, "bad"}, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`}},
			{"INSERT INTO required VALUES(1,'00000000000000000000000000000000'),(2,NULL)", nil, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "required" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "required", Column: "v"}},
			{"INSERT INTO required VALUES(NULL,$1)", []any{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}"}, stuntgres.Error{Code: "23502", Message: `null value in column "id" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null, " + uuidA + ").", Schema: "public", Table: "required", Column: "id"}},
		} {
			var err error
			if prepared {
				stmt, e := db.Prepare(tc.q)
				err = e
				if e == nil {
					_, err = stmt.Exec(tc.args...)
					stmt.Close()
				}
			} else {
				_, err = db.Exec(tc.q, tc.args...)
			}
			typeCoreDiagnostic(t, err, tc.want)
			wantTuples(t, db, "SELECT * FROM required", [][]any{{int64(99), uuidA}})
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := db.ExecContext(ctx, "INSERT INTO u VALUES($1)", uuidA)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

const uuidDump = "CREATE TABLE \"public\".\"empty\" (\"v\" UUID);\nCREATE TABLE \"public\".\"saved\" (\"v\" UUID NOT NULL, \"n\" UUID);\nINSERT INTO \"public\".\"saved\" VALUES ('00000000-0000-0000-0000-000000000000', NULL);\nINSERT INTO \"public\".\"saved\" VALUES ('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'ffffffff-ffff-ffff-ffff-ffffffffffff');\n"

func TestUUIDControlsAndRetainedSnapshot(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, "uuid-control")
	execSQL(t, db, "CREATE TABLE empty(v UUID)")
	execSQL(t, db, "CREATE TABLE saved(v UUID NOT NULL,n UUID)")
	execSQL(t, db, "INSERT INTO saved VALUES('00000000000000000000000000000000',NULL),('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}','ffffffffffffffffffffffffffffffff')")
	raw, err := d.Open("uuid-control")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT * FROM saved ORDER BY v", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil || dump.String() != uuidDump {
		t.Fatal(dump.String(), err)
	}
	execSQL(t, db, "INSERT INTO saved VALUES($1,NULL)", uuidF)
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(uuidDump)); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != uuidDump {
		t.Fatal(redump.String(), err)
	}
	wantTuples(t, db, "SELECT * FROM saved ORDER BY v", [][]any{{uuidZ, nil}, {uuidA, uuidF}})
	wantTuples(t, db, "SELECT * FROM empty", [][]any{})
	for _, script := range []string{"CREATE TABLE replacement(v UUID); INSERT INTO replacement VALUES('00000000000000000000000000000000'),('bad')", "/*é*/ CREATE TABLE replacement(v UUID); INSERT INTO replacement VALUES('00000000000000000000000000000000'),('bad')"} {
		err := stuntgres.Restore(ctx, db, strings.NewReader(script))
		pos := "103"
		if strings.HasPrefix(script, "/*") {
			pos = "109"
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: pos})
		redump.Reset()
		if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != uuidDump {
			t.Fatal(redump.String(), err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, strings.NewReader(uuidDump)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	failure := errors.New("uuid restore source failure")
	if err := stuntgres.Restore(ctx, db, uuidFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	redump.Reset()
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != uuidDump {
		t.Fatal(redump.String(), err)
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v UUID); INSERT INTO replacement VALUES($1)"))
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: "66"})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE saved(v TEXT); INSERT INTO saved VALUES('new')")); err != nil {
		t.Fatal(err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "UUID" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
		t.Fatal("retained metadata changed")
	}
	dst := make([]driver.Value, 2)
	for _, want := range [][]driver.Value{{uuidZ, nil}, {uuidA, uuidF}} {
		if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
			t.Fatal(dst, want, err)
		}
	}
	if err := held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT v FROM saved", [][]any{{"new"}})
}

func TestUUIDOrderingTrapsAndAllNull(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-order-traps")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	execSQL(t, db, "INSERT INTO u VALUES('80000000000000000000000000000000'),('00000100000000000000000000000000'),('00000001000000000000000000000000'),('7fffffffffffffffffffffffffffffff'),('00000000000000000000000000000001'),('00000000000000000000000000000000')")
	wantTuples(t, db, "SELECT v FROM u ORDER BY v", [][]any{{uuidZ}, {"00000000-0000-0000-0000-000000000001"}, {"00000001-0000-0000-0000-000000000000"}, {"00000100-0000-0000-0000-000000000000"}, {"7fffffff-ffff-ffff-ffff-ffffffffffff"}, {"80000000-0000-0000-0000-000000000000"}})
	execSQL(t, db, "CREATE TABLE nulls(v UUID)")
	execSQL(t, db, "INSERT INTO nulls VALUES(NULL)")
	rows, err := db.Query("SELECT * FROM nulls")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if types[0].DatabaseTypeName() != "UUID" || types[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal(types[0])
	}
	if !rows.Next() {
		t.Fatal("missing NULL")
	}
	var value any
	if err := rows.Scan(&value); err != nil || value != nil {
		t.Fatal(value, err)
	}
}

func TestUUIDAuthorization(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "uuid-auth")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE ROLE uuid_reader", "CREATE TABLE u(v UUID)", "INSERT INTO u VALUES('a0eebc999c0b4ef8bb6d6bb9bd380a11')"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	held, err := c.PrepareContext(ctx, "SELECT v FROM u")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := c.ExecContext(ctx, "SET SESSION AUTHORIZATION uuid_reader"); err != nil {
		t.Fatal(err)
	}
	var value string
	err = held.QueryRowContext(ctx).Scan(&value)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for table u"})
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q    string
			args []any
			want stuntgres.Error
		}{
			{"SELECT v FROM u", nil, stuntgres.Error{Code: "42501", Message: "permission denied for table u"}},
			{"INSERT INTO u VALUES($1)", []any{uuidA}, stuntgres.Error{Code: "42501", Message: "permission denied for table u"}},
			{"INSERT INTO u VALUES($1)", []any{"bad"}, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`}},
			{"INSERT INTO u VALUES('a0eebc999c0b4ef8bb6d6bb9bd380a11')", nil, stuntgres.Error{Code: "42501", Message: "permission denied for table u"}},
		} {
			var err error
			if prepared {
				s, e := c.PrepareContext(ctx, tc.q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = s.ExecContext(ctx, tc.args...)
				s.Close()
			} else {
				_, err = c.ExecContext(ctx, tc.q, tc.args...)
			}
			typeCoreDiagnostic(t, err, tc.want)
		}
	}
	if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	if err := held.QueryRowContext(ctx).Scan(&value); err != nil || value != uuidA {
		t.Fatal(value, err)
	}
	wantTuples(t, db, "SELECT v FROM u", [][]any{{uuidA}})
}

type uuidFailingReader struct{ err error }

func (r uuidFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestUUIDQueryDiagnosticHasNoCursor(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "uuid-query-errors")
	execSQL(t, db, "CREATE TABLE u(v UUID)")
	for _, q := range []string{"SELECT v FROM u WHERE v='bad'", "/*é*/ SELECT v FROM u WHERE v='bad'"} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		pos := "25"
		if strings.HasPrefix(q, "/*") {
			pos = "31"
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: pos})
	}
	q := "SELECT v FROM u WHERE v=$1"
	s, err := db.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			rows, err = s.Query("bad")
		} else {
			rows, err = db.Query(q, "bad")
		}
		if rows != nil {
			rows.Close()
			t.Fatal("failed bound query exposed cursor")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`})
	}
}
