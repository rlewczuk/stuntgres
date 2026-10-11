package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

type nameReferenceValuer struct {
	value driver.Value
	calls *int
}

func (v nameReferenceValuer) Value() (driver.Value, error) { *v.calls++; return v.value, nil }

// Preserve PostgreSQL-first evidence when the optional oracle is selected.
func nameReferenceSteps(t *testing.T, steps []fixture.Step) {
	t.Helper()
	p := fixture.NewScenario(t)
	backends := p.Backends()
	if len(backends) == 2 {
		backends[0], backends[1] = backends[1], backends[0]
	}
	for _, backend := range backends {
		t.Run(backend.Name, func(t *testing.T) {
			for _, s := range steps {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		})
	}
}

func TestReferenceNameValues(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE "MixedObject"(id INT,n NAME,t TEXT)`, 0))
	p.Step(t, fixture.Prepared(fixture.Exec(`CREATE TABLE alias(n "name")`, 0)))
	p.Step(t, fixture.Exec(`INSERT INTO "MixedObject" VALUES(0,'MiXeD','MiXeD')`, 1))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := `INSERT INTO "MixedObject" VALUES($1,$2,$3)`
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			want := [][]any{{int64(0), "MiXeD", "MiXeD"}}
			for i, tc := range []struct{ in, want string }{{"", ""}, {" x ", " x "}, {"O'Brien\\tail\n", "O'Brien\\tail\n"}, {strings.Repeat("a", 62), strings.Repeat("a", 62)}, {strings.Repeat("a", 63), strings.Repeat("a", 63)}, {strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 100), strings.Repeat("a", 63)}, {strings.Repeat("a", 61) + "éZ", strings.Repeat("a", 61) + "é"}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}, {strings.Repeat("a", 60) + "🙂", strings.Repeat("a", 60)}, {strings.Repeat("a", 59) + "🙂Z", strings.Repeat("a", 59) + "🙂"}, {strings.Repeat("é", 32), strings.Repeat("é", 31)}} {
				for mode := range 5 {
					id := int64(i*5 + mode + 1)
					var input any = tc.in
					calls := 0
					if mode == 1 {
						input = []byte(tc.in)
					}
					if mode == 2 {
						input = nameReferenceValuer{[]byte(tc.in), &calls}
					}
					if mode == 3 {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(`INSERT INTO "MixedObject" VALUES($1,'`+strings.ReplaceAll(tc.in, "'", "''")+`',$2)`, 1, id, tc.in))
					} else if mode == 4 {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 1, id, input, tc.in))
					} else {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, id, input, tc.in))
					}
					if mode == 2 && calls != 1 {
						t.Fatal("Valuer calls", calls)
					}
					want = append(want, []any{id, tc.want, tc.in})
				}
			}
			owned := []byte("Owned")
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 100, owned, "bytes"))
			owned[0] = 'X'
			for _, frame := range []struct {
				id    int
				input any
			}{{101, nil}, {102, []byte(nil)}, {103, []byte{}}} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, frame.id, frame.input, nil))
			}
			var typedNil any = ""
			if backend.PG {
				typedNil = nil
			}
			want = append(want, []any{int64(100), "Owned", "bytes"}, []any{int64(101), nil, nil}, []any{int64(102), typedNil, nil}, []any{int64(103), "", nil})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(`SELECT * FROM "MixedObject" ORDER BY id`, []string{"id", "n", "t"}, []string{"INT4", "NAME", "TEXT"}, want))
		})
	}
	p.Step(t, fixture.Exec("CREATE TABLE FOLDED(n NAME)", 0))
	p.Step(t, fixture.Exec("INSERT INTO folded VALUES('MiXeD')", 1))
	p.Step(t, fixture.Select("SELECT n FROM folded", []string{"n"}, []string{"NAME"}, [][]any{{"MiXeD"}}))
}

func TestReferenceNameConversions(t *testing.T) {
	a := strings.Repeat("a", 62)
	nameReferenceSteps(t, []fixture.Step{
		fixture.Exec("CREATE TABLE names(n NAME)", 0), fixture.Exec("INSERT INTO names VALUES(42),(TRUE),(FALSE)", 3), fixture.Prepared(fixture.Exec("INSERT INTO names VALUES($1),($2),($3),($4)", 4, int64(42), true, false, int64(-9223372036854775808))),
		fixture.Exec("INSERT INTO names VALUES($1),($2),($3),($4)", 4, int64(42), true, false, int64(-9223372036854775808)),
		fixture.Select("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{"-9223372036854775808"}, {"-9223372036854775808"}, {"42"}, {"42"}, {"42"}, {"f"}, {"false"}, {"false"}, {"t"}, {"true"}, {"true"}}),
		fixture.Exec("CREATE TABLE src(t TEXT,v VARCHAR,b BPCHAR)", 0), fixture.Exec("INSERT INTO src VALUES($1,$2,$3)", 1, a+"XY", a+"XY", a+" X"), fixture.Exec("CREATE TABLE dst(n NAME)", 0),
		fixture.Exec("INSERT INTO dst SELECT t FROM src", 1), fixture.Prepared(fixture.Exec("INSERT INTO dst SELECT v FROM src", 1)), fixture.Exec("INSERT INTO dst SELECT b FROM src", 1), fixture.Exec("INSERT INTO dst SELECT v FROM src", 1), fixture.Select("SELECT n FROM dst ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{a}, {a + "X"}, {a + "X"}, {a + "X"}}),
		fixture.Exec("CREATE TABLE tails(n NAME)", 0), fixture.Exec("INSERT INTO tails VALUES('a  ')", 1), fixture.Exec("CREATE TABLE outputs(t TEXT,v VARCHAR(4),b CHAR(4))", 0), fixture.Exec("INSERT INTO outputs SELECT n,n,n FROM tails", 1), fixture.Select("SELECT * FROM outputs", []string{"t", "v", "b"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"a  ", "a  ", "a   "}}),
	})
}

func TestReferenceNameComparison(t *testing.T) {
	steps := []fixture.Step{fixture.Exec("CREATE TABLE names(n NAME,t TEXT)", 0)}
	for _, v := range []any{nil, "", "A", "a", "a\t", "a ", "é"} {
		steps = append(steps, fixture.Exec("INSERT INTO names VALUES($1,$2)", 1, v, v))
	}
	steps = append(steps, fixture.Select("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{""}, {"A"}, {"a"}, {"a\t"}, {"a "}, {"é"}, {nil}}), fixture.Select("SELECT n FROM names ORDER BY n DESC", []string{"n"}, []string{"NAME"}, [][]any{{nil}, {"é"}, {"a "}, {"a\t"}, {"a"}, {"A"}, {""}}))
	for _, tc := range []struct {
		op   string
		want [][]any
	}{{"=", [][]any{{"a"}}}, {"<>", [][]any{{""}, {"A"}, {"a\t"}, {"a "}, {"é"}}}, {"<", [][]any{{""}, {"A"}}}, {"<=", [][]any{{""}, {"A"}, {"a"}}}, {">", [][]any{{"a\t"}, {"a "}, {"é"}}}, {">=", [][]any{{"a"}, {"a\t"}, {"a "}, {"é"}}}} {
		s := fixture.Select("SELECT n FROM names WHERE n"+tc.op+"'a' ORDER BY n", []string{"n"}, []string{"NAME"}, tc.want)
		steps = append(steps, s, fixture.Prepared(s))
	}
	steps = append(steps, fixture.Select("SELECT n FROM names WHERE n=$1", []string{"n"}, []string{"NAME"}, nil, nil))
	long, short := strings.Repeat("x", 64), strings.Repeat("x", 63)
	steps = append(steps, fixture.Exec("CREATE TABLE longnames(n NAME,t TEXT)", 0), fixture.Exec("INSERT INTO longnames VALUES($1,$2)", 1, long, long))
	for _, q := range []string{"SELECT n FROM longnames WHERE n<t", "SELECT n FROM longnames WHERE t>n", "SELECT n FROM longnames WHERE n='" + long + "'"} {
		steps = append(steps, fixture.Select(q, []string{"n"}, []string{"NAME"}, [][]any{{short}}))
	}
	s := fixture.Select("SELECT n FROM longnames WHERE n=$1", []string{"n"}, []string{"NAME"}, [][]any{{short}}, long)
	steps = append(steps, s, fixture.Prepared(s), fixture.Select("SELECT n FROM longnames WHERE n=t", []string{"n"}, []string{"NAME"}, nil), fixture.Select("SELECT n FROM longnames WHERE t=n", []string{"n"}, []string{"NAME"}, nil))
	nameReferenceSteps(t, steps)
}

func TestReferenceNameDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE names(n NAME)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE ints(i INT)", 0))
	for _, tc := range []struct {
		q          string
		diagnostic fixture.Diagnostic
		stage      string
	}{
		{`/*é*/ CREATE TABLE bad(n name(3))`, fixture.Diagnostic{Code: "42601", Message: `type modifier is not allowed for type "name"`, Position: "26"}, "execute"},
		{`/*é*/ CREATE TABLE bad(n "NAME")`, fixture.Diagnostic{Code: "42704", Message: `type "NAME" does not exist`, Position: "26"}, "execute"},
		{`/*é*/ INSERT INTO ints(i) SELECT n FROM names`, fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type name`, Position: "34", Hint: "You will need to rewrite or cast the expression."}, "prepare"},
		{`/*é*/ SELECT n FROM names WHERE n=TRUE`, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: name = boolean", Position: "34", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, "prepare"},
	} {
		s := fixture.ImplicitError(tc.q, tc.diagnostic)
		p.Step(t, s)
		s.ErrorStage = tc.stage
		p.Step(t, fixture.Prepared(s))
	}
	p.Step(t, fixture.Exec("INSERT INTO names VALUES('sentinel')", 1))
	unchanged := fixture.Select("SELECT n FROM names", []string{"n"}, []string{"NAME"}, [][]any{{"sentinel"}})
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO names VALUES('ok'),($1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, prefix := range []string{"", strings.Repeat("x", 63)} {
				for _, tc := range []struct{ raw, suffix string }{{"\x00", "0x00"}, {"\xff", "0xff"}, {"\xc3", "0xc3"}, {"\xc3(", "0xc3 0x28"}} {
					for mode := range 3 {
						calls := 0
						var input any = prefix + tc.raw
						if mode == 1 {
							input = []byte(prefix + tc.raw)
						}
						if mode == 2 {
							input = nameReferenceValuer{[]byte(prefix + tc.raw), &calls}
						}
						for _, execution := range []fixture.Executor{backend.DB, held} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"}, input)
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, execution, backend.Recorder, s)
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, unchanged)
						}
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError("INSERT INTO names VALUES($1)", fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"}, input)
							s.Prepared, s.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, unchanged)
						}
					}
				}
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = backend.DB.ExecContext(ctx, "INSERT INTO names VALUES('x')")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, unchanged)
		}()
	}
}

func TestReferenceNameMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE names(n NAME NOT NULL,m NAME,t TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO names VALUES('MiXeD',NULL,'text')", 1))
	for _, backend := range p.Backends() {
		func() {
			q := "SELECT n,m FROM names WHERE n=$1"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			steps := []fixture.Step{
				fixture.Select("SELECT * FROM names", []string{"n", "m", "t"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"MiXeD", nil, "text"}}),
				fixture.Select("SELECT * FROM names WHERE n='absent'", []string{"n", "m", "t"}, []string{"NAME", "NAME", "TEXT"}, nil),
				fixture.Select(q, []string{"n", "m"}, []string{"NAME", "NAME"}, [][]any{{"MiXeD", nil}}, "MiXeD"),
				fixture.Select(q, []string{"n", "m"}, []string{"NAME", "NAME"}, nil, "absent"),
				fixture.Select(q, []string{"n", "m"}, []string{"NAME", "NAME"}, nil, nil),
			}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			for _, step := range steps {
				rows, err := held.QueryContext(ctx, step.Query, step.Args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, typ := range types {
					scan := reflect.TypeFor[string]()
					if backend.PG && i != 2 {
						scan = reflect.TypeFor[any]()
					}
					nullable, known := typ.Nullable()
					length, hasLength := typ.Length()
					_, _, precision := typ.DecimalSize()
					if typ.DatabaseTypeName() != step.Types[i] || typ.ScanType() != scan || known != !backend.PG || nullable != (!backend.PG && i != 0) || precision || hasLength != (i == 2) || i == 2 && length != int64(9223372036854775807) {
						t.Fatal(typ, nullable, known, length, hasLength)
					}
				}
				for rows.Next() {
					values := make([]any, len(types))
					dest := make([]any, len(types))
					for i := range dest {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						fixture.AssertRawValue(t, backend.PG, step.Types[i], value)
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
			}
			var s string
			var b []byte
			var ns sql.NullString
			// Preserve the original non-Context scanner; the recorded companion
			// below is a separate real execution with the recorder context.
			if err := backend.DB.QueryRow("SELECT n,n,m FROM names").Scan(&s, &b, &ns); err != nil || s != "MiXeD" || string(b) != "MiXeD" || ns.Valid {
				t.Fatal(s, b, ns, err)
			}
			if err := backend.DB.QueryRowContext(ctx, "SELECT n,n,m FROM names").Scan(&s, &b, &ns); err != nil || s != "MiXeD" || string(b) != "MiXeD" || ns.Valid {
				t.Fatal(s, b, ns, err)
			}
		}()
	}
}
