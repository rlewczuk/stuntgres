package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

type nameValuer struct {
	value driver.Value
	calls *int
}

func (v nameValuer) Value() (driver.Value, error) { *v.calls++; return v.value, nil }

func TestNameValues(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "MixedObject"(id INT,n NAME,t TEXT)`)
	execSQL(t, db, `INSERT INTO "MixedObject" VALUES(0,'MiXeD','MiXeD')`)
	stmt, err := db.Prepare(`INSERT INTO "MixedObject" VALUES($1,$2,$3)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	cases := []struct{ in, want string }{{"", ""}, {" x ", " x "}, {"O'Brien\\tail\n", "O'Brien\\tail\n"}, {strings.Repeat("a", 62), strings.Repeat("a", 62)}, {strings.Repeat("a", 63), strings.Repeat("a", 63)}, {strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 100), strings.Repeat("a", 63)}, {strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}, {strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)}, {strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"}, {strings.Repeat("é", 32), strings.Repeat("é", 31)}}
	want := [][]any{{int64(0), "MiXeD", "MiXeD"}}
	for i, tc := range cases {
		for mode := range 4 {
			id := i*4 + mode + 1
			var input any = tc.in
			calls := 0
			if mode == 1 {
				input = []byte(tc.in)
			}
			if mode == 2 {
				input = nameValuer{[]byte(tc.in), &calls}
			}
			if mode == 3 {
				execSQL(t, db, `INSERT INTO "MixedObject" VALUES($1,'`+strings.ReplaceAll(tc.in, "'", "''")+`',$2)`, id, tc.in)
			} else if _, err = stmt.Exec(id, input, tc.in); err != nil {
				t.Fatal(err)
			}
			if mode == 2 && calls != 1 {
				t.Fatal("Valuer calls", calls)
			}
			want = append(want, []any{int64(id), tc.want, tc.in})
		}
	}
	owned := []byte("Owned")
	if _, err = stmt.Exec(100, owned, "bytes"); err != nil {
		t.Fatal(err)
	}
	owned[0] = 'X'
	if _, err = stmt.Exec(101, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.Exec(102, []byte(nil), nil); err != nil {
		t.Fatal(err)
	}
	want = append(want, []any{int64(100), "Owned", "bytes"}, []any{int64(101), nil, nil}, []any{int64(102), "", nil})
	wantTuples(t, db, `SELECT * FROM "MixedObject" ORDER BY id`, want)
	execSQL(t, db, "CREATE TABLE FOLDED(n NAME)")
	execSQL(t, db, "INSERT INTO folded VALUES('MiXeD')")
	wantTuples(t, db, "SELECT n FROM folded", [][]any{{"MiXeD"}})
}

func TestNameMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE names(n NAME NOT NULL,m NAME,t TEXT)")
	execSQL(t, db, "INSERT INTO names VALUES('MiXeD',NULL,'text')")
	for _, q := range []string{"SELECT * FROM names", "SELECT * FROM names WHERE n='absent'"} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{"NAME", "NAME", "TEXT"} {
			typ := types[i]
			nullable, ok := typ.Nullable()
			if typ.DatabaseTypeName() != want || typ.ScanType() != reflect.TypeFor[string]() || !ok || nullable != (i != 0) {
				t.Fatal(typ)
			}
			length, known := typ.Length()
			if want == "NAME" && known || want == "TEXT" && (!known || length != int64(9223372036854775807)) {
				t.Fatal("length", typ, length, known)
			}
			if _, _, ok = typ.DecimalSize(); ok {
				t.Fatal("precision", typ)
			}
		}
		if rows.Next() {
			var n, m, text any
			if err = rows.Scan(&n, &m, &text); err != nil || n != "MiXeD" || m != nil || text != "text" {
				t.Fatal(n, m, text, err)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	var s string
	var b []byte
	var ns sql.NullString
	if err := db.QueryRow("SELECT n,n,m FROM names").Scan(&s, &b, &ns); err != nil || s != "MiXeD" || string(b) != "MiXeD" || ns.Valid {
		t.Fatal(s, b, ns, err)
	}
}

func TestNameConversions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE names(n NAME)")
	execSQL(t, db, "INSERT INTO names VALUES(42),(TRUE),(FALSE)")
	execSQL(t, db, "INSERT INTO names VALUES($1),($2),($3),($4)", int64(42), true, false, int64(-9223372036854775808))
	wantTuples(t, db, "SELECT n FROM names ORDER BY n", [][]any{{"-9223372036854775808"}, {"42"}, {"42"}, {"f"}, {"false"}, {"t"}, {"true"}})
	execSQL(t, db, "CREATE TABLE src(t TEXT,v VARCHAR,b BPCHAR)")
	a := strings.Repeat("a", 62)
	execSQL(t, db, "INSERT INTO src VALUES($1,$2,$3)", a+"XY", a+"XY", a+" X")
	execSQL(t, db, "CREATE TABLE dst(n NAME)")
	for _, column := range []string{"t", "v", "b"} {
		execSQL(t, db, "INSERT INTO dst SELECT "+column+" FROM src")
	}
	wantTuples(t, db, "SELECT n FROM dst ORDER BY n", [][]any{{a}, {a + "X"}, {a + "X"}})
	execSQL(t, db, "CREATE TABLE tails(n NAME)")
	execSQL(t, db, "INSERT INTO tails VALUES('a  ')")
	execSQL(t, db, "CREATE TABLE outputs(t TEXT,v VARCHAR(4),b CHAR(4))")
	execSQL(t, db, "INSERT INTO outputs SELECT n,n,n FROM tails")
	wantTuples(t, db, "SELECT * FROM outputs", [][]any{{"a  ", "a  ", "a   "}})
}

func TestNameComparison(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE names(n NAME,t TEXT)")
	for _, v := range []any{nil, "", "A", "a", "a\t", "a ", "é"} {
		execSQL(t, db, "INSERT INTO names VALUES($1,$2)", v, v)
	}
	wantTuples(t, db, "SELECT n FROM names ORDER BY n", [][]any{{""}, {"A"}, {"a"}, {"a\t"}, {"a "}, {"é"}, {nil}})
	wantTuples(t, db, "SELECT n FROM names ORDER BY n DESC", [][]any{{nil}, {"é"}, {"a "}, {"a\t"}, {"a"}, {"A"}, {""}})
	for _, tc := range []struct {
		op   string
		want [][]any
	}{{"=", [][]any{{"a"}}}, {"<>", [][]any{{""}, {"A"}, {"a\t"}, {"a "}, {"é"}}}, {"<", [][]any{{""}, {"A"}}}, {"<=", [][]any{{""}, {"A"}, {"a"}}}, {">", [][]any{{"a\t"}, {"a "}, {"é"}}}, {">=", [][]any{{"a"}, {"a\t"}, {"a "}, {"é"}}}} {
		wantTuples(t, db, "SELECT n FROM names WHERE n"+tc.op+"'a' ORDER BY n", tc.want)
	}
	wantTuples(t, db, "SELECT n FROM names WHERE n=$1", [][]any{}, nil)
	execSQL(t, db, "CREATE TABLE longnames(n NAME,t TEXT)")
	long := strings.Repeat("x", 64)
	short := strings.Repeat("x", 63)
	execSQL(t, db, "INSERT INTO longnames VALUES($1,$2)", long, long)
	for _, q := range []string{"SELECT n FROM longnames WHERE n<t", "SELECT n FROM longnames WHERE t>n", "SELECT n FROM longnames WHERE n='" + long + "'", "SELECT n FROM longnames WHERE n=$1"} {
		if strings.Contains(q, "$1") {
			wantTuples(t, db, q, [][]any{{short}}, long)
		} else {
			wantTuples(t, db, q, [][]any{{short}})
		}
	}
	wantTuples(t, db, "SELECT n FROM longnames WHERE n=t", [][]any{})
	wantTuples(t, db, "SELECT n FROM longnames WHERE t=n", [][]any{})
}

func TestNameDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE names(n NAME)")
	execSQL(t, db, "CREATE TABLE ints(i INT)")
	for _, tc := range []struct {
		q       string
		want    stuntgres.Error
		utility bool
	}{
		{`/*é*/ CREATE TABLE bad(n name(3))`, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "name"`, Position: "26"}, true},
		{`/*é*/ CREATE TABLE bad(n "NAME")`, stuntgres.Error{Code: "42704", Message: `type "NAME" does not exist`, Position: "26"}, true},
		{`/*é*/ INSERT INTO ints(i) SELECT n FROM names`, stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type name`, Position: "34", Hint: "You will need to rewrite or cast the expression."}, false},
		{`/*é*/ SELECT n FROM names WHERE n=TRUE`, stuntgres.Error{Code: "42883", Message: "operator does not exist: name = boolean", Position: "34", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, false},
	} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if tc.utility {
			if err != nil {
				t.Fatal("utility Prepare", err)
			}
			_, err = stmt.Exec()
			stmt.Close()
		} else if stmt != nil {
			stmt.Close()
			t.Fatal("semantic failure exposed statement")
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	execSQL(t, db, "INSERT INTO names VALUES('sentinel')")
	stmt, err := db.Prepare("INSERT INTO names VALUES('ok'),($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prefix := range []string{"", strings.Repeat("x", 63)} {
		for _, tc := range []struct{ raw, suffix string }{{"\x00", "0x00"}, {"\xff", "0xff"}, {"\xc3", "0xc3"}, {"\xc3(", "0xc3 0x28"}} {
			for mode := range 3 {
				var arg any = prefix + tc.raw
				calls := 0
				if mode == 1 {
					arg = []byte(prefix + tc.raw)
				}
				if mode == 2 {
					arg = nameValuer{[]byte(prefix + tc.raw), &calls}
				}
				_, err = stmt.Exec(arg)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"})
				_, err = db.Exec("INSERT INTO names VALUES('ok'),($1)", arg)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"})
				wantTuples(t, db, "SELECT n FROM names", [][]any{{"sentinel"}})
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = db.ExecContext(ctx, "INSERT INTO names VALUES('x')")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
