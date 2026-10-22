package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONBDescriptorPayloadAndNormalization(t *testing.T) {
	types := testTypes()
	d := types[JSONB]
	fresh := testTypes()
	ctx := context.Background()
	if d.ID() != 3802 || d.Name() != "jsonb" || d.SQLName() != "JSONB" || d.DatabaseName() != "JSONB" || d.AssociatedArray() != 3807 || types[3807].Element() != d || types[3807].DatabaseName() != "_JSONB" || d.Usage().Collation.ID != 0 || IsCharacter(JSONB) || !StoredScalarCastType(JSONB) {
		t.Fatal("incoherent JSONB descriptor", d)
	}
	if fresh[JSONB] == d || fresh[3807].Element() != fresh[JSONB] {
		t.Fatal("shared builtin generation")
	}
	cases := []struct{ input, want string }{
		{` {"z":1,"a":1,"a":2} `, `{"a": 2, "z": 1}`},
		{`{"a":1,"\u0061":2}`, `{"a": 2}`},
		{`{"é":1,"aaa":2,"雪":3,"z":4,"aa":5}`, `{"z": 4, "aa": 5, "é": 1, "aaa": 2, "雪": 3}`},
		{`[1,true,null,{"b":2,"a":1}]`, `[1, true, null, {"a": 1, "b": 2}]`},
		{`null`, `null`}, {`"null"`, `"null"`}, {`true`, `true`}, {`false`, `false`}, {`[]`, `[]`}, {`{}`, `{}`},
		{`-0`, `0`}, {`-0.00`, `0.00`}, {`1.00`, `1.00`}, {`1.230e-5`, `0.00001230`}, {`9223372036854775808`, `9223372036854775808`},
		{`"\uD800\uDC00"`, `"𐀀"`}, {`"\uDBFF\uDFFF"`, "\"\U0010ffff\""}, {`"\uD83D\uDE04"`, `"😄"`},
		{`"<>&\u2028"`, "\"<>&\u2028\""}, {`"\"\\\/\b\f\n\r\t"`, `"\"\\/\b\f\n\r\t"`}, {`"\\u0000"`, `"\\u0000"`},
	}
	for _, tc := range cases {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatalf("%q -> %q want %q err=%v", tc.input, out, tc.want, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+strings.ReplaceAll(tc.want, "'", "''")+"'" {
			t.Fatal(literal, err)
		}
		again, err := d.Input(ctx, tc.want, -1)
		if err != nil || !SameDatum(v, again) {
			t.Fatal("canonical representation identity", err)
		}
		reminted, err := v.Retag(fresh[JSONB])
		if err != nil || reminted.jsonb != v.jsonb {
			t.Fatal("retag copied payload", err)
		}
		if out, err := fresh[JSONB].Output(ctx, reminted); err != nil || out != tc.want {
			t.Fatal(out, err)
		}
		if _, err := v.Text(); err == nil {
			t.Fatal("JSONB admitted character accessor")
		}
	}
	for _, v := range []Value{{typ: d}, Null(d)} {
		if !v.null {
			if _, err := d.Output(ctx, v); err == nil {
				t.Fatal("missing payload output admitted")
			}
			if _, err := v.Retag(fresh[JSONB]); err == nil {
				t.Fatal("missing payload retag admitted")
			}
		}
	}
	domain, err := Domain(90000, "jb_domain", "jb_domain", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	base, err := d.Input(ctx, `{"x":1}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(base)
	if err != nil || wrapped.jsonb != base.jsonb {
		t.Fatal("domain copied payload", err)
	}
	array, err := ArrayValue(types[3807], []ArrayDimension{{Lower: 1, Length: 2}}, []Value{base, Null(d)})
	if err != nil {
		t.Fatal(err)
	}
	reminted, err := array.Retag(fresh[3807])
	if err != nil || reminted.array.elements[0].jsonb != base.jsonb || reminted.array.elements[0].typ != fresh[JSONB] {
		t.Fatal("array remint lost payload ownership", err)
	}
	if _, err := PlanCoercion(types[Unknown].Usage(), d.Usage(), Assignment); err == nil {
		t.Fatal("unknown stored source admitted")
	}
	if err := d.CheckModifierSupport(); err == nil {
		t.Fatal("JSONB typmod admitted")
	}
}

func TestJSONBNumericUnicodeDiagnosticsAndPrecedence(t *testing.T) {
	d := testTypes()[JSONB]
	ctx := context.Background()
	cases := []struct{ input, code, message, detail, context string }{
		{`"\u0000"`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: "\u0000...`},
		{`"\uD800"`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: "\uD800"`},
		{`"\uDC00"`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: "\uDC00...`},
		{`"\uD800\uD800"`, "22P02", "invalid input syntax for type json", "Unicode high surrogate must not follow a high surrogate.", `JSON data, line 1: "\uD800\uD800...`},
		{`"\uD800\u0041"`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: "\uD800\u0041...`},
		{`{"a":"\u0000","a":1}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`},
		{`["\u0000",]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: ["\u0000...`},
		{`1e131072.`, "22P02", "invalid input syntax for type json", `Token "." is invalid.`, `JSON data, line 1: 1e131072.`},
		{`1e131072x`, "22P02", "invalid input syntax for type json", `Token "1e131072x" is invalid.`, `JSON data, line 1: 1e131072x`},
		{`1e131072`, "22003", "value overflows numeric format", "", ""}, {`1e-16384`, "22003", "value overflows numeric format", "", ""},
		{`[1e131072,]`, "22003", "value overflows numeric format", "", ""}, {`[1e131072,"\u0000"]`, "22003", "value overflows numeric format", "", ""},
		{`{"a":1e131072,"a":1}`, "22003", "value overflows numeric format", "", ""}, {`0e100000000000000000000`, "22003", "value overflows numeric format", "", ""},
		{`0e-16384`, "22003", "value overflows numeric format", "", ""},
	}
	for _, tc := range cases {
		_, err := d.Input(ctx, tc.input, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Context: tc.context}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%q got %+v (%v) want %+v", tc.input, got, err, want)
		}
	}
	for _, tc := range []struct{ input, want string }{{"1e131071", "1" + strings.Repeat("0", 131071)}, {"1e-16383", "0." + strings.Repeat("0", 16382) + "1"}, {"0.1e131072", "1" + strings.Repeat("0", 131071)}, {"0e131072", "0"}} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatal("numeric boundary", tc.input, len(out), len(tc.want), err)
		}
	}
	for _, input := range []string{`+1`, `01`, `NaN`, `Infinity`, `0x10`, `1_000`, `1.`, `1e+`, `TRUE`, `"\u000g"`, `"\v"`} {
		if _, err := d.Input(ctx, input, -1); err == nil {
			t.Fatal("invalid JSONB admitted", input)
		}
	}
	depth := 10000
	input := strings.Repeat("[", depth) + "null" + strings.Repeat("]", depth)
	v, err := d.Input(ctx, input, -1)
	if err != nil {
		t.Fatal("deep iterative input", err)
	}
	out, err := d.Output(ctx, v)
	if err != nil || out != input {
		t.Fatal("deep output", err)
	}
	again, err := d.Input(ctx, input, -1)
	if err != nil || !SameDatum(v, again) {
		t.Fatal("deep representation comparison", err)
	}
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Hash(ctx, v); err != nil {
		t.Fatal("deep iterative hash", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, input, -1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if _, err := g.Hash(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestJSONBCanonicalizationCancellation(t *testing.T) {
	ctx := context.Background()
	wide := func() []jsonbEntry {
		fields := make([]jsonbEntry, 1024)
		for i := range fields {
			key := fmt.Sprintf("%04d", i%512)
			fields[i] = jsonbEntry{key: key, ordinal: i, value: jsonbNode{kind: jsonbString, text: fmt.Sprint(i)}}
		}
		return fields
	}
	t.Run("wide_object_finalization", func(t *testing.T) {
		fields := wide()
		// Unique ascending keys force the first heap sift to swap. The duplicate
		// fixture below starts with already ordered bottom heap nodes, so its
		// early comparison polls alone do not establish mutation progress.
		for i := range fields {
			fields[i].key = fmt.Sprintf("%04d", i)
		}
		parent := jsonbNode{kind: jsonbArray, items: []jsonbNode{{kind: jsonbString, text: "sentinel"}}}
		b := jsonbBuilder{frames: []jsonbFrame{{node: parent}, {node: jsonbNode{kind: jsonbObject, fields: fields}}}, root: jsonbNode{kind: jsonbString, text: "unpublished"}}
		cancel := &groupingCancelContext{Context: ctx, at: 32}
		if err := b.close(cancel); !errors.Is(err, context.Canceled) || cancel.calls != 32 {
			t.Fatal("in-progress sort", err, cancel.calls)
		}
		changed := false
		for i, field := range fields {
			changed = changed || field.ordinal != i
		}
		if !changed || len(b.frames) != 2 || len(b.frames[0].node.items) != 1 || b.root.text != "unpublished" {
			t.Fatal("sort did not progress or published an incomplete object")
		}
		// Independently check wide canonical order, duplicate-last selection and
		// cleared discarded references after a successful close.
		fields = wide()
		b = jsonbBuilder{frames: []jsonbFrame{{node: jsonbNode{kind: jsonbObject, fields: fields}}}}
		if err := b.close(ctx); err != nil {
			t.Fatal(err)
		}
		if len(b.frames) != 0 || len(b.root.fields) != 512 {
			t.Fatal("wide object cardinality")
		}
		for i, field := range b.root.fields {
			if field.key != fmt.Sprintf("%04d", i) || field.ordinal != i+512 || field.value.text != fmt.Sprint(i+512) {
				t.Fatal("canonical order or last-wins changed", i, field)
			}
		}
		for _, field := range fields[512:] {
			if field.key != "" || field.value.text != "" {
				t.Fatal("discarded reference retained")
			}
		}
	})
	t.Run("long_key_comparison", func(t *testing.T) {
		prefix := strings.Repeat("x", 8*jsonbWorkChunk)
		cancel := &groupingCancelContext{Context: ctx, at: 2}
		if _, err := compareJSONBKeys(cancel, prefix+"a", prefix+"b"); !errors.Is(err, context.Canceled) || cancel.calls != 2 {
			t.Fatal("long-prefix comparison", err, cancel.calls)
		}
		if c, err := compareJSONBKeys(ctx, prefix+"a", prefix+"b"); err != nil || c >= 0 {
			t.Fatal("long-prefix order", c, err)
		}
		// The sort itself must propagate a key-comparison cancellation.
		fields := []jsonbEntry{{key: prefix + "b"}, {key: prefix + "a"}}
		cancel = &groupingCancelContext{Context: ctx, at: 3}
		if err := sortJSONBFields(cancel, fields); !errors.Is(err, context.Canceled) || fields[0].key != prefix+"b" {
			t.Fatal("sort key-comparison propagation", err)
		}
	})
	t.Run("duplicate_compaction", func(t *testing.T) {
		fields := []jsonbEntry{{key: "a", ordinal: 3}, {key: "a", ordinal: 2}, {key: "b", ordinal: 1}, {key: "c", ordinal: 0}}
		cancel := &groupingCancelContext{Context: ctx, at: 6}
		got, err := compactJSONBFields(cancel, fields, false)
		if !errors.Is(err, context.Canceled) || got != nil || cancel.calls != 6 {
			t.Fatal("in-progress compaction", got, err, cancel.calls)
		}
		prefix := strings.Repeat("x", 8*jsonbWorkChunk)
		fields = []jsonbEntry{{key: prefix, ordinal: 1}, {key: prefix, ordinal: 0}}
		cancel = &groupingCancelContext{Context: ctx, at: 4}
		if got, err := compactJSONBFields(cancel, fields, false); !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal("long duplicate-key comparison", got, err)
		}
	})
	t.Run("discarded_tail_clearing", func(t *testing.T) {
		fields := make([]jsonbEntry, 32)
		for i := range fields {
			fields[i] = jsonbEntry{key: "a", ordinal: len(fields) - i, value: jsonbNode{kind: jsonbString, text: "discard"}}
		}
		// First entry takes one poll; each remaining entry takes its entry poll
		// and the short key comparator's two polls. Cancel on the second clear.
		cancel := &groupingCancelContext{Context: ctx, at: 1 + 3*(len(fields)-1) + 2}
		got, err := compactJSONBFields(cancel, fields, false)
		if !errors.Is(err, context.Canceled) || got != nil || fields[1].key != "" || fields[2].key != "a" {
			t.Fatal("in-progress clearing", got, err, cancel.calls)
		}
	})
}

func TestJSONBStringRenderingCancellation(t *testing.T) {
	ctx := context.Background()
	text := strings.Repeat("é\n\\\"", 4*jsonbWorkChunk)
	t.Run("bounded_scan_and_copy", func(t *testing.T) {
		var b strings.Builder
		cancel := &groupingCancelContext{Context: ctx, at: 2}
		err := writeJSONBString(cancel, &b, text)
		if !errors.Is(err, context.Canceled) || b.Len() <= 1 || b.Len() > 6*jsonbWorkChunk+1 || cancel.calls != 2 {
			t.Fatal("string did not stop after a bounded chunk", b.Len(), err, cancel.calls)
		}
		b.Reset()
		if err := writeJSONBString(ctx, &b, text); err != nil {
			t.Fatal(err)
		}
		want := `"` + strings.Repeat("é"+`\n\\\"`, 4*jsonbWorkChunk) + `"`
		if b.String() != want {
			t.Fatal("chunked escaping changed")
		}
	})
	for _, tc := range []struct {
		name string
		root jsonbNode
		at   int
	}{
		{"value", jsonbNode{kind: jsonbString, text: text}, 3},
		{"first_key", jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: text, value: jsonbNode{kind: jsonbNull}}}}, 3},
		{"later_key", jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: "a", value: jsonbNode{kind: jsonbNull}}, {key: text, value: jsonbNode{kind: jsonbNull}}}}, 6},
		{"last_string_poll", jsonbNode{kind: jsonbString, text: strings.Repeat("x", jsonbWorkChunk)}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cancel := &groupingCancelContext{Context: ctx, at: tc.at}
			out, err := jsonbOutput(cancel, &tc.root)
			if !errors.Is(err, context.Canceled) || out != "" || cancel.calls != tc.at {
				t.Fatal("partial rendered output published", out, err, cancel.calls)
			}
			if out, err := jsonbOutput(ctx, &tc.root); err != nil || out == "" {
				t.Fatal("cancellation mutated payload", err)
			}
		})
	}
}

func TestJSONBInputRenderingCancellationDoesNotPublish(t *testing.T) {
	d := testTypes()[JSONB]
	text := strings.Repeat("x", 8*jsonbWorkChunk)
	input := `"` + text + `"`
	// Count successful scanner+rendering polls, then cancel inside rendering,
	// after its first chunk. Calibration keeps scanner polling changes from
	// accidentally turning this into an entry/scanner cancellation witness.
	count := &groupingCancelContext{Context: context.Background(), at: int(^uint(0) >> 1)}
	v, err := d.Input(count, input, -1)
	if err != nil || v.text != input {
		t.Fatal("calibration input", err)
	}
	render := &groupingCancelContext{Context: context.Background(), at: int(^uint(0) >> 1)}
	if out, err := jsonbOutput(render, &v.jsonb.root); err != nil || out != input {
		t.Fatal("calibration rendering", err)
	}
	cancel := &groupingCancelContext{Context: context.Background(), at: count.calls - render.calls + 3}
	got, err := d.Input(cancel, input, -1)
	if !errors.Is(err, context.Canceled) || got != (Value{}) || cancel.calls != cancel.at {
		t.Fatal("input published incomplete payload", got, err, cancel.calls)
	}
	if again, err := d.Input(context.Background(), input, -1); err != nil || !SameDatum(v, again) {
		t.Fatal("canceled input changed subsequent construction", err)
	}
}

func TestJSONBComparisonHashAndDatumProviders(t *testing.T) {
	types := testTypes()
	d := types[JSONB]
	ctx := context.Background()
	cmp, err := BindComparison(d, d, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b string
		sign int
	}{
		{"1", "1.00", 0}, {`{"x":[1]}`, `{"x":[1.0]}`, 0}, {`{"a":1,"a":2}`, `{"a":2}`, 0},
		{"[]", "null", -1}, {"null", `"a"`, -1}, {`"z"`, "0", -1}, {"999", "false", -1}, {"true", "[0]", -1}, {"[0]", "{}", -1},
		{"[9]", "[0,0]", -1}, {`{"aa":1,"c":1}`, `{"b":1,"d":1}`, 1}, {"[[]]", "[null]", 1}, {`{"x":[]}`, `{"x":null}`, 1},
		{"9223372036854775808", "9223372036854775807", 1}, {"[1,2]", "[2,1]", -1},
	} {
		a, err := d.Input(ctx, tc.a, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := d.Input(ctx, tc.b, -1)
		if err != nil {
			t.Fatal(err)
		}
		n, err := cmp.Compare(ctx, a, b, InputEnvironment{})
		if err != nil || n < 0 != (tc.sign < 0) || n > 0 != (tc.sign > 0) {
			t.Fatal(tc, n, err)
		}
		equal, err := g.Equal(ctx, a, b)
		if err != nil || equal != (tc.sign == 0) {
			t.Fatal(equal, err)
		}
		if equal {
			ah, err := g.Hash(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			bh, err := g.Hash(ctx, b)
			if err != nil || ah != bh {
				t.Fatal("numeric-equivalent structural hashes", ah, bh, err)
			}
		}
	}
	a, _ := d.Input(ctx, "1", -1)
	b, _ := d.Input(ctx, "1.00", -1)
	if SameDatum(a, b) {
		t.Fatal("representation ignored numeric scale")
	}
	class, ok := DefaultIndexClass(d)
	if !ok || class.Name != "jsonb_ops" || class.OID() != 10088 {
		t.Fatal(class, ok)
	}
	if _, err := bindTestIndexOrdering(d.Usage(), class); err != nil {
		t.Fatal(err)
	}
	if _, err := BindOrdering(d.Usage()); err != nil {
		t.Fatal(err)
	}
	if _, err := cmp.Compare(ctx, Value{typ: d}, a, InputEnvironment{}); err == nil {
		t.Fatal("missing comparison payload admitted")
	}
	if _, err := g.Hash(ctx, Value{typ: d}); err == nil {
		t.Fatal("missing hash payload admitted")
	}
}

func TestJSONBIOCastContexts(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	d := types[JSONB]
	for _, context := range []CoercionContext{Implicit, Assignment, Explicit} {
		for _, pair := range [][2]ID{{JSON, JSONB}, {JSONB, JSON}, {Text, JSONB}, {JSONB, Text}, {Bool, JSONB}, {Int4, JSONB}, {JSONB, Bool}} {
			want := (pair[0] == JSON || pair[1] == JSON) && context != Implicit || (pair[0] == Text || pair[1] == Text) && context == Explicit
			_, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), context)
			if (err == nil) != want {
				t.Fatal(pair, context, err)
			}
		}
	}
	source := ` {"a":1,"a":2} `
	json, err := types[JSON].Input(ctx, source, -1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCoercion(types[JSON].Usage(), d.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	v, err := plan.Apply(ctx, json, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := d.Output(ctx, v); out != `{"a": 2}` {
		t.Fatal(out)
	}
	if retained, _ := json.JSONText(); retained != source {
		t.Fatal("JSON source changed", retained)
	}
	null, err := plan.Apply(ctx, Null(types[JSON]), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !null.IsNull() || null.Type() != d {
		t.Fatal(null, err)
	}
	invalid, _ := types[JSON].Input(ctx, `"\u0000"`, -1)
	if _, err := plan.Apply(ctx, invalid, InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("IO bypassed JSONB validation")
	}
}
