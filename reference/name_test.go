//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// PostgreSQL runs first so native admission failures cannot hide oracle evidence.
func nameReferenceSteps(t *testing.T, steps []referenceStep) {
	t.Helper()
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, s := range steps {
				referenceRunStep(t, p.ctx, backend.db, backend.record, s)
			}
		})
	}
}

func TestReferenceNameValues(t *testing.T) {
	steps := []referenceStep{referenceExec(`CREATE TABLE "MixedObject"(id INT,n NAME,t TEXT)`, 0), referencePrepared(referenceExec(`CREATE TABLE alias(n "name")`, 0)), referenceExec(`INSERT INTO "MixedObject" VALUES(0,'MiXeD','MiXeD')`, 1)}
	want := [][]any{{int64(0), "MiXeD", "MiXeD"}}
	for i, tc := range []struct{ in, want string }{{"", ""}, {" x ", " x "}, {"O'Brien\\tail\n", "O'Brien\\tail\n"}, {strings.Repeat("a", 62), strings.Repeat("a", 62)}, {strings.Repeat("a", 63), strings.Repeat("a", 63)}, {strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 100), strings.Repeat("a", 63)}, {strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}, {strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)}, {strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"}, {strings.Repeat("é", 32), strings.Repeat("é", 31)}} {
		for mode := range 3 {
			id := int64(i*3 + mode + 1)
			var input any = tc.in
			if mode == 1 {
				input = []byte(tc.in)
			}
			s := referenceExec(`INSERT INTO "MixedObject" VALUES($1,$2,$3)`, 1, id, input, tc.in)
			if mode == 1 {
				s = referencePrepared(s)
			}
			if mode == 2 {
				s = referenceExec(`INSERT INTO "MixedObject" VALUES($1,'`+strings.ReplaceAll(tc.in, "'", "''")+`',$2)`, 1, id, tc.in)
			}
			steps = append(steps, s)
			want = append(want, []any{id, tc.want, tc.in})
		}
	}
	steps = append(steps, referencePrepared(referenceExec(`INSERT INTO "MixedObject" VALUES(100,$1,$2)`, 1, nil, nil)), referenceSelect(`SELECT * FROM "MixedObject" ORDER BY id`, []string{"id", "n", "t"}, []string{"INT4", "NAME", "TEXT"}, append(want, []any{int64(100), nil, nil})))
	nameReferenceSteps(t, steps)
}

func TestReferenceNameConversions(t *testing.T) {
	a := strings.Repeat("a", 62)
	nameReferenceSteps(t, []referenceStep{
		referenceExec("CREATE TABLE names(n NAME)", 0), referenceExec("INSERT INTO names VALUES(42),(TRUE),(FALSE)", 3), referencePrepared(referenceExec("INSERT INTO names VALUES($1),($2),($3)", 3, int64(42), true, false)),
		referenceSelect("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{"42"}, {"42"}, {"f"}, {"false"}, {"t"}, {"true"}}),
		referenceExec("CREATE TABLE src(t TEXT,v VARCHAR,b BPCHAR)", 0), referenceExec("INSERT INTO src VALUES($1,$2,$3)", 1, a+"XY", a+"XY", a+" X"), referenceExec("CREATE TABLE dst(n NAME)", 0),
		referenceExec("INSERT INTO dst SELECT t FROM src", 1), referencePrepared(referenceExec("INSERT INTO dst SELECT v FROM src", 1)), referenceExec("INSERT INTO dst SELECT b FROM src", 1), referenceSelect("SELECT n FROM dst ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{a}, {a + "X"}, {a + "X"}}),
		referenceExec("CREATE TABLE tails(n NAME)", 0), referenceExec("INSERT INTO tails VALUES('a  ')", 1), referenceExec("CREATE TABLE outputs(t TEXT,v VARCHAR(4),b CHAR(4))", 0), referenceExec("INSERT INTO outputs SELECT n,n,n FROM tails", 1), referenceSelect("SELECT * FROM outputs", []string{"t", "v", "b"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"a  ", "a  ", "a   "}}),
	})
}

func TestReferenceNameComparison(t *testing.T) {
	steps := []referenceStep{referenceExec("CREATE TABLE names(n NAME)", 0)}
	for _, v := range []any{nil, "", "A", "a", "a\t", "a ", "é"} {
		steps = append(steps, referenceExec("INSERT INTO names VALUES($1)", 1, v))
	}
	steps = append(steps, referenceSelect("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{""}, {"A"}, {"a"}, {"a\t"}, {"a "}, {"é"}, {nil}}), referenceSelect("SELECT n FROM names ORDER BY n DESC", []string{"n"}, []string{"NAME"}, [][]any{{nil}, {"é"}, {"a "}, {"a\t"}, {"a"}, {"A"}, {""}}))
	for _, tc := range []struct {
		op   string
		want [][]any
	}{{"=", [][]any{{"a"}}}, {"<>", [][]any{{""}, {"A"}, {"a\t"}, {"a "}, {"é"}}}, {"<", [][]any{{""}, {"A"}}}, {"<=", [][]any{{""}, {"A"}, {"a"}}}, {">", [][]any{{"a\t"}, {"a "}, {"é"}}}, {">=", [][]any{{"a"}, {"a\t"}, {"a "}, {"é"}}}} {
		steps = append(steps, referencePrepared(referenceSelect("SELECT n FROM names WHERE n"+tc.op+"'a' ORDER BY n", []string{"n"}, []string{"NAME"}, tc.want)))
	}
	long := strings.Repeat("x", 64)
	short := strings.Repeat("x", 63)
	steps = append(steps, referenceExec("CREATE TABLE longnames(n NAME,t TEXT)", 0), referenceExec("INSERT INTO longnames VALUES($1,$2)", 1, long, long))
	for _, q := range []string{"SELECT n FROM longnames WHERE n<t", "SELECT n FROM longnames WHERE t>n", "SELECT n FROM longnames WHERE n='" + long + "'"} {
		steps = append(steps, referenceSelect(q, []string{"n"}, []string{"NAME"}, [][]any{{short}}))
	}
	steps = append(steps, referencePrepared(referenceSelect("SELECT n FROM longnames WHERE n=$1", []string{"n"}, []string{"NAME"}, [][]any{{short}}, long)), referenceSelect("SELECT n FROM longnames WHERE n=t", []string{"n"}, []string{"NAME"}, [][]any{}), referenceSelect("SELECT n FROM longnames WHERE t=n", []string{"n"}, []string{"NAME"}, [][]any{}))
	nameReferenceSteps(t, steps)
}

func TestReferenceNameDiagnostics(t *testing.T) {
	steps := []referenceStep{referenceExec("CREATE TABLE names(n NAME)", 0), referenceExec("CREATE TABLE ints(i INT)", 0)}
	for _, tc := range []struct {
		q          string
		diagnostic referenceDiagnostic
		stage      string
	}{
		{`/*é*/ CREATE TABLE bad(n name(3))`, referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "name"`, Position: "26"}, "execute"},
		{`/*é*/ CREATE TABLE bad(n "NAME")`, referenceDiagnostic{Code: "42704", Message: `type "NAME" does not exist`, Position: "26"}, "execute"},
		{`/*é*/ INSERT INTO ints(i) SELECT n FROM names`, referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type name`, Position: "34", Hint: "You will need to rewrite or cast the expression."}, "prepare"},
		{`/*é*/ SELECT n FROM names WHERE n=TRUE`, referenceDiagnostic{Code: "42883", Message: "operator does not exist: name = boolean", Position: "34", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, "prepare"},
	} {
		s := referenceImplicitError(tc.q, tc.diagnostic)
		steps = append(steps, s)
		s = referencePrepared(s)
		s.errorStage = tc.stage
		steps = append(steps, s)
	}
	steps = append(steps, referenceExec("INSERT INTO names VALUES('sentinel')", 1))
	for _, prefix := range []string{"", strings.Repeat("x", 63)} {
		for _, tc := range []struct{ raw, suffix string }{{"\x00", "0x00"}, {"\xff", "0xff"}, {"\xc3", "0xc3"}, {"\xc3(", "0xc3 0x28"}} {
			for _, input := range []any{prefix + tc.raw, []byte(prefix + tc.raw)} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO names VALUES($1)", referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix}, input)
					pg := *s.diagnostic
					pg.Context = "unnamed portal parameter $1"
					s.postgresDiagnostic = &pg
					s.prepared = prepared
					s.errorStage = "execute"
					steps = append(steps, s)
				}
			}
		}
	}
	steps = append(steps, referenceSelect("SELECT n FROM names", []string{"n"}, []string{"NAME"}, [][]any{{"sentinel"}}))
	nameReferenceSteps(t, steps)
}

func TestReferenceNameMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		scan     reflect.Type
		nullable bool
	}{{"postgres", p.postgres, reflect.TypeFor[any](), false}, {"native", p.memory, reflect.TypeFor[string](), true}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, q := range []string{"CREATE TABLE names(n NAME NOT NULL,m NAME,t TEXT)", "INSERT INTO names VALUES('MiXeD',NULL,'text')"} {
				if _, err := backend.db.ExecContext(p.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			stmt, err := backend.db.PrepareContext(p.ctx, "SELECT n,m FROM names WHERE n=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, arg := range []any{"MiXeD", "absent", nil} {
				rows, err := stmt.QueryContext(p.ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, typ := range types {
					nullable, known := typ.Nullable()
					if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != backend.scan || known != backend.nullable || known && nullable != (i != 0) {
						t.Fatal(typ, nullable, known)
					}
					if _, ok := typ.Length(); ok {
						t.Fatal("length", typ)
					}
					if _, _, ok := typ.DecimalSize(); ok {
						t.Fatal("precision", typ)
					}
				}
				if rows.Next() {
					var a, b any
					if err = rows.Scan(&a, &b); err != nil {
						t.Fatal(err)
					}
					if backend.name == "postgres" {
						value, ok := a.([]byte)
						if !ok || string(value) != "MiXeD" {
							t.Fatal(a)
						}
					} else if a != "MiXeD" {
						t.Fatal(a)
					}
					if b != nil {
						t.Fatal(b)
					}
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
		})
	}
}
