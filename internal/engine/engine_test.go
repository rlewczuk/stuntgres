package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var testSessionID atomic.Uint64

func testOperation(e *Engine, ctx context.Context) (*Operation, func(), error) {
	s, err := e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		return nil, nil, err
	}
	op, err := s.Acquire(ctx)
	cleanup := func() {
		if op != nil {
			op.Done()
		}
		s.RequestEnd(CleanClose)
		_ = s.WaitEnd(context.Background())
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return op, cleanup, nil
}
func testExecute(e *Engine, ctx context.Context, stmt parser.Statement, args []driver.NamedValue) (Result, error) {
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	return testExecuteFrame(e, op, stmt, args)
}
func testDescribe(e *Engine, stmt parser.Statement) (ParameterInfo, error) {
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		return ParameterInfo{}, err
	}
	defer cleanup()
	description, err := e.Describe(op, stmt)
	return description.Parameters, err
}
func statement(t *testing.T, sql string) parser.Statement {
	t.Helper()
	s, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("expected single statement, got %d", len(s))
	}
	return s[0]
}
func run(t *testing.T, e *Engine, sql string, args ...any) Result {
	t.Helper()
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	r, err := testExecute(e, context.Background(), statement(t, sql), named)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

// Tuple assertions use an independent retained cursor so lifetime tests may
// inspect the original result later without rewinding its public position.
func independentFinalRows(original exec.RowResult) *exec.FinalRows {
	old := original.(*exec.FinalRows)
	r := exec.NewFinalRows(old.Columns, old.Count)
	r.Values, r.Selection, r.Slots = old.Values, old.Selection, old.Slots
	r.Anonymous = old.Anonymous
	r.RegclassOutput = old.RegclassOutput
	if old.Source != nil {
		source := *old.Source
		if source.View != nil {
			source.View = source.View.Retain()
		}
		r.Source = &source
	}
	return r
}
func tuples(r Result) [][]driver.Value {
	cursor := independentFinalRows(r.Rows)
	defer cursor.Close()
	rows := make([][]driver.Value, 0, cursor.Count)
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			panic(err)
		}
		if !present {
			break
		}
		row := make([]driver.Value, len(values))
		for j, v := range values {
			row[j] = testExport(v)
		}
		rows = append(rows, row)
	}
	return rows
}

func testExport(v sqltype.Value) driver.Value {
	if v.IsNull() {
		return nil
	}
	if v.Type().BaseType().IsArray() || v.Type().BaseType().IsEnum() {
		s, err := v.Type().Output(context.Background(), v)
		if err != nil {
			panic(err)
		}
		return s
	}
	switch v.Type().ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		n, err := v.Integer()
		if err != nil {
			panic(err)
		}
		return n
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		s, err := v.Text()
		if err != nil {
			panic(err)
		}
		return s
	case sqltype.Bool:
		b, err := v.Boolean()
		if err != nil {
			panic(err)
		}
		return b
	}
	panic("invalid test value")
}
func testUsage(e *Engine, id sqltype.ID) sqltype.Usage {
	d, ok := e.CatalogRead().LookupType(id)
	if !ok {
		panic("missing type")
	}
	return d.Usage()
}
func testExecuteFrame(e *Engine, op *Operation, stmt parser.Statement, args []driver.NamedValue) (Result, error) {
	raw := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" || a.Ordinal != i+1 {
			return Result{}, fmt.Errorf("invalid test argument")
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a.Value)
		if err != nil {
			return Result{}, err
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		raw[i] = v
	}
	return e.Execute(op, stmt, len(raw), func(ctx context.Context, p ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		for i, v := range raw {
			if v == nil {
				dst[i] = sqltype.Null(p.Types[i].Type)
				continue
			}
			var s string
			switch v := v.(type) {
			case string:
				s = v
			case int64:
				s = strconv.FormatInt(v, 10)
			case bool:
				s = strconv.FormatBool(v)
			default:
				return sqlError("0A000", "unsupported parameter value")
			}
			value, err := p.Types[i].Type.InputWithEnvironment(ctx, s, -1, input)
			if err != nil {
				if p.Types[i].Type.ContainsRegclass() {
					return sequenceInputError(err, parser.Position{}, "", i+1)
				}
				return err
			}
			dst[i] = value
		}
		return nil
	})
}
func state(t *testing.T, err error, want string) {
	t.Helper()
	var s interface{ SQLState() string }
	if !errors.As(err, &s) || s.SQLState() != want {
		t.Fatalf("error %v, want SQLSTATE %s", err, want)
	}
}
func TestGenericCreateDiagnosticsHaveNoObjectFields(t *testing.T) {
	e := New()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "t"}}}
	for _, tc := range []struct {
		stmt          parser.Statement
		code, message string
	}{
		{&parser.CreateTable{Name: name, Elements: []parser.TableElement{{Column: &parser.Column{Name: "id", TypeName: parser.TypeName{Name: "integer"}}}, {Column: &parser.Column{Name: "id", TypeName: parser.TypeName{Name: "text"}}}}}, "42701", `column "id" specified more than once`},
		{&parser.CreateTable{Name: name, Elements: []parser.TableElement{{Column: &parser.Column{Name: "id", TypeName: parser.TypeName{Name: "unsupported"}}}}}, "42704", `type "unsupported" does not exist`},
	} {
		if _, err := testDescribe(e, tc.stmt); err != nil {
			t.Fatalf("CREATE preparation: %v", err)
		}
		_, err := testExecute(e, context.Background(), tc.stmt, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("generic CREATE error = %#v (%v), want %#v", got, err, want)
		}
	}
}
func TestBindingAndSemanticValidation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t (a INTEGER, b TEXT, c INTEGER)")
	info, err := testDescribe(e, statement(t, "INSERT INTO t VALUES ($1, $2, $1)"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Count != 2 || info.Types[0].Type.ID() != sqltype.Int4 || info.Types[1].Type.ID() != sqltype.Text {
		t.Fatalf("bad parameter types: %+v", info)
	}
	for _, c := range []struct{ sql, code string }{
		{"SELECT missing FROM t", "42703"}, {"SELECT a FROM t WHERE missing = 1", "42703"}, {"SELECT a FROM t ORDER BY missing", "42703"}, {"SELECT a FROM missing", "42P01"},
		{"INSERT INTO t VALUES ($2, 'x', $2)", "42P18"}, {"INSERT INTO t VALUES ($1, $1, 1)", "0A000"}, {"INSERT INTO t (a,a) VALUES (1,2)", "42701"},
		{"SELECT a FROM t WHERE a = TRUE", "42883"}, {"SELECT b FROM t WHERE b = 1", "42883"},
	} {
		t.Run(c.sql, func(t *testing.T) { _, err := testDescribe(e, statement(t, c.sql)); state(t, err, c.code) })
	}
	s := statement(t, "INSERT INTO t VALUES ($1,$2,$1)")
	for _, args := range [][]driver.NamedValue{{{Ordinal: 1, Value: 1}}, {{Ordinal: 2, Value: 1}, {Ordinal: 1, Value: "x"}}, {{Ordinal: 1, Name: "a", Value: 1}, {Ordinal: 2, Value: "x"}}} {
		if _, err := testExecute(e, context.Background(), s, args); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	run(t, e, "INSERT INTO t VALUES ($1,$2,$1)", int32(7), "x")
	want := [][]driver.Value{{int64(7), "x", int64(7)}}
	if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestImplicitShortTargetDescribe(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE short_values (a INT, b TEXT)")
	info, err := testDescribe(e, statement(t, "INSERT INTO short_values VALUES ($1)"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Count != 1 || info.Types[0].Type.ID() != sqltype.Int4 {
		t.Fatalf("short target parameter info: %+v", info)
	}
	run(t, e, "INSERT INTO short_values VALUES ($1),($1)", 7)
	if got, want := tuples(run(t, e, "SELECT * FROM short_values ORDER BY a")), [][]driver.Value{{int64(7), nil}, {int64(7), nil}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("short implicit rows got %v want %v", got, want)
	}
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestInsertImplicitShortStagedCancellation(t *testing.T) {
	// Instrument only the admitted operation's execution context: session
	// initialization and acquisition finish first. Scope/executor admission
	// checks at 1–6; rows/typed expressions at 7–15, constraints at 16–18,
	// final publication at 22 after staging and Finish admission.
	for _, tc := range []struct {
		name  string
		check int32
	}{
		{"after_first_staged_row", 10},
		{"constraint_pass_start", 16},
		{"during_constraint_pass", 17},
		{"before_publication", 22},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)")
			run(t, e, "INSERT INTO t VALUES(99,'sentinel',TRUE)")
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := testOperation(e, base)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: tc.check}
			op.ctx = ctx
			_, err = e.Execute(op, statement(t, "INSERT INTO t VALUES(1,'one'),(2,'two'),(3,'three')"), 0, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("staged cancellation: %v", err)
			}
			var sqlErr interface{ SQLState() string }
			if errors.As(err, &sqlErr) {
				t.Fatalf("context cancellation fabricated SQLSTATE: %v", err)
			}
			if got := ctx.checks.Load(); got != tc.check {
				t.Fatalf("cancellation check got %d, want %d", got, tc.check)
			}
			want := [][]driver.Value{{int64(99), "sentinel", true}}
			if got := tuples(run(t, e, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
				t.Fatalf("canceled staged INSERT changed complete rows: %v", got)
			}
			run(t, e, "INSERT INTO t VALUES(4)")
			want = [][]driver.Value{{int64(4), nil, nil}, {int64(99), "sentinel", true}}
			if got := tuples(run(t, e, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
				t.Fatalf("engine unusable after cancellation: %v", got)
			}
		})
	}
}

func TestCoercionsAndStatementAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t (i INTEGER NOT NULL, n BIGINT, s TEXT, b BOOLEAN)")
	run(t, e, "INSERT INTO t VALUES ($1,$2,$3,$4)", " +2147483647 ", "-9223372036854775808", true, []byte(" ye "))
	run(t, e, "INSERT INTO t (i,s,b) VALUES (0, 42, 'off')")
	want := [][]driver.Value{{int64(2147483647), int64(-9223372036854775808), "true", true}, {int64(0), nil, "42", false}}
	if got := tuples(run(t, e, "SELECT * FROM t ORDER BY i DESC")); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, c := range []struct{ sql, code string }{
		{"INSERT INTO t (i) VALUES (1),(2147483648)", "22003"}, {"INSERT INTO t (i) VALUES (1),('oops')", "22P02"}, {"INSERT INTO t (i) VALUES (1),(NULL)", "23502"},
		{"INSERT INTO t (s) VALUES ('omitted required')", "23502"}, {"INSERT INTO t (i) VALUES (TRUE)", "42804"}, {"INSERT INTO t (i,b) VALUES (1,1)", "42804"}, {"INSERT INTO t (i,b) VALUES (1,'o')", "22P02"}, {"INSERT INTO t (i,n) VALUES (1,'9223372036854775808')", "22003"},
	} {
		_, err := testExecute(e, context.Background(), statement(t, c.sql), nil)
		state(t, err, c.code)
		if got := tuples(run(t, e, "SELECT * FROM t ORDER BY i DESC")); !reflect.DeepEqual(got, want) {
			t.Fatalf("failed statement changed rows: %v", got)
		}
	}
	_, err := testExecute(e, context.Background(), statement(t, "INSERT INTO t (i) VALUES ($1)"), []driver.NamedValue{{Ordinal: 1, Value: 1.5}})
	state(t, err, "0A000")
	// WHERE integer literals use numeric comparison, not an int4 assignment cast.
	if r := run(t, e, "SELECT i FROM t WHERE i = 2147483648"); r.Count != 0 {
		t.Fatalf("unexpected matching count %d", r.Count)
	}
}
func TestNullOrderingAndSnapshotLifetime(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t (i INTEGER, s TEXT, b BOOLEAN)")
	run(t, e, "INSERT INTO t VALUES (2,'z',TRUE),(NULL,'A',FALSE),(1,NULL,NULL),(1,'a',TRUE)")
	asc := run(t, e, "SELECT i FROM t ORDER BY i")
	want := [][]driver.Value{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}
	if !reflect.DeepEqual(tuples(asc), want) {
		t.Fatalf("ASC rows: %v", tuples(asc))
	}
	if got := tuples(run(t, e, "SELECT i FROM t ORDER BY i DESC")); !reflect.DeepEqual(got, [][]driver.Value{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}) {
		t.Fatalf("DESC rows: %v", got)
	}
	if r := run(t, e, "SELECT * FROM t WHERE i = NULL"); r.Count != 0 {
		t.Fatal("NULL equality matched")
	}
	if got := tuples(run(t, e, "SELECT s FROM t ORDER BY s")); !reflect.DeepEqual(got, [][]driver.Value{{"A"}, {"a"}, {"z"}, {nil}}) {
		t.Fatalf("text order: %v", got)
	}
	if got := tuples(run(t, e, "SELECT b FROM t ORDER BY b")); !reflect.DeepEqual(got, [][]driver.Value{{false}, {true}, {true}, {nil}}) {
		t.Fatalf("bool order: %v", got)
	}
	run(t, e, "INSERT INTO t VALUES (0,'new',FALSE)")
	if !reflect.DeepEqual(tuples(asc), want) {
		t.Fatal("insert changed snapshot")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE replacement (x TEXT);")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tuples(asc), want) {
		t.Fatal("reset/restore changed snapshot")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestControlsRoundTripAndFailurePreservation(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE "a table" ("quote""col" TEXT, n BIGINT, b BOOLEAN, i INTEGER NOT NULL)`)
	run(t, e, `CREATE TABLE empty (x TEXT)`)
	run(t, e, `INSERT INTO "a table" VALUES ($1, $2, $3, $4)`, "line\nO'Brien\\tail", int64(-9223372036854775808), false, 0)
	run(t, e, `INSERT INTO "a table" VALUES ('',NULL,NULL,1)`)
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	copy := New()
	if err := copy.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := copy.Dump(context.Background(), &again); err != nil {
		t.Fatal(err)
	}
	if dump.String() != again.String() {
		t.Fatalf("unstable dump:\n%s\n%s", dump.String(), again.String())
	}
	want := [][]driver.Value{{"line\nO'Brien\\tail", int64(-9223372036854775808), false, int64(0)}, {"", nil, nil, int64(1)}}
	if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip rows: %v", got)
	}
	for _, script := range []string{"CREATE TABLE other (i INTEGER NOT NULL); INSERT INTO other VALUES (1),(NULL);", "CREATE TABLE other (i INTEGER); CREATE TABLE other (i TEXT);", "SELECT * FROM empty;", "CREATE TABLE other (i INTEGER); INSERT INTO other VALUES ($1);", "CREATE TABLE other (i INTEGER); nonsense"} {
		if err := copy.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatalf("restore accepted %q", script)
		}
		if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
			t.Fatalf("failed restore lost sentinel: %v", got)
		}
	}
	if err := copy.Restore(context.Background(), failingReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader error: %v", err)
	}
	if err := copy.Dump(context.Background(), failingWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []func() error{func() error { return copy.Reset(ctx) }, func() error { return copy.Restore(ctx, strings.NewReader("")) }, func() error {
		_, err := testExecute(copy, ctx, statement(t, `INSERT INTO "a table" VALUES ('lost',0,TRUE,3)`), nil)
		return err
	}} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error: %v", err)
		}
	}
	if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed controls changed sentinel: %v", got)
	}
	if err := copy.Restore(context.Background(), strings.NewReader(" -- empty\n /* script */ ")); err != nil {
		t.Fatal(err)
	}
	_, err := testDescribe(copy, statement(t, `SELECT * FROM "a table"`))
	state(t, err, "42P01")
}
func TestQualifiedRelationsUseOneCatalogIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE public.t (id INTEGER)")
	view := e.CatalogRead()
	e.mu.Lock()
	qualified, err := e.resolveReference(catalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "t"}}})
	unqualified, err2 := e.resolveReference(catalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}})
	e.mu.Unlock()
	if err != nil || err2 != nil || qualified != unqualified || qualified.Address.ObjectID == 0 {
		t.Fatalf("qualified=%#v unqualified=%#v errors=%v,%v", qualified, unqualified, err, err2)
	}
	rev := e.CatalogRead().Get(qualified.Address).Revision()
	r := run(t, e, "INSERT INTO public.t VALUES (1)")
	if r.Count != 1 || e.CatalogRead().Get(qualified.Address).Revision() != rev {
		t.Fatal("INSERT changed relation definition or count")
	}
	got := run(t, e, "SELECT id FROM t")
	if got.Count != 1 || got.Rows.Schema()[0].Usage.Type.DatabaseName() != "INT4" || !reflect.DeepEqual(tuples(got), [][]driver.Value{{int64(1)}}) {
		t.Fatalf("qualified result: %#v %v", got, tuples(got))
	}
	run(t, e, `CREATE TABLE public."Foo" (id INTEGER)`)
	run(t, e, "CREATE TABLE public.FOO (id INTEGER)")
	if _, err = testExecute(e, context.Background(), statement(t, `CREATE TABLE public."foo" (id INTEGER)`), nil); err == nil {
		t.Fatal("normalized duplicate was accepted")
	} else {
		state(t, err, "42P07")
	}
	run(t, e, `CREATE TABLE public."a.b" (id INTEGER)`)
	if r := run(t, e, `SELECT id FROM public."a.b"`); r.Count != 0 {
		t.Fatalf("dotted quoted relation rows %d", r.Count)
	}
	run(t, e, `CREATE TABLE "public.t" (id INTEGER)`)
	if r := run(t, e, `SELECT id FROM "public.t"`); r.Count != 0 {
		t.Fatalf("single dotted identifier resolved as qualification: %d", r.Count)
	}
	longA := strings.Repeat("a", 61) + "éx"
	longB := strings.Repeat("a", 61) + "éy"
	run(t, e, "CREATE TABLE public."+longA+" (id INTEGER)")
	if _, err = testExecute(e, context.Background(), statement(t, "CREATE TABLE public."+longB+" (id INTEGER)"), nil); err == nil {
		t.Fatal("UTF-8-truncated duplicate accepted")
	} else {
		state(t, err, "42P07")
	}
	if _, err = testExecute(e, context.Background(), statement(t, `SELECT id FROM "A.B".t`), nil); err == nil {
		t.Fatal("dotted schema lookup unexpectedly succeeded")
	} else {
		state(t, err, "42P01")
	}
	if _, err = parser.Parse(`SELECT * FROM "public.t"`); err != nil {
		t.Fatalf("one dotted identifier rejected: %v", err)
	}
	if _, err = parser.Parse(`SELECT * FROM a.b.c`); err == nil {
		t.Fatal("three-part relation name accepted")
	}
	if _, err = parser.Parse(`SELECT * FROM public.`); err == nil {
		t.Fatal("incomplete relation name accepted")
	}
}
