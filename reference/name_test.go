package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type nameReferenceValuer struct {
	value driver.Value
	calls *int
}

func (v nameReferenceValuer) Value() (driver.Value, error) { *v.calls++; return v.value, nil }

// Preserve PostgreSQL-first evidence when the optional oracle is selected.
func nameReferenceSteps(t *testing.T, steps []referenceStep) {
	t.Helper()
	p := newReferenceScenario(t)
	backends := p.backends()
	if len(backends) == 2 {
		backends[0], backends[1] = backends[1], backends[0]
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			for _, s := range steps {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		})
	}
}

func TestReferenceNameValues(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec(`CREATE TABLE "MixedObject"(id INT,n NAME,t TEXT)`, 0))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE alias(n "name")`, 0)))
	p.step(t, referenceExec(`INSERT INTO "MixedObject" VALUES(0,'MiXeD','MiXeD')`, 1))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := `INSERT INTO "MixedObject" VALUES($1,$2,$3)`
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
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
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(`INSERT INTO "MixedObject" VALUES($1,'`+strings.ReplaceAll(tc.in, "'", "''")+`',$2)`, 1, id, tc.in))
					} else if mode == 4 {
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 1, id, input, tc.in))
					} else {
						referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, id, input, tc.in))
					}
					if mode == 2 && calls != 1 {
						t.Fatal("Valuer calls", calls)
					}
					want = append(want, []any{id, tc.want, tc.in})
				}
			}
			owned := []byte("Owned")
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 100, owned, "bytes"))
			owned[0] = 'X'
			for _, frame := range []struct {
				id    int
				input any
			}{{101, nil}, {102, []byte(nil)}, {103, []byte{}}} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, frame.id, frame.input, nil))
			}
			var typedNil any = ""
			if backend.pg {
				typedNil = nil
			}
			want = append(want, []any{int64(100), "Owned", "bytes"}, []any{int64(101), nil, nil}, []any{int64(102), typedNil, nil}, []any{int64(103), "", nil})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect(`SELECT * FROM "MixedObject" ORDER BY id`, []string{"id", "n", "t"}, []string{"INT4", "NAME", "TEXT"}, want))
		})
	}
	p.step(t, referenceExec("CREATE TABLE FOLDED(n NAME)", 0))
	p.step(t, referenceExec("INSERT INTO folded VALUES('MiXeD')", 1))
	p.step(t, referenceSelect("SELECT n FROM folded", []string{"n"}, []string{"NAME"}, [][]any{{"MiXeD"}}))
}

func TestReferenceNameConversions(t *testing.T) {
	a := strings.Repeat("a", 62)
	nameReferenceSteps(t, []referenceStep{
		referenceExec("CREATE TABLE names(n NAME)", 0), referenceExec("INSERT INTO names VALUES(42),(TRUE),(FALSE)", 3), referencePrepared(referenceExec("INSERT INTO names VALUES($1),($2),($3),($4)", 4, int64(42), true, false, int64(-9223372036854775808))),
		referenceExec("INSERT INTO names VALUES($1),($2),($3),($4)", 4, int64(42), true, false, int64(-9223372036854775808)),
		referenceSelect("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{"-9223372036854775808"}, {"-9223372036854775808"}, {"42"}, {"42"}, {"42"}, {"f"}, {"false"}, {"false"}, {"t"}, {"true"}, {"true"}}),
		referenceExec("CREATE TABLE src(t TEXT,v VARCHAR,b BPCHAR)", 0), referenceExec("INSERT INTO src VALUES($1,$2,$3)", 1, a+"XY", a+"XY", a+" X"), referenceExec("CREATE TABLE dst(n NAME)", 0),
		referenceExec("INSERT INTO dst SELECT t FROM src", 1), referencePrepared(referenceExec("INSERT INTO dst SELECT v FROM src", 1)), referenceExec("INSERT INTO dst SELECT b FROM src", 1), referenceExec("INSERT INTO dst SELECT v FROM src", 1), referenceSelect("SELECT n FROM dst ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{a}, {a + "X"}, {a + "X"}, {a + "X"}}),
		referenceExec("CREATE TABLE tails(n NAME)", 0), referenceExec("INSERT INTO tails VALUES('a  ')", 1), referenceExec("CREATE TABLE outputs(t TEXT,v VARCHAR(4),b CHAR(4))", 0), referenceExec("INSERT INTO outputs SELECT n,n,n FROM tails", 1), referenceSelect("SELECT * FROM outputs", []string{"t", "v", "b"}, []string{"TEXT", "VARCHAR", "BPCHAR"}, [][]any{{"a  ", "a  ", "a   "}}),
	})
}

func TestReferenceNameComparison(t *testing.T) {
	steps := []referenceStep{referenceExec("CREATE TABLE names(n NAME,t TEXT)", 0)}
	for _, v := range []any{nil, "", "A", "a", "a\t", "a ", "é"} {
		steps = append(steps, referenceExec("INSERT INTO names VALUES($1,$2)", 1, v, v))
	}
	steps = append(steps, referenceSelect("SELECT n FROM names ORDER BY n", []string{"n"}, []string{"NAME"}, [][]any{{""}, {"A"}, {"a"}, {"a\t"}, {"a "}, {"é"}, {nil}}), referenceSelect("SELECT n FROM names ORDER BY n DESC", []string{"n"}, []string{"NAME"}, [][]any{{nil}, {"é"}, {"a "}, {"a\t"}, {"a"}, {"A"}, {""}}))
	for _, tc := range []struct {
		op   string
		want [][]any
	}{{"=", [][]any{{"a"}}}, {"<>", [][]any{{""}, {"A"}, {"a\t"}, {"a "}, {"é"}}}, {"<", [][]any{{""}, {"A"}}}, {"<=", [][]any{{""}, {"A"}, {"a"}}}, {">", [][]any{{"a\t"}, {"a "}, {"é"}}}, {">=", [][]any{{"a"}, {"a\t"}, {"a "}, {"é"}}}} {
		s := referenceSelect("SELECT n FROM names WHERE n"+tc.op+"'a' ORDER BY n", []string{"n"}, []string{"NAME"}, tc.want)
		steps = append(steps, s, referencePrepared(s))
	}
	steps = append(steps, referenceSelect("SELECT n FROM names WHERE n=$1", []string{"n"}, []string{"NAME"}, nil, nil))
	long, short := strings.Repeat("x", 64), strings.Repeat("x", 63)
	steps = append(steps, referenceExec("CREATE TABLE longnames(n NAME,t TEXT)", 0), referenceExec("INSERT INTO longnames VALUES($1,$2)", 1, long, long))
	for _, q := range []string{"SELECT n FROM longnames WHERE n<t", "SELECT n FROM longnames WHERE t>n", "SELECT n FROM longnames WHERE n='" + long + "'"} {
		steps = append(steps, referenceSelect(q, []string{"n"}, []string{"NAME"}, [][]any{{short}}))
	}
	s := referenceSelect("SELECT n FROM longnames WHERE n=$1", []string{"n"}, []string{"NAME"}, [][]any{{short}}, long)
	steps = append(steps, s, referencePrepared(s), referenceSelect("SELECT n FROM longnames WHERE n=t", []string{"n"}, []string{"NAME"}, nil), referenceSelect("SELECT n FROM longnames WHERE t=n", []string{"n"}, []string{"NAME"}, nil))
	nameReferenceSteps(t, steps)
}

func TestReferenceNameDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE names(n NAME)", 0))
	p.step(t, referenceExec("CREATE TABLE ints(i INT)", 0))
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
		p.step(t, s)
		s.errorStage = tc.stage
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("INSERT INTO names VALUES('sentinel')", 1))
	unchanged := referenceSelect("SELECT n FROM names", []string{"n"}, []string{"NAME"}, [][]any{{"sentinel"}})
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO names VALUES('ok'),($1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
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
						for _, execution := range []referenceExecutor{backend.db, held} {
							s := referenceImplicitError(q, referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"}, input)
							s.errorStage = "execute"
							referenceRunStep(t, p.ctx, execution, backend.recorder, s)
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, unchanged)
						}
						for _, prepared := range []bool{false, true} {
							s := referenceImplicitError("INSERT INTO names VALUES($1)", referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.suffix, Context: "unnamed portal parameter $1"}, input)
							s.prepared, s.errorStage = prepared, "execute"
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, unchanged)
						}
					}
				}
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err = backend.db.ExecContext(ctx, "INSERT INTO names VALUES('x')")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, unchanged)
		}()
	}
}

func TestReferenceNameMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE names(n NAME NOT NULL,m NAME,t TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO names VALUES('MiXeD',NULL,'text')", 1))
	for _, backend := range p.backends() {
		func() {
			q := "SELECT n,m FROM names WHERE n=$1"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			steps := []referenceStep{
				referenceSelect("SELECT * FROM names", []string{"n", "m", "t"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"MiXeD", nil, "text"}}),
				referenceSelect("SELECT * FROM names WHERE n='absent'", []string{"n", "m", "t"}, []string{"NAME", "NAME", "TEXT"}, nil),
				referenceSelect(q, []string{"n", "m"}, []string{"NAME", "NAME"}, [][]any{{"MiXeD", nil}}, "MiXeD"),
				referenceSelect(q, []string{"n", "m"}, []string{"NAME", "NAME"}, nil, "absent"),
				referenceSelect(q, []string{"n", "m"}, []string{"NAME", "NAME"}, nil, nil),
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			for _, step := range steps {
				rows, err := held.QueryContext(ctx, step.query, step.args...)
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
					if backend.pg && i != 2 {
						scan = reflect.TypeFor[any]()
					}
					nullable, known := typ.Nullable()
					length, hasLength := typ.Length()
					_, _, precision := typ.DecimalSize()
					if typ.DatabaseTypeName() != step.types[i] || typ.ScanType() != scan || known != !backend.pg || nullable != (!backend.pg && i != 0) || precision || hasLength != (i == 2) || i == 2 && length != int64(9223372036854775807) {
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
						referenceAssertRawValue(t, backend.pg, step.types[i], value)
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, held, backend.recorder, step)
			}
			var s string
			var b []byte
			var ns sql.NullString
			// Preserve the original non-Context scanner; the recorded companion
			// below is a separate real execution with the recorder context.
			if err := backend.db.QueryRow("SELECT n,n,m FROM names").Scan(&s, &b, &ns); err != nil || s != "MiXeD" || string(b) != "MiXeD" || ns.Valid {
				t.Fatal(s, b, ns, err)
			}
			if err := backend.db.QueryRowContext(ctx, "SELECT n,n,m FROM names").Scan(&s, &b, &ns); err != nil || s != "MiXeD" || string(b) != "MiXeD" || ns.Valid {
				t.Fatal(s, b, ns, err)
			}
		}()
	}
}
