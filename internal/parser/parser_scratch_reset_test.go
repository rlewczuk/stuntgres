package parser

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

// Seed every fixed cell, including cells a short parse never uses. The test
// observes a known receiver, never a presumed pool hit or object identity reuse.
func seededParseScratch() *sqlParserImpl {
	cell := sqlSymType{yys: 91, integer: 37, desc: true,
		tok:   lexeme{text: "retained token", raw: "raw token", pos: Position{5, 6}},
		names: []string{"retained name"}, columns: []Column{{Name: "retained column"}},
		rows: [][]Expr{{LiteralExpr{Value: Value{Integer: 41}}}},
		expr: ParameterExpr{Number: 9}, stmt: &Select{},
		col: &Column{Name: "carrier"}, windowSpec: &WindowSpec{}, alterAction: &AlterTableAction{},
	}
	p := &sqlParserImpl{lval: cell, char: 123}
	for i := range p.stack {
		p.stack[i] = cell
	}
	return p
}

func requireScratchZero(t *testing.T, p *sqlParserImpl) {
	t.Helper()
	if !reflect.ValueOf(*p).IsZero() {
		t.Fatal("generated receiver not fully typed-zero after Parse")
	}
}

func requireScratchDiagnostic(t *testing.T, err error, message, position, diagnosticContext string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: position, Context: diagnosticContext}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic %#v (%v), want %#v", got, err, want)
	}
}

func requireScratchSeven(t *testing.T) {
	t.Helper()
	statements, err := ParseWithOptions("SELECT 7", Options{Context: context.Background(), StandardConformingStrings: true})
	if err != nil || len(statements) != 1 || statements[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 7 {
		t.Fatal(statements, err)
	}
	typ, err := ParseTypeName(context.Background(), "integer", nil)
	if err != nil || typ.Name != "int4" || !typ.System {
		t.Fatal(typ, err)
	}
}

func TestParseScratchFullReset(t *testing.T) {
	for _, input := range []string{"SELECT 7", "INSERT INTO t VALUES(1),(2)"} {
		t.Run(input, func(t *testing.T) {
			p := seededParseScratch()
			l := &lexer{input: input}
			if result := parseWithScratch(p, l); result != 0 || l.err != nil || len(l.statements) != 1 {
				t.Fatal(result, l.err, l.statements)
			}
			requireScratchZero(t, p)
			if input == "SELECT 7" {
				if l.statements[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 7 {
					t.Fatal(l.statements)
				}
			} else {
				rows := l.statements[0].(*Insert).Rows
				if len(rows) != 2 || len(rows[0]) != 1 || len(rows[1]) != 1 || rows[0][0].(LiteralExpr).Value.Integer != 1 || rows[1][0].(LiteralExpr).Value.Integer != 2 {
					t.Fatal(rows)
				}
			}
		})
	}
	t.Run("syntax", func(t *testing.T) {
		p := seededParseScratch()
		l := &lexer{input: "SELECT ("}
		if parseWithScratch(p, l) == 0 {
			t.Fatal("syntax accepted")
		}
		requireScratchZero(t, p)
		requireScratchDiagnostic(t, l.err, "syntax error at end of input", "9", "")
		statements, err := Parse(l.input)
		if statements != nil {
			t.Fatal("published failed SQL", statements)
		}
		requireScratchDiagnostic(t, err, "syntax error at end of input", "9", "")
	})
	for _, mode := range []string{"cancel", "reject", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("original callback failure")
			callback := func(string, Position) error {
				switch mode {
				case "cancel":
					cancel()
				case "reject":
					return sentinel
				case "panic":
					panic(sentinel)
				}
				return nil
			}
			p := seededParseScratch()
			l := &lexer{input: "-- callback\nSELECT 7", options: Options{Context: ctx, LineComment: callback}}
			if mode == "panic" {
				func() {
					defer func() {
						if got := recover(); got != sentinel {
							t.Fatalf("panic identity changed: %v", got)
						}
					}()
					parseWithScratch(p, l)
					t.Fatal("panic suppressed")
				}()
			} else {
				parseWithScratch(p, l)
				want := error(sentinel)
				if mode == "cancel" {
					want = context.Canceled
				}
				var diagnostic *sqlstate.Error
				if l.err != want || errors.As(l.err, &diagnostic) {
					t.Fatal(l.err)
				}
				// Repeat at the public package boundary with a fresh live context.
				publicCtx, publicCancel := context.WithCancel(context.Background())
				defer publicCancel()
				statements, err := ParseWithOptions(l.input, Options{Context: publicCtx, LineComment: func(string, Position) error {
					if mode == "cancel" {
						publicCancel()
						return nil
					}
					return sentinel
				}})
				if statements != nil || err != want || errors.As(err, &diagnostic) {
					t.Fatal(statements, err)
				}
			}
			requireScratchZero(t, p)
			requireScratchSeven(t)
		})
	}
	for _, input := range []string{"integer", "integer trailing"} {
		t.Run("type/"+input, func(t *testing.T) {
			p := seededParseScratch()
			l := &lexer{input: input, options: Options{Context: context.Background(), StandardConformingStrings: true}, typeInputEntry: true, typeInputMode: true}
			result := parseWithScratch(p, l)
			requireScratchZero(t, p)
			if input == "integer" {
				if result != 0 || l.err != nil || l.typeInputResult.Name != "int4" || !l.typeInputResult.System {
					t.Fatal(result, l.err, l.typeInputResult)
				}
			} else {
				if result == 0 {
					t.Fatal("trailing type accepted")
				}
				requireScratchDiagnostic(t, l.err, `syntax error at or near "trailing"`, "9", "")
				typ, err := ParseTypeName(context.Background(), input, nil)
				if !reflect.ValueOf(typ).IsZero() {
					t.Fatal(typ)
				}
				requireScratchDiagnostic(t, err, `syntax error at or near "trailing"`, "9", `invalid type name "integer trailing"`)
			}
		})
	}
	t.Run("type cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		input := `"` + strings.Repeat("a", 64) + `"`
		p := seededParseScratch()
		notices := 0
		l := &lexer{input: input, options: Options{Context: ctx, StandardConformingStrings: true}, typeInputEntry: true, typeInputMode: true, typeInputNotice: func(sqlstate.Error) { notices++; cancel() }}
		parseWithScratch(p, l)
		requireScratchZero(t, p)
		if notices != 1 || l.err != context.Canceled {
			t.Fatal(notices, l.err)
		}
		publicCtx, publicCancel := context.WithCancel(context.Background())
		defer publicCancel()
		typ, err := ParseTypeName(publicCtx, input, func(sqlstate.Error) { publicCancel() })
		var diagnostic *sqlstate.Error
		if !reflect.ValueOf(typ).IsZero() || err != context.Canceled || errors.As(err, &diagnostic) {
			t.Fatal(typ, err)
		}
		requireScratchSeven(t)
	})
	for _, typeMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("notice panic/type=%v", typeMode), func(t *testing.T) {
			sentinel := &struct{ name string }{"notice panic"}
			input := strings.Repeat("a", 64)
			l := &lexer{input: "SELECT " + input, options: Options{Notice: func(sqlstate.Error) { panic(sentinel) }}}
			if typeMode {
				l = &lexer{input: input, typeInputEntry: true, typeInputMode: true, typeInputNotice: func(sqlstate.Error) { panic(sentinel) }}
			}
			p := seededParseScratch()
			func() {
				defer func() {
					if got := recover(); got != sentinel {
						t.Fatalf("notice panic changed: %v", got)
					}
				}()
				parseWithScratch(p, l)
				t.Fatal("notice panic suppressed")
			}()
			requireScratchZero(t, p)
			requireScratchSeven(t)
		})
	}
}

func TestParseScratchExclusiveLoans(t *testing.T) {
	first := parseScratchPool.Get().(*sqlParserImpl)
	defer parseScratchPool.Put(first)
	defer func() { *first = sqlParserImpl{} }()
	second := parseScratchPool.Get().(*sqlParserImpl)
	defer parseScratchPool.Put(second)
	defer func() { *second = sqlParserImpl{} }()
	if first == second {
		t.Fatal("simultaneous checkouts share receiver")
	}
	for _, p := range []*sqlParserImpl{first, second} {
		l := &lexer{input: "SELECT 7"}
		if parseWithScratch(p, l) != 0 || l.err != nil || l.statements[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 7 {
			t.Fatal(l.err, l.statements)
		}
		requireScratchZero(t, p)
	}
}

func TestParseScratchNoticeOwnership(t *testing.T) {
	name, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
	sqlWant := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier \"%s\" will be truncated to \"%s\"", name, clipped)}
	typeWant := sqlWant
	typeWant.Context = fmt.Sprintf("invalid type name \"%s\"", name)
	var sqlNotices, typeNotices []sqlstate.Error
	var innerSQL []Statement
	var innerType TypeName
	var innerErr error
	outerSQL, err := ParseWithOptions("SELECT "+name, Options{StandardConformingStrings: true, Notice: func(n sqlstate.Error) {
		sqlNotices = append(sqlNotices, n)
		innerSQL, innerErr = ParseWithOptions(`INSERT INTO nested VALUES('x\ny')`, Options{StandardConformingStrings: false})
		if innerErr == nil {
			innerType, innerErr = ParseTypeName(context.Background(), "numeric(7,2)[]", nil)
		}
	}})
	if err != nil || innerErr != nil || len(outerSQL) != 1 || len(innerSQL) != 1 {
		t.Fatal(outerSQL, innerSQL, err, innerErr)
	}
	if outerSQL[0].(*Select).Targets[0].(ColumnExpr).Name.Parts[0].Text != clipped || innerSQL[0].(*Insert).Rows[0][0].(LiteralExpr).Value.Text != "x\ny" || innerType.Name != "numeric" || len(innerType.Modifiers) != 2 || *innerType.Modifiers[0].Integer != 7 || *innerType.Modifiers[1].Integer != 2 || len(innerType.ArrayBounds) != 1 {
		t.Fatal(outerSQL, innerSQL, innerType)
	}
	outerType, err := ParseTypeName(context.Background(), name, func(n sqlstate.Error) {
		typeNotices = append(typeNotices, n)
		innerSQL, innerErr = ParseWithOptions(`INSERT INTO nested VALUES('x\ny')`, Options{StandardConformingStrings: true})
		if innerErr == nil {
			innerType, innerErr = ParseTypeName(context.Background(), `"Odd.Schema"."Odd.Type"[]`, nil)
		}
	})
	if err != nil || innerErr != nil || outerType.Name != clipped || outerType.System || innerSQL[0].(*Insert).Rows[0][0].(LiteralExpr).Value.Text != `x\ny` || innerType.Name != "Odd.Type" || !innerType.Quoted || len(innerType.Parts) != 2 || innerType.Parts[0].Text != "Odd.Schema" || len(innerType.ArrayBounds) != 1 {
		t.Fatal(outerType, innerSQL, innerType, err, innerErr)
	}
	// Later notices without callbacks must not reach either previous collector.
	if _, err := Parse("SELECT " + name); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTypeName(context.Background(), name, nil); err != nil {
		t.Fatal(err)
	}
	if statements, err := Parse("SELECT ("); statements != nil || err == nil {
		t.Fatal(statements, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if statements, err := ParseWithOptions("-- cancel\nSELECT 7", Options{Context: ctx, LineComment: func(string, Position) error { cancel(); return nil }}); statements != nil || err != context.Canceled {
		t.Fatal(statements, err)
	}
	requireScratchSeven(t)
	if len(sqlNotices) != 1 || sqlNotices[0] != sqlWant || len(typeNotices) != 1 || typeNotices[0] != typeWant {
		t.Fatal(sqlNotices, typeNotices)
	}
	if outerSQL[0].(*Select).Targets[0].(ColumnExpr).Name.Parts[0].Text != clipped || outerType.Name != clipped {
		t.Fatal("notice result changed")
	}
}

func TestParseScratchConcurrentCancellation(t *testing.T) {
	var workers sync.WaitGroup
	for id := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			name := fmt.Sprintf("worker_%d", id)
			standard := id%2 == 0
			want := "a\nb"
			if standard {
				want = `a\nb`
			}
			var retained *Insert
			var retainedType TypeName
			for range 12 {
				statements, err := ParseWithOptions("INSERT INTO t VALUES('"+name+"','a\\nb'),('other',E'c\\n')", Options{StandardConformingStrings: standard})
				if err != nil || len(statements) != 1 {
					t.Errorf("worker %d SQL: %v", id, err)
					return
				}
				insert := statements[0].(*Insert)
				if len(insert.Rows) != 2 || insert.Rows[0][0].(LiteralExpr).Value.Text != name || insert.Rows[0][1].(LiteralExpr).Value.Text != want || insert.Rows[1][1].(LiteralExpr).Value.Text != "c\n" {
					t.Errorf("worker %d mixed SQL", id)
					return
				}
				typ, err := ParseTypeName(context.Background(), fmt.Sprintf("numeric(%d,2)[]", id+3), nil)
				if err != nil || typ.Name != "numeric" || len(typ.Modifiers) != 2 || *typ.Modifiers[0].Integer != int32(id+3) || *typ.Modifiers[1].Integer != 2 || len(typ.ArrayBounds) != 1 {
					t.Errorf("worker %d type: %v", id, err)
					return
				}
				qualified, err := ParseTypeName(context.Background(), `"Odd.Schema"."`+name+`"[]`, nil)
				if err != nil || qualified.Name != name || !qualified.Quoted || len(qualified.Parts) != 2 || qualified.Parts[0].Text != "Odd.Schema" || len(qualified.ArrayBounds) != 1 {
					t.Errorf("worker %d qualified type: %v", id, err)
					return
				}
				ctx, cancel := context.WithCancel(context.Background())
				failed, err := ParseWithOptions("-- cancel\nSELECT 7", Options{Context: ctx, LineComment: func(string, Position) error { cancel(); return nil }})
				cancel()
				var diagnostic *sqlstate.Error
				if failed != nil || err != context.Canceled || errors.As(err, &diagnostic) {
					t.Errorf("worker %d SQL cancellation: %v", id, err)
					return
				}
				ctx, cancel = context.WithCancel(context.Background())
				failedType, err := ParseTypeName(ctx, `"`+strings.Repeat("a", 64)+`"`, func(sqlstate.Error) { cancel() })
				cancel()
				if !reflect.ValueOf(failedType).IsZero() || err != context.Canceled || errors.As(err, &diagnostic) {
					t.Errorf("worker %d type cancellation: %v", id, err)
					return
				}
				fresh, err := ParseWithOptions("SELECT 7", Options{Context: context.Background()})
				if err != nil || len(fresh) != 1 || fresh[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 7 {
					t.Errorf("worker %d recovery: %v", id, err)
					return
				}
				freshType, err := ParseTypeName(context.Background(), "integer", nil)
				if err != nil || freshType.Name != "int4" || !freshType.System {
					t.Errorf("worker %d type recovery: %v", id, err)
					return
				}
				if qualified.Name != name || !qualified.Quoted || len(qualified.Parts) != 2 || qualified.Parts[0].Text != "Odd.Schema" || qualified.Parts[1].Text != name || len(qualified.ArrayBounds) != 1 {
					t.Errorf("worker %d retained qualification changed", id)
					return
				}
				if retained != nil && (retained.Rows[0][0].(LiteralExpr).Value.Text != name || retained.Rows[0][1].(LiteralExpr).Value.Text != want) {
					t.Errorf("worker %d retained SQL changed", id)
					return
				}
				if retainedType.Name != "" && (*retainedType.Modifiers[0].Integer != int32(id+3) || *retainedType.Modifiers[1].Integer != 2 || len(retainedType.ArrayBounds) != 1) {
					t.Errorf("worker %d retained type changed", id)
					return
				}
				retained, retainedType = insert, typ
			}
		}()
	}
	workers.Wait()
}

func TestParseScratchRetainedParametersAndPositions(t *testing.T) {
	statements, err := Parse("/*é*/ INSERT INTO t VALUES($2,$1),($2,$3)")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	insert := statements[0].(*Insert)
	numeric, err := ParseTypeName(context.Background(), "/*é*/ numeric(7,2)[]", nil)
	if err != nil {
		t.Fatal(err)
	}
	qualified, err := ParseTypeName(context.Background(), `"Odd.Schema"."Odd.Type"[]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		if _, err := Parse(fmt.Sprintf("INSERT INTO churn VALUES(%d,'different'),(%d,'next')", i, i+1)); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseTypeName(context.Background(), fmt.Sprintf("varchar(%d)[]", i+1), nil); err != nil {
			t.Fatal(err)
		}
		failed, err := Parse("INSERT INTO discarded VALUES(1); SELECT (")
		if failed != nil {
			t.Fatal("failed prefix published", failed)
		}
		requireScratchDiagnostic(t, err, "syntax error at end of input", "42", "")
		failedType, err := ParseTypeName(context.Background(), "integer trailing", nil)
		if !reflect.ValueOf(failedType).IsZero() {
			t.Fatal(failedType)
		}
		requireScratchDiagnostic(t, err, `syntax error at or near "trailing"`, "9", `invalid type name "integer trailing"`)
		ctx, cancel := context.WithCancel(context.Background())
		failed, err = ParseWithOptions("-- cancel\nSELECT 7", Options{Context: ctx, LineComment: func(string, Position) error { cancel(); return nil }})
		cancel()
		if failed != nil || err != context.Canceled {
			t.Fatal(failed, err)
		}
		ctx, cancel = context.WithCancel(context.Background())
		failedType, err = ParseTypeName(ctx, `"`+strings.Repeat("a", 64)+`"`, func(sqlstate.Error) { cancel() })
		cancel()
		if !reflect.ValueOf(failedType).IsZero() || err != context.Canceled {
			t.Fatal(failedType, err)
		}
	}
	if len(insert.Rows) != 2 || len(insert.Rows[0]) != 2 || len(insert.Rows[1]) != 2 {
		t.Fatal(insert.Rows)
	}
	wants := []ParameterExpr{{Number: 2, Pos: Position{28, 28}}, {Number: 1, Pos: Position{31, 31}}, {Number: 2, Pos: Position{36, 36}}, {Number: 3, Pos: Position{39, 39}}}
	for i, want := range wants {
		if got := insert.Rows[i/2][i%2].(ParameterExpr); got != want {
			t.Fatalf("parameter %d: %#v want %#v", i, got, want)
		}
	}
	if len(insert.Name.Parts) != 1 || insert.Name.Parts[0] != (Identifier{Text: "t", Pos: Position{19, 19}}) || insert.Pos != (Position{7, 7}) {
		t.Fatal(insert.Name, insert.Pos)
	}
	if numeric.Name != "numeric" || !numeric.System || numeric.Pos != (Position{7, 7}) || len(numeric.Modifiers) != 2 || *numeric.Modifiers[0].Integer != 7 || *numeric.Modifiers[1].Integer != 2 || numeric.Modifiers[0].Pos != (Position{15, 15}) || numeric.Modifiers[1].Pos != (Position{17, 17}) || len(numeric.ArrayBounds) != 1 {
		t.Fatal(numeric)
	}
	if qualified.Name != "Odd.Type" || !qualified.Quoted || qualified.System || qualified.Pos != (Position{0, 1}) || qualified.Schema == nil || *qualified.Schema != (Identifier{Text: "Odd.Schema", Pos: Position{0, 1}}) || len(qualified.Parts) != 2 || qualified.Parts[0] != (Identifier{Text: "Odd.Schema", Pos: Position{0, 1}}) || qualified.Parts[1] != (Identifier{Text: "Odd.Type", Pos: Position{13, 14}}) || len(qualified.ArrayBounds) != 1 {
		t.Fatal(qualified)
	}
}

func TestParseScratchDeepReset(t *testing.T) {
	retained, err := Parse("INSERT INTO retained VALUES('first'),('second')")
	if err != nil || len(retained) != 1 {
		t.Fatal(retained, err)
	}
	typ, err := ParseTypeName(context.Background(), "numeric(7,2)[]", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{true, false} {
		input := "SELECT " + strings.Repeat("(", 64) + "1" + strings.Repeat(")", 64)
		if !valid {
			input = "SELECT " + strings.Repeat("(", 64)
		}
		p := seededParseScratch()
		l := &lexer{input: input}
		result := parseWithScratch(p, l)
		requireScratchZero(t, p)
		if valid {
			if result != 0 || l.err != nil || len(l.statements) != 1 || l.statements[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 1 {
				t.Fatal(result, l.err, l.statements)
			}
		} else {
			if result == 0 {
				t.Fatal("malformed deep input accepted")
			}
			requireScratchDiagnostic(t, l.err, "syntax error at end of input", "72", "")
			failed, err := Parse(input)
			if failed != nil {
				t.Fatal(failed)
			}
			requireScratchDiagnostic(t, err, "syntax error at end of input", "72", "")
		}
	}
	rows := retained[0].(*Insert).Rows
	if len(rows) != 2 || rows[0][0].(LiteralExpr).Value.Text != "first" || rows[1][0].(LiteralExpr).Value.Text != "second" || typ.Name != "numeric" || len(typ.Modifiers) != 2 || *typ.Modifiers[0].Integer != 7 || *typ.Modifiers[1].Integer != 2 || len(typ.ArrayBounds) != 1 {
		t.Fatal(rows, typ)
	}
}
