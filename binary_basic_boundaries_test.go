package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestBinaryBasicEmptyNullAndByteSets(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'\x010201030201','\x0201'),(2,'\x010201030201','\x01010102'),(3,'\x010201','\x0201'),(4,'\xc3a9f09f9880',''),(5,'','\xff'),(6,'',''),(7,NULL,'\xff'),(8,'\xff',NULL),(9,NULL,NULL),(10,' A ','\x00')`)
	want := [][]any{
		{[]byte{3}, []byte{3, 2, 1}, []byte{1, 2, 1, 3}, int64(6), int64(6), int64(48)},
		{[]byte{3}, []byte{3, 2, 1}, []byte{1, 2, 1, 3}, int64(6), int64(6), int64(48)},
		{[]byte{}, []byte{}, []byte{}, int64(3), int64(3), int64(24)},
		{[]byte{195, 169, 240, 159, 152, 128}, []byte{195, 169, 240, 159, 152, 128}, []byte{195, 169, 240, 159, 152, 128}, int64(6), int64(6), int64(48)},
		{[]byte{}, []byte{}, []byte{}, int64(0), int64(0), int64(0)},
		{[]byte{}, []byte{}, []byte{}, int64(0), int64(0), int64(0)},
		{nil, nil, nil, nil, nil, nil},
		{nil, nil, nil, int64(1), int64(1), int64(8)},
		{nil, nil, nil, nil, nil, nil},
		{[]byte(" A "), []byte(" A "), []byte(" A "), int64(3), int64(3), int64(24)},
	}
	wantTuples(t, db, "SELECT btrim(v,s),ltrim(v,s),rtrim(v,s),length(v),octet_length(v),bit_length(v) FROM t ORDER BY id", want)
	wantTuples(t, db, "SELECT v||'', ''||v,btrim(v,NULL),btrim(NULL,v) FROM t WHERE id=6", [][]any{{[]byte{}, []byte{}, nil, nil}})
	for _, q := range []string{"SELECT btrim(v,s) AS b,length(v) AS n FROM t WHERE id=9", "SELECT btrim(v,s) AS b,length(v) AS n FROM t WHERE id=99"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "BYTEA" || types[1].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		if types[0].ScanType() != reflect.TypeFor[[]byte]() || types[1].ScanType() != reflect.TypeFor[int64]() {
			t.Fatal(types)
		}
		if _, ok := types[0].Length(); ok {
			t.Fatal("computed bytea length metadata")
		}
		if _, _, ok := types[0].DecimalSize(); ok {
			t.Fatal("computed bytea precision metadata")
		}
		for rows.Next() {
			var a, b any
			if err := rows.Scan(&a, &b); err != nil || a != nil || b != nil {
				t.Fatal(a, b, err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if types[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal("metadata lost after close")
		}
	}
}

func TestBinaryBasicContextualPreparedFramesAndOwnership(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{"CREATE TABLE t(v BYTEA,s BYTEA)", `INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	stmt, err := conn.PrepareContext(ctx, "SELECT v||$1 AS cat,btrim(v,$1) AS bt,ltrim(v,$1) AS lt,rtrim(v,$1) AS rt FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	v := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0}
	for _, mode := range []string{"hex", "escape"} {
		if _, err := conn.ExecContext(ctx, "SET bytea_output="+mode); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			arg  any
			want []any
		}{
			{[]byte{255, 128, 0}, []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}, []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}},
			{[]byte{}, []any{v, v, v, v}},
			{nil, []any{nil, nil, nil, nil}},
			{`\x41`, []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 92, 120, 52, 49}, v, v, v}},
			{[]byte{255, 128, 0}, []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}, []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}},
		} {
			rows, err := stmt.QueryContext(ctx, tc.arg)
			if err != nil {
				t.Fatal(err)
			}
			if input, ok := tc.arg.([]byte); ok && len(input) > 0 {
				input[0] = 42
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var cat, bt, lt, rt any
			if err := rows.Scan(&cat, &bt, &lt, &rt); err != nil || !reflect.DeepEqual([]any{cat, bt, lt, rt}, tc.want) {
				t.Fatal(cat, bt, lt, rt, tc.want, err)
			}
			if output, ok := cat.([]byte); ok && len(output) > 0 {
				output[0] = 99
			}
			if b, ok := bt.([]byte); ok && len(b) > 0 && !reflect.DeepEqual(b, tc.want[1]) {
				t.Fatal("sibling buffer alias")
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		}
	}
	for _, tc := range []struct {
		q    string
		arg  any
		want any
	}{
		{"SELECT $1||v FROM t", []byte{1}, []byte{1, 0, 255, 128, 65, 0, 128, 66, 255, 0}},
		{"SELECT btrim($1,s) FROM t", []byte{0, 65, 255}, []byte{65}},
		{`SELECT btrim(v,'\x0080ff') FROM t`, nil, []byte{65, 0, 128, 66}},
	} {
		s, err := conn.PrepareContext(ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if tc.arg == nil {
			err = s.QueryRowContext(ctx).Scan(&got)
		} else {
			err = s.QueryRowContext(ctx, tc.arg).Scan(&got)
		}
		s.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc.want, err)
		}
	}
	var stored []byte
	if err := conn.QueryRowContext(ctx, "SELECT v FROM t").Scan(&stored); err != nil || !reflect.DeepEqual(stored, v) {
		t.Fatal(stored, err)
	}
}

func TestBinaryBasicTruthfulCapabilityBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA,s TEXT)")
	for _, q := range []string{"SELECT length(NULL)", "SELECT octet_length('é')", "SELECT bit_length($1)", "SELECT btrim(NULL,NULL)", "SELECT ltrim(' x ')", "SELECT rtrim(s) FROM t", "SELECT length(v,'UTF8') FROM t", "SELECT v||s FROM t", "SELECT s||v FROM t", "SELECT 'a'||'b' FROM t"} {
		s, err := db.Prepare(q)
		if s != nil {
			s.Close()
			t.Fatal("published unsupported statement", q)
		}
		var se *stuntgres.Error
		if !errors.As(err, &se) || se.Code != "0A000" {
			t.Fatal(q, err)
		}
	}
}

func TestBinaryBasicExcludedSyntaxAndOccurrenceTyping(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v BYTEA)")
	execSQL(t, db, "CREATE TABLE dst(v BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES('\x00ff80')`)
	// Preserve the rebased master's existing same-type cast admission.
	wantTuples(t, db, "SELECT v::bytea,length(v::bytea) AS n FROM t", [][]any{{[]byte{0, 255, 128}, int64(3)}})
	_, trimErr := db.Exec("SELECT TRIM(v) FROM t")
	typeCoreDiagnostic(t, trimErr, stuntgres.Error{Code: "42883", Message: "function pg_catalog.btrim(bytea) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	for _, q := range []string{
		"SELECT byteacat(v,v) FROM t",
		"INSERT INTO dst SELECT 'a'||'b' FROM t",
	} {
		_, err := db.Exec(q)
		var se *stuntgres.Error
		if !errors.As(err, &se) || se.Code != "0A000" {
			t.Fatal(q, err)
		}
	}
	_, err := db.Exec("CREATE TABLE bad(v BYTEA DEFAULT length(NULL))")
	var se *stuntgres.Error
	if !errors.As(err, &se) || se.Code != "0A000" {
		t.Fatal(err)
	}
	stmt, err := db.Prepare("SELECT $1,v||$1 FROM t")
	if stmt != nil {
		stmt.Close()
	}
	if !errors.As(err, &se) || se.Code != "42P08" {
		t.Fatal("incompatible earlier occurrence", err)
	}
	stmt, err = db.Prepare("SELECT v FROM t LIMIT length(v)")
	if stmt != nil {
		stmt.Close()
		t.Fatal("variable LIMIT published plan")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P10", Message: "argument of LIMIT must not contain variables", Position: "30"})
}

func TestBinaryBasicConcatStrictNullAndEmptyIdentity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'','\xff'),(2,'\xff',''),(3,'',''),(4,NULL,'\xff'),(5,'\xff',NULL),(6,NULL,NULL)`)
	wantTuples(t, db, "SELECT v||s FROM t ORDER BY id", [][]any{{[]byte{255}}, {[]byte{255}}, {[]byte{}}, {nil}, {nil}, {nil}})
}

func TestBinaryBasicEncodingLengthCapability(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE enc(v BYTEA,t TEXT,vc VARCHAR(4),bp CHAR(4),n NAME)")
	execSQL(t, db, `INSERT INTO enc VALUES('\xc3a9','UTF8','UTF8','UTF8','UTF8')`)
	for _, q := range []string{
		"SELECT length(NULL,'UTF8') AS n",
		"SELECT length(v,t) AS n FROM enc",
		"SELECT length(v,vc) AS n FROM enc",
		"SELECT length(v,bp) AS n FROM enc",
		"SELECT length(NULL,t) AS n FROM enc",
		"SELECT length(NULL,vc) AS n FROM enc",
		"SELECT length(NULL,bp) AS n FROM enc",
		"SELECT length(v,n) AS n FROM enc",
		"SELECT length(v,'UTF8') AS n FROM enc",
		`SELECT length('\xc3a9','UTF8') AS n`,
	} {
		t.Run(q, func(t *testing.T) {
			want := stuntgres.Error{Code: "0A000", Message: "encoding-aware length is unsupported", Position: "8"}
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
				t.Fatal("excluded overload exposed cursor")
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("excluded overload exposed statement")
			}
			typeCoreDiagnostic(t, err, want)
		})
	}
	_, err := db.Exec("SELECT length(v,1) FROM enc")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "function length(bytea, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
}
