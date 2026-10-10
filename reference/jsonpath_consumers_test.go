package reference_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func jsonpathReferenceBindFailure(t *testing.T, p *referencePair, executor referenceExecutor, query string, want referenceDiagnostic, slot string, args ...any) {
	t.Helper()
	result, err := executor.ExecContext(p.ctx, query, args...)
	if err == nil {
		t.Fatalf("invalid bind succeeded: %s", query)
	}
	if result != nil {
		t.Fatal("invalid bind published result")
	}
	got := referenceExtractDiagnostic(t, err)
	want.Context = "unnamed portal parameter $" + slot + " = '...'"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bind diagnostic got %#v want %#v", got, want)
	}
}

func jsonpathReferenceHeld(t *testing.T, p *referencePair, b referenceBackend, query string) referenceHeldStatement {
	t.Helper()
	stmt, err := b.db.PrepareContext(p.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
}

func TestReferenceJSONPathConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, jsonpathReferenceStep(t, b, s)) }
		run(referenceExec("CREATE TABLE jp_frames(id INT,p jsonpath,q jsonpath)", 0))
		q := "INSERT INTO jp_frames VALUES($1,$2,$2)"
		held := jsonpathReferenceHeld(t, p, b, q)
		buf := []byte(`$.a.v`)
		var nilBytes []byte
		cases := []struct{ arg, want any }{
			{`$`, `$`}, {buf, `$."a"."v"`}, {jsonReferenceValuer(`$a.b`), `$"a"."b"`},
			{nil, nil}, {nilBytes, nil}, {"null", "null"}, {true, "true"}, {int64(42), "42"}, {1.25, "1.25"},
		}
		var want [][]any
		for i, tc := range cases {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, i, tc.arg))
			want = append(want, []any{int64(i), tc.want, tc.want})
			if i == 1 {
				buf[0] = 'x'
			}
		}
		run(referenceSelect("SELECT id,p,q FROM jp_frames ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, want))
		bad := jsonpathReferenceDiagnostic(jsonpathReferenceFailures[1])
		jsonpathReferenceBindFailure(t, p, held, q, bad, "2", 99, "last")
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, 99, `strict $`))
		update := "UPDATE jp_frames SET p=$1 WHERE id=$2"
		uh := jsonpathReferenceHeld(t, p, b, update)
		jsonpathReferenceBindFailure(t, p, uh, update, bad, "1", "last", 99)
		run(referenceSelect("SELECT p,q FROM jp_frames WHERE id=99", []string{"p", "q"}, []string{"JSONPATH", "JSONPATH"}, [][]any{{"strict $", "strict $"}}))
		referenceRunStep(t, p.ctx, uh, b.recorder, referenceExec(update, 1, "$a.b", 99))
		multi := "INSERT INTO jp_frames VALUES(100,$1,$1),(101,$2,$2)"
		mh := jsonpathReferenceHeld(t, p, b, multi)
		jsonpathReferenceBindFailure(t, p, mh, multi, bad, "2", "$", "last")
		run(referenceSelect("SELECT id FROM jp_frames WHERE id >= 100 ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
		referenceRunStep(t, p.ctx, mh, b.recorder, referenceExec(multi, 2, "$", "null"))
		run(referenceSelect("SELECT id,p,q FROM jp_frames WHERE id >= 99 ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(99), `$"a"."b"`, "strict $"}, {int64(100), "$", "$"}, {int64(101), "null", "null"}}))
		cast := "SELECT CAST($1 AS jsonpath) AS v"
		ch := jsonpathReferenceHeld(t, p, b, cast)
		for _, tc := range jsonpathReferenceOutputs[:4] {
			referenceRunStep(t, p.ctx, ch, b.recorder, jsonpathReferenceStep(t, b, referenceSelect(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{tc.output}}, tc.input)))
		}
		// Encoding is a protocol/character-input boundary, not path grammar.
		for _, tc := range []struct {
			arg     any
			message string
		}{
			{string([]byte{0xff}), `invalid byte sequence for encoding "UTF8": 0xff`},
			{[]byte{'$', 0}, `invalid byte sequence for encoding "UTF8": 0x00`},
		} {
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: tc.message, Context: "unnamed portal parameter $1"}
			s := referenceErrorDiagnostic(cast, want, tc.arg)
			s.selectRows, s.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, ch, b.recorder, s)
		}
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			for _, sql := range []string{"BEGIN", "SAVEPOINT jp_before"} {
				if _, err := conn.ExecContext(p.ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			jsonpathReferenceBindFailure(t, p, conn, "INSERT INTO jp_frames VALUES(102,$1,$1)", bad, "1", "last")
			for _, sql := range []string{"ROLLBACK TO SAVEPOINT jp_before", "INSERT INTO jp_frames VALUES(102,'$','$')", "COMMIT"} {
				if _, err := conn.ExecContext(p.ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
		}()
		run(referenceSelect("SELECT p FROM jp_frames WHERE id=102", []string{"p"}, []string{"JSONPATH"}, [][]any{{"$"}}))
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if _, err := b.db.ExecContext(ctx, "INSERT INTO jp_frames VALUES(103,$1,$1)", "$"); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled bind: %v", err)
		}
		run(referenceSelect("SELECT id FROM jp_frames WHERE id=103", []string{"id"}, []string{"INT4"}, nil))
	}
}

func TestReferenceJSONPathFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, jsonpathReferenceStep(t, b, s)) }
		run(referenceExec("CREATE TABLE jp_smoke(id INT,p jsonpath,q jsonpath)", 0))
		inquiry := func(s referenceStep) { run(jsonpathReferenceInquiry(b, s)) }
		inquiry(referenceSelect("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"jsonpath", int64(4072), int64(4073), int64(0)}}))
		inquiry(referenceSelect("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='_jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"_jsonpath", int64(4073), int64(0), int64(4072)}}))
		insert := "INSERT INTO jp_smoke VALUES($1,$2,$2)"
		ih := jsonpathReferenceHeld(t, p, b, insert)
		referenceRunStep(t, p.ctx, ih, b.recorder, referenceExec(insert, 1, 1, `$.g ? (@.a == 1)`))
		run(referenceSelect("SELECT id,p,q FROM jp_smoke ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(1), `$."g"?(@."a" == 1)`, `$."g"?(@."a" == 1)`}}))
		update := "UPDATE jp_smoke SET p=$1 WHERE id=$2"
		uh := jsonpathReferenceHeld(t, p, b, update)
		referenceRunStep(t, p.ctx, uh, b.recorder, referenceExec(update, 1, `strict $.a[1,2, 3 to 16]`, 1))
		run(referenceSelect("SELECT p,q FROM jp_smoke WHERE id=1", []string{"p", "q"}, []string{"JSONPATH", "JSONPATH"}, [][]any{{`strict $."a"[1,2,3 to 16]`, `$."g"?(@."a" == 1)`}}))
		cast := "SELECT CAST($1 AS jsonpath) AS v"
		ch := jsonpathReferenceHeld(t, p, b, cast)
		referenceRunStep(t, p.ctx, ch, b.recorder, jsonpathReferenceStep(t, b, referenceSelect(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{`$"a"."b"`}}, "$a.b")))
		referenceRunStep(t, p.ctx, ch, b.recorder, jsonpathReferenceStep(t, b, referenceSelect(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{`$?(@ like_regex "pattern" flag "ism")`}}, `$ ? (@ like_regex "pattern" flag "isim")`)))
		bad := jsonpathReferenceDiagnostic(jsonpathReferenceFailures[13])
		jsonpathReferenceBindFailure(t, p, uh, update, bad, "1", `$ ? (@ like_regex "(invalid pattern")`, 1)
		literal := bad
		literal.Position = "8"
		s := referenceErrorDiagnostic("SELECT "+jsonReferenceQuote(`$ ? (@ like_regex "(invalid pattern")`)+"::jsonpath", literal)
		s.selectRows, s.errorStage = true, "execute"
		run(s)
		multi := "INSERT INTO jp_smoke VALUES(2,$1,$1),(3,$2,$2)"
		mh := jsonpathReferenceHeld(t, p, b, multi)
		jsonpathReferenceBindFailure(t, p, mh, multi, jsonpathReferenceDiagnostic(jsonpathReferenceFailures[1]), "2", "$", "last")
		run(referenceSelect("SELECT id FROM jp_smoke ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		referenceRunStep(t, p.ctx, uh, b.recorder, referenceExec(update, 1, "$", 1))
		referenceRunStep(t, p.ctx, mh, b.recorder, referenceExec(multi, 2, nil, "null"))
		run(referenceSelect("SELECT id,p,q FROM jp_smoke ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(1), "$", `$."g"?(@."a" == 1)`}, {int64(2), nil, nil}, {int64(3), "null", "null"}}))
	}
}
