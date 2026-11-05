package xml

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func legacyTableRows(t *testing.T, ctx context.Context, document *string, paths string, width int) ([][]LegacyCell, []int, error) {
	t.Helper()
	table, err := NewLegacyXPathTable(paths, width)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]LegacyCell
	var notices []int
	err = table.Document(ctx, document, func(cells []LegacyCell) error {
		rows = append(rows, append([]LegacyCell{}, cells...))
		return nil
	}, func(kind int) { notices = append(notices, kind) })
	return rows, notices, err
}

func TestLegacyXPathTableExpansion(t *testing.T) {
	for _, tc := range []struct {
		name, doc, paths string
		width            int
		rows             [][]LegacyCell
		notices          []int
	}{
		{"zip", `<doc><a>10</a><a>20</a><b>7</b></doc>`, "/doc/a|/doc/b", 3, [][]LegacyCell{{{Text: "10"}, {Text: "7"}, {Null: true}}, {{Text: "20"}, {Null: true}, {Null: true}}}, nil},
		{"empty", `<doc><a/></doc>`, "/doc/a", 1, [][]LegacyCell{{{Text: ""}}}, nil},
		{"descendants", `<doc><a>x<b>y</b>z</a></doc>`, "/doc/a", 1, [][]LegacyCell{{{Text: "xyz"}}}, nil},
		{"unmatched", `<doc/>`, "/missing", 1, nil, nil},
		{"scalar_only", `<doc/>`, "string(.)", 1, nil, nil},
		{"string_repeat", `<doc><a>x</a><a>y</a></doc>`, "/doc/a|string('s')", 2, [][]LegacyCell{{{Text: "x"}, {Text: "s"}}, {{Text: "y"}, {Text: "s"}}}, nil},
		{"ignored_tail", `<doc><a>x</a></doc>`, "/doc/a|[", 1, [][]LegacyCell{{{Text: "x"}}}, nil},
		{"key_only", `<doc/>`, "[", 0, nil, nil},
		{"malformed_key_only", `<doc>`, "[", 0, [][]LegacyCell{{}}, nil},
		{"malformed", `<doc>`, "[", 1, [][]LegacyCell{{{Null: true}}}, nil},
		{"undefined_prefix", `<doc><a>x</a></doc>`, "/doc/a|/p:doc", 2, [][]LegacyCell{{{Text: "x"}, {Null: true}}}, nil},
		{"notices", `<doc><a>x</a><a>y</a></doc>`, "/doc/a|count(/doc/a)|boolean(/doc/a)", 3, [][]LegacyCell{{{Text: "x"}, {Text: "<unsupported/>"}, {Text: "<unsupported/>"}}, {{Text: "y"}, {Text: "<unsupported/>"}, {Text: "<unsupported/>"}}}, []int{3, 2, 3, 2, 3, 2}},
		{"scalar_notice", `<doc/>`, "count(/doc/a)", 1, nil, []int{3}},
		{"raw", `<?xml version="1.0" encoding="ISO-8859-1"?><doc><a>é</a></doc>`, "/doc/a", 1, [][]LegacyCell{{{Text: "Ã©"}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, notices, err := legacyTableRows(t, context.Background(), &tc.doc, tc.paths, tc.width)
			if err != nil || !reflect.DeepEqual(rows, tc.rows) || !reflect.DeepEqual(notices, tc.notices) {
				t.Fatal(rows, notices, err)
			}
		})
	}
	rows, notices, err := legacyTableRows(t, context.Background(), nil, "[", 2)
	if err != nil || !reflect.DeepEqual(rows, [][]LegacyCell{{{Null: true}, {Null: true}}}) || len(notices) != 0 {
		t.Fatal("NULL document", rows, notices, err)
	}
}

func TestLegacyXPathTableAccumulation(t *testing.T) {
	table, err := NewLegacyXPathTable("[", 1)
	if err != nil {
		t.Fatal(err)
	}
	var held []LegacyCell
	emit := func(cells []LegacyCell) error { held = append([]LegacyCell(nil), cells...); return nil }
	notice := func(kind int) { t.Fatal("unexpected notice", kind) }
	bad, good := "<doc>", "<doc/>"
	if err := table.Document(context.Background(), &bad, emit, notice); err != nil {
		t.Fatal(err)
	}
	err = table.Document(context.Background(), &good, emit, notice)
	var diagnostic *LegacyXPathError
	want := "line 1: Premature end of data in tag doc line 1\n<doc>\n     ^\nInvalid expression"
	if !errors.As(err, &diagnostic) || diagnostic.Detail != want || !reflect.DeepEqual(held, []LegacyCell{{Null: true}}) {
		t.Fatal("ordered invocation events or retained row", held, err)
	}
	_, _, err = legacyTableRows(t, context.Background(), &good, "[", 1)
	if !errors.As(err, &diagnostic) || diagnostic.Detail != "Invalid expression" {
		t.Fatal("events escaped invocation", err)
	}
}

func TestLegacyXPathTableLiteralAccumulation(t *testing.T) {
	table, err := NewLegacyXPathTable("concat('a|b','c')", 1)
	if err != nil {
		t.Fatal(err)
	}
	bad, good := "<doc>", "<doc/>"
	var rows [][]LegacyCell
	emit := func(cells []LegacyCell) error {
		rows = append(rows, append([]LegacyCell(nil), cells...))
		return nil
	}
	notice := func(kind int) { t.Fatal("unexpected notice", kind) }
	if err := table.Document(context.Background(), &bad, emit, notice); err != nil {
		t.Fatal(err)
	}
	err = table.Document(context.Background(), &good, emit, notice)
	var diagnostic *LegacyXPathError
	const detail = "line 1: Premature end of data in tag doc line 1\n<doc>\n     ^\nUnfinished literal"
	if !errors.As(err, &diagnostic) || diagnostic.Detail != detail || !reflect.DeepEqual(rows, [][]LegacyCell{{{Null: true}}}) {
		t.Fatal("literal compiler detail or ordered prior events lost", rows, err)
	}
}

func TestLegacyXPathTableCancellation(t *testing.T) {
	for _, tc := range []struct{ phase, doc, path string }{
		{"startRaw", "<doc/>", "/doc/a"},
		{"next", "<doc/>", strings.Repeat(" ", 20000) + "/doc/a"},
		{"axis", "<doc><a>x</a></doc>", "/doc/descendant::a"},
		{"sourceText", "<doc><a>A<b>" + strings.Repeat("x", 20000) + "</b>C</a></doc>", "/doc/a"},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: 1, cause: context.Canceled}
			rows, _, err := legacyTableRows(t, ctx, &tc.doc, tc.path, 1)
			if !ctx.admitted || !errors.Is(err, context.Canceled) || len(rows) != 0 {
				t.Fatal("actual-phase cancellation", tc.phase, ctx.admitted, len(rows), err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("context acquired SQLSTATE")
			}
		})
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc := "<doc><a>x</a></doc>"
			rows, notices, err := legacyTableRows(t, context.Background(), &doc, "/doc/a|count(/doc/a)", 2)
			if err != nil || len(rows) != 1 || !reflect.DeepEqual(notices, []int{3, 3}) {
				t.Error("concurrent invocation", rows, notices, err)
			}
		}()
	}
	wg.Wait()
}

func TestLegacyXPathTableEvaluationAccumulation(t *testing.T) {
	const evaluation = "Undefined namespace prefix"
	const malformed = "line 1: Premature end of data in tag doc line 1\n<doc>\n     ^"
	const namespace = "line 1: Namespace prefix p on r is not defined\n<p:r>8</p:r>\n    ^"
	for _, tc := range []struct {
		name, paths string
		width       int
		documents   []string
		prior       []string
		detail      string
		rows        [][]LegacyCell
	}{
		{"evaluation_then_compile", "/p:doc|[", 2, []string{"<doc/>"}, []string{evaluation}, evaluation + "\nInvalid expression", nil},
		{"repeated_evaluation_then_compile", "/p:doc|/q:doc|[", 3, []string{"<doc/>"}, []string{evaluation, evaluation}, evaluation + "\n" + evaluation + "\nInvalid expression", nil},
		{"distinct_evaluator_messages", "$missing|count('x')|[", 3, []string{"<doc/>"}, []string{"Undefined variable", "Invalid type"}, "Undefined variable\nInvalid type\nInvalid expression", nil},
		{"compile_before_evaluation", "[|/p:doc", 2, []string{"<doc/>"}, nil, "Invalid expression", nil},
		{"same_document_parser_order", "/p:r|[", 2, []string{"<p:r>8</p:r>"}, []string{namespace, evaluation}, namespace + "\n" + evaluation + "\nInvalid expression", nil},
		{"prior_document_parser_order", "/p:doc|[", 2, []string{"<doc>", "<doc/>"}, []string{malformed, evaluation}, malformed + "\n" + evaluation + "\nInvalid expression", [][]LegacyCell{{{Null: true}, {Null: true}}}},
		{"empty_variable_diagnostic", "$p:missing|[", 2, []string{"<doc/>"}, nil, "Invalid expression", nil},
		{"empty_function_diagnostic", "p:missing()|[", 2, []string{"<doc/>"}, nil, "Invalid expression", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table, err := NewLegacyXPathTable(tc.paths, tc.width)
			if err != nil {
				t.Fatal(err)
			}
			var rows [][]LegacyCell
			emit := func(cells []LegacyCell) error {
				rows = append(rows, append([]LegacyCell(nil), cells...))
				return nil
			}
			notice := func(kind int) { t.Fatal("evaluation became a notice", kind) }
			for i, document := range tc.documents {
				err = table.Document(context.Background(), &document, emit, notice)
				if i < len(tc.documents)-1 && err != nil {
					t.Fatal("earlier unusable document must retain its marker", err)
				}
			}
			var failure *LegacyXPathError
			if !errors.As(err, &failure) || failure.Detail != tc.detail || !reflect.DeepEqual(table.diagnostics, tc.prior) || !reflect.DeepEqual(rows, tc.rows) {
				t.Fatal("ordered genuine diagnostics or retained rows lost", table.diagnostics, rows, err)
			}
			fresh, err := NewLegacyXPathTable("[", 1)
			if err != nil {
				t.Fatal(err)
			}
			document := "<doc/>"
			err = fresh.Document(context.Background(), &document, emit, notice)
			if !errors.As(err, &failure) || failure.Detail != "Invalid expression" || len(fresh.diagnostics) != 0 || !reflect.DeepEqual(table.diagnostics, tc.prior) || !reflect.DeepEqual(rows, tc.rows) {
				t.Fatal("independent invocation inherited or changed prior state", table.diagnostics, rows, err)
			}
		})
	}
	t.Run("nonfatal_terminal_cross_document_ownership", func(t *testing.T) {
		table, err := NewLegacyXPathTable("/doc/a|/p:doc", 2)
		if err != nil {
			t.Fatal(err)
		}
		var rows [][]LegacyCell
		emit := func(cells []LegacyCell) error {
			rows = append(rows, append([]LegacyCell(nil), cells...))
			return nil
		}
		notice := func(kind int) { t.Fatal("evaluation became a notice", kind) }
		document := "<doc><a>10</a><a>20</a></doc>"
		if err := table.Document(context.Background(), &document, emit, notice); err != nil {
			t.Fatal("tolerated evaluation must not raise an error", err)
		}
		wantRows := [][]LegacyCell{{{Text: "10"}, {Null: true}}, {{Text: "20"}, {Null: true}}}
		wantPrior := []string{evaluation, evaluation, evaluation}
		if !reflect.DeepEqual(rows, wantRows) || !reflect.DeepEqual(table.diagnostics, wantPrior) {
			t.Fatal("driven or terminal evaluation was lost or deduplicated", rows, table.diagnostics)
		}
		heldRows, heldDiagnostics := rows[:2:2], table.diagnostics[:3:3]
		for _, document := range []string{"<doc>", "<p:r>8</p:r>"} {
			if err := table.Document(context.Background(), &document, emit, notice); err != nil {
				t.Fatal("cross-document retention changed nonfatal behavior", err)
			}
		}
		wantAll := []string{evaluation, evaluation, evaluation, malformed, namespace, evaluation}
		if !reflect.DeepEqual(table.diagnostics, wantAll) || !reflect.DeepEqual(heldDiagnostics, wantPrior) || !reflect.DeepEqual(heldRows, wantRows) || len(rows) != 3 || !reflect.DeepEqual(rows[2], []LegacyCell{{Null: true}, {Null: true}}) {
			t.Fatal("later parser/evaluator events overwrote prior owned entries or rows", rows, table.diagnostics)
		}
		fresh, err := NewLegacyXPathTable("/doc/a|/p:doc", 2)
		if err != nil || len(fresh.diagnostics) != 0 {
			t.Fatal("new operation inherited diagnostics", err)
		}
		var freshRows [][]LegacyCell
		if err := fresh.Document(context.Background(), &document, func(cells []LegacyCell) error {
			freshRows = append(freshRows, append([]LegacyCell(nil), cells...))
			return nil
		}, notice); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(freshRows, wantRows) || !reflect.DeepEqual(fresh.diagnostics, wantPrior) || &fresh.diagnostics[0] == &table.diagnostics[0] || !reflect.DeepEqual(table.diagnostics, wantAll) || !reflect.DeepEqual(heldRows, wantRows) {
			t.Fatal("independent invocation shared diagnostic backing or mutated retained output", freshRows, fresh.diagnostics)
		}
	})
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run("evaluation_cancellation_"+cause.Error(), func(t *testing.T) {
			table, err := NewLegacyXPathTable("/p:doc|[", 2)
			if err != nil {
				t.Fatal(err)
			}
			emitted := 0
			emit := func([]LegacyCell) error { emitted++; return nil }
			notice := func(kind int) { t.Fatal("evaluation became a notice", kind) }
			bad, good := "<doc>", "<doc/>"
			if err := table.Document(context.Background(), &bad, emit, notice); err != nil {
				t.Fatal(err)
			}
			// Admit real evaluation, then cancel after it returns its genuine
			// XPath failure, before that diagnostic can be retained or cleared.
			ctx := &phaseContext{Context: context.Background(), phase: "eval", final: true, cause: cause}
			err = table.Document(ctx, &good, emit, notice)
			var failure *LegacyXPathError
			var xpath *XPathError
			if !ctx.admitted || !errors.Is(err, cause) || errors.As(err, &failure) || errors.As(err, &xpath) || emitted != 1 || !reflect.DeepEqual(table.diagnostics, []string{malformed}) {
				t.Fatal("ordinary cancellation lost priority or changed prior owned state", ctx.admitted, emitted, table.diagnostics, err)
			}
		})
	}
}
