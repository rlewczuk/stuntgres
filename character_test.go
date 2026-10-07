package stuntgres_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
	"unicode/utf8"
)

// Expectations here are independent character-type contract witnesses, not
// values learned from the native implementation. Oracle-dependent pq contracts
// are captured separately by TestReferenceCharacterOracle.
func characterMetadata(t *testing.T, db *sql.DB, q string, names []string, lengths []int64) {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != len(names) {
		t.Fatalf("metadata count %d want %d", len(types), len(names))
	}
	check := func() {
		for i, typ := range types {
			n, ok := typ.Length()
			_, _, decimal := typ.DecimalSize()
			wantLength := lengths[i]
			if wantLength < 0 {
				wantLength = math.MaxInt64
			}
			if typ.DatabaseTypeName() != names[i] || typ.ScanType() != reflect.TypeFor[string]() || decimal || !ok || n != wantLength {
				t.Fatalf("column %d type=%s scan=%v length=(%d,%t) decimal=%t want %s/%d", i, typ.DatabaseTypeName(), typ.ScanType(), n, ok, decimal, names[i], wantLength)
			}
		}
	}
	check()
	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	check()
}

func TestCharacterAliasesAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		decl, name, value string
		length            int64
	}{
		{"char", "BPCHAR", "é", 1}, {"character", "BPCHAR", "é", 1}, {"char(3)", "BPCHAR", "é  ", 3}, {"character(3)", "BPCHAR", "é  ", 3}, {"bpchar(3)", "BPCHAR", "é  ", 3}, {"bpchar", "BPCHAR", "é", -1}, {"varchar", "VARCHAR", "é", -1}, {"character varying", "VARCHAR", "é", -1}, {"varchar(3)", "VARCHAR", "é", 3}, {"character varying(3)", "VARCHAR", "é", 3}, {"text", "TEXT", "é", -1}, {`"bpchar"`, "BPCHAR", "é", -1}, {`"varchar"(3)`, "VARCHAR", "é", 3},
	} {
		t.Run(tc.decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE c(v "+tc.decl+")")
			characterMetadata(t, db, "SELECT v FROM c", []string{tc.name}, []int64{tc.length})
			execSQL(t, db, "INSERT INTO c VALUES('é'),(NULL)")
			bagRows, bagErr := db.Query("SELECT v FROM c")
			queryCoreRows(t, bagRows, bagErr, []string{"v"}, []string{tc.name}, [][]any{{tc.value}, {nil}}, true)
			rows, err := db.Query("SELECT v FROM c")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			n, ok := types[0].Nullable()
			if !ok || !n {
				t.Fatal("declaration nullable metadata", n, ok)
			}
		})
	}
	for _, decl := range []string{"char(1)", "varchar(1)", "char(10485760)", "varchar(10485760)"} {
		t.Run("maximum_"+decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE c(v "+decl+")")
		})
	}
	for _, decl := range []string{`"char"`} {
		t.Run("unsupported_"+decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			_, err := db.Exec("CREATE TABLE c(v " + decl + ")")
			sqlState(t, err, "0A000")
		})
	}
	for _, decl := range []string{`"character"`, `"VARCHAR"`} {
		t.Run("unknown_"+decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			_, err := db.Exec("CREATE TABLE c(v " + decl + ")")
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42704", Message: "type " + decl + " does not exist", Position: "18"})
		})
	}
}

func TestCharacterValuesAndAssignment(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE c(id INT,c CHAR(3),v VARCHAR(2),t TEXT,u BPCHAR,w VARCHAR)")
	execSQL(t, db, "INSERT INTO c VALUES(1,'é🙂','é🙂','a  ','a  ','a  '),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)")
	wantTuples(t, db, "SELECT * FROM c ORDER BY id", [][]any{{int64(1), "é🙂 ", "é🙂", "a  ", "a  ", "a  "}, {int64(2), "   ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}})
	stmt, err := db.Prepare("INSERT INTO c VALUES($1,$2,$3,$4,$5,$6)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	owned := []byte("e\u0301")
	if _, err = stmt.Exec(4, owned, owned, owned, owned, owned); err != nil {
		t.Fatal(err)
	}
	owned[0] = 'X'
	wantTuples(t, db, "SELECT c,v,t,u,w FROM c WHERE id=4", [][]any{{"e\u0301 ", "e\u0301", "e\u0301", "e\u0301", "e\u0301"}})
	for _, decl := range []string{"char(5)", "varchar(5)"} {
		t.Run(decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE c(v "+decl+")")
			for _, value := range []string{"good      ", "good\t", "abcde   "} {
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						stmt, e := db.Prepare("INSERT INTO c VALUES($1)")
						if e != nil {
							t.Fatal(e)
						}
						_, err = stmt.Exec([]byte(value))
						stmt.Close()
					} else {
						_, err = db.Exec("INSERT INTO c VALUES($1)", value)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			dbType := "VARCHAR"
			if strings.HasPrefix(decl, "char(") {
				dbType = "BPCHAR"
			}
			bagRows, bagErr := db.Query("SELECT v FROM c")
			queryCoreRows(t, bagRows, bagErr, []string{"v"}, []string{dbType}, [][]any{{"good "}, {"good "}, {"good\t"}, {"good\t"}, {"abcde"}, {"abcde"}}, true)
			name := "character varying(5)"
			if strings.HasPrefix(decl, "char(") {
				name = "character(5)"
			}
			for _, value := range []string{"good \t", "abcde\n", "abcde\r", "abcde\u00a0", "abcde\u3000"} {
				stmt, e := db.Prepare("INSERT INTO c VALUES($1)")
				if e != nil {
					t.Fatal(e)
				}
				_, err := stmt.Exec(value)
				stmt.Close()
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type " + name})
			}
			for _, q := range []string{"INSERT INTO c VALUES('ok'),('overlong')", "INSERT INTO c VALUES(CAST('abcdef' AS text))"} {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal("assignment must prepare", e)
				}
				_, err := stmt.Exec()
				stmt.Close()
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type " + name})
			}
			bagRows, bagErr = db.Query("SELECT v FROM c")
			queryCoreRows(t, bagRows, bagErr, []string{"v"}, []string{dbType}, [][]any{{"good "}, {"good "}, {"good\t"}, {"good\t"}, {"abcde"}, {"abcde"}}, true)
		})
	}
	execSQL(t, db, "CREATE TABLE repeat(a CHAR(2),b CHAR(4))")
	repeat, err := db.Prepare("INSERT INTO repeat VALUES($1,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.Close()
	for _, v := range []any{"a", nil} {
		r, e := repeat.Exec(v)
		if e != nil {
			t.Fatal(e)
		}
		n, e := r.RowsAffected()
		if e != nil || n != 1 {
			t.Fatal(n, e)
		}
	}
	_, err = repeat.Exec("abc")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type character(2)"})
	bagRows, bagErr := db.Query("SELECT * FROM repeat")
	queryCoreRows(t, bagRows, bagErr, []string{"a", "b"}, []string{"BPCHAR", "BPCHAR"}, [][]any{{"a ", "a   "}, {nil, nil}}, true)
	execSQL(t, db, "CREATE TABLE io(c CHAR(5),v VARCHAR(5))")
	execSQL(t, db, "INSERT INTO io VALUES(TRUE,123),(FALSE,1.25)")
	bagRows, bagErr = db.Query("SELECT * FROM io")
	queryCoreRows(t, bagRows, bagErr, []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"true ", "123"}, {"false", "1.25"}}, true)
}

func TestCharacterCastsAndConversions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE seed(i INT)")
	execSQL(t, db, "INSERT INTO seed VALUES(1)")
	wantTuples(t, db, "SELECT CAST(CAST(12 AS text) AS varchar(1)),CAST(TRUE AS text)::char(6) FROM seed", [][]any{{"1", "true  "}})
	wantTuples(t, db, "SELECT CAST(CAST('12 ' AS bpchar) AS text)::int4 FROM seed", [][]any{{int64(12)}})
	wantTuples(t, db, "SELECT CAST(NULL AS char(3)) IS NULL,CAST('a' AS char(3)) IS NOT DISTINCT FROM CAST('a ' AS text),CAST('a' AS char(3)) IS DISTINCT FROM CAST('a' AS text) FROM seed", [][]any{{true, false, false}})
	wantTuples(t, db, "SELECT num_nulls(CAST(NULL AS char(3)),CAST('a' AS varchar(2)))", [][]any{{int64(1)}})
	for _, tc := range []struct{ expr, want string }{
		{"CAST('too long' AS varchar(5))", "too l"}, {"'too long'::varchar(5)", "too l"}, {"CAST('too long' AS char(5))", "too l"}, {"'ok'::char(5)", "ok   "}, {"CAST('ab' AS char)", "a"}, {"'ab  '::bpchar", "ab  "}, {"'é🙂x'::varchar(2)", "é🙂"}, {"CAST(CAST('a ' AS char(2)) AS text)", "a"}, {"CAST(CAST('a   ' AS bpchar) AS varchar(1))", "a"}, {"CAST('a '::varchar(2) AS text)", "a "}, {"CAST('a '::char(2) AS char(4))", "a   "},
	} {
		t.Run(tc.expr, func(t *testing.T) { wantTuples(t, db, "SELECT "+tc.expr+" FROM seed", [][]any{{tc.want}}) })
	}
	for _, tc := range []struct {
		decl, name string
		n          int64
	}{{"char(3)", "BPCHAR", 3}, {"varchar(2)", "VARCHAR", 2}, {"text", "TEXT", -1}} {
		q := "SELECT CAST(NULL AS " + tc.decl + ") FROM seed"
		wantTuples(t, db, q, [][]any{{nil}})
		characterMetadata(t, db, q, []string{tc.name}, []int64{tc.n})
	}
	stmt, err := db.Prepare("SELECT CAST($1 AS varchar(2)) FROM seed")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct{ in, want any }{{"é🙂x", "é🙂"}, {"x", "x"}, {"", ""}, {nil, nil}} {
		rows, e := stmt.Query(tc.in)
		if e != nil {
			t.Fatal(e)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if e = rows.Scan(&got); e != nil {
			t.Fatal(e)
		}
		if got != tc.want {
			t.Fatal(got, tc.want)
		}
		if rows.Next() {
			t.Fatal("extra row")
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
	}
	execSQL(t, db, "CREATE TABLE c(v CHAR(2))")
	stmt, err = db.Prepare("INSERT INTO c VALUES(CAST('abcd' AS varchar(3)))")
	if err != nil {
		t.Fatal(err)
	}
	_, err = stmt.Exec()
	stmt.Close()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type character(2)"})
	execSQL(t, db, "INSERT INTO c VALUES('a')")
	wantTuples(t, db, "SELECT CAST(v AS char(4)),CAST(v AS text),CAST(v AS varchar(3)) FROM c", [][]any{{"a   ", "a", "a"}})
	wantTuples(t, db, "SELECT v::char(4)::text FROM c", [][]any{{"a"}})
	wantTuples(t, db, "SELECT CAST('1' AS integer) FROM seed", [][]any{{int64(1)}})
	for _, tc := range []struct {
		q, name, typ string
		want         any
	}{
		{"SELECT CAST(TIMESTAMP 'epoch' AS text) FROM seed", "text", "TEXT", "1970-01-01 00:00:00"},
		{"SELECT TIMESTAMP 'epoch'::timestamp FROM seed", "timestamp", "TIMESTAMP", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		want := [][]any{{tc.want}}
		timestampQuery(t, db, tc.q, []string{tc.name}, []string{tc.typ}, want)
		stmt, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		timestampCheckRows(t, rows, []string{tc.name}, []string{tc.typ}, want)
		stmt.Close()
	}
	for _, q := range []string{"SELECT CAST(1 AS varchar(2)) FROM seed"} {
		_, err := db.Prepare(q)
		sqlState(t, err, "0A000")
	}
	for _, q := range []string{`SELECT CAST('a' AS "VARCHAR") FROM seed`, `SELECT 'a'::"character" FROM seed`} {
		_, err := db.Prepare(q)
		sqlState(t, err, "42704")
		_, err = db.Query(q)
		sqlState(t, err, "42704")
	}
}

func TestCharacterEqualityAndOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE c(c CHAR(3),d CHAR(5),v VARCHAR(5),t TEXT,u BPCHAR)")
	execSQL(t, db, "INSERT INTO c VALUES('a','a','a ','a ','a  ')")
	for _, tc := range []struct {
		predicate string
		match     bool
	}{{"c=d", true}, {"d=c", true}, {"c='a    '", true}, {"'a    '=c", true}, {"t='a'", false}, {"v='a'", false}, {"c=t", false}, {"t=c", false}, {"c=CAST('a' AS text)", true}, {"CAST('a' AS text)=c", true}, {"CAST('a ' AS text)=c", false}, {"c=CAST('a ' AS text)", false}, {"c=NULL", false}, {"u='a'", true}} {
		t.Run(tc.predicate, func(t *testing.T) {
			want := [][]any{}
			if tc.match {
				want = [][]any{{"a  "}}
			}
			wantTuples(t, db, "SELECT c FROM c WHERE "+tc.predicate, want)
		})
	}
	for _, predicate := range []string{"c=v", "v=c", "u=v", "v=u"} {
		wantTuples(t, db, "SELECT c FROM c WHERE "+predicate, [][]any{{"a  "}})
	}
	execSQL(t, db, "CREATE TABLE ordered(v BPCHAR)")
	execSQL(t, db, "INSERT INTO ordered VALUES('b '),('a\n'),('a ') ")
	wantTuples(t, db, "SELECT v FROM ordered ORDER BY v", [][]any{{"a "}, {"a\n"}, {"b "}})
	execSQL(t, db, "CREATE TABLE spaces(c BPCHAR,v VARCHAR,t TEXT)")
	execSQL(t, db, "INSERT INTO spaces VALUES('   ','   ','   ')")
	wantTuples(t, db, "SELECT c FROM spaces WHERE c=''", [][]any{{"   "}})
	for _, col := range []string{"v", "t"} {
		wantTuples(t, db, "SELECT "+col+" FROM spaces WHERE "+col+"=''", [][]any{})
	}
}

func TestCharacterDiagnostics(t *testing.T) {
	// Utility Prepare is metadata-only; semantic type validation occurs during
	// direct or prepared Execute with the same complete diagnostic record.
	for _, tc := range []struct{ decl, message string }{{"char(0)", "length for type char must be at least 1"}, {"varchar(0)", "length for type varchar must be at least 1"}, {"bpchar(-1)", "length for type char must be at least 1"}, {`"varchar"(-1)`, "length for type varchar must be at least 1"}, {"char(10485761)", "length for type char cannot exceed 10485760"}, {"varchar(10485761)", "length for type varchar cannot exceed 10485760"}, {"text(3)", `type modifier is not allowed for type "text"`}} {
		t.Run(tc.decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			q := "/*é🙂*/ CREATE TABLE c(v " + tc.decl + ")"
			position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.decl)]) + 1)
			code := "22023"
			if tc.decl == "text(3)" {
				code = "42601"
			}
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("semantic modifier rejected at Prepare", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: code, Message: tc.message, Position: position})
			}
		})
	}
	for _, decl := range []string{"text", "varchar", "char(2)", "bpchar"} {
		t.Run("input_"+decl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE c(v "+decl+")")
			for _, prepared := range []bool{false, true} {
				for _, value := range []any{"a\x00b", []byte("a\x00b")} {
					var err error
					if prepared {
						s, e := db.Prepare("INSERT INTO c VALUES($1)")
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.Exec(value)
						s.Close()
					} else {
						_, err = db.Exec("INSERT INTO c VALUES($1)", value)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`})
				}
			}
			for _, tc := range []struct {
				payload []byte
				bytes   string
			}{
				{[]byte{0xff}, "0xff"}, {[]byte{0xc3, 0x28}, "0xc3 0x28"}, {[]byte{0xe2, 0x82}, "0xe2 0x82"},
			} {
				for _, value := range []any{string(tc.payload), tc.payload} {
					for _, prepared := range []bool{false, true} {
						var err error
						if prepared {
							s, e := db.Prepare("INSERT INTO c VALUES($1)")
							if e != nil {
								t.Fatal(e)
							}
							_, err = s.Exec(value)
							s.Close()
						} else {
							_, err = db.Exec("INSERT INTO c VALUES($1)", value)
						}
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes})
					}
				}
			}
			wantTuples(t, db, "SELECT v FROM c", [][]any{})
		})
	}
}
