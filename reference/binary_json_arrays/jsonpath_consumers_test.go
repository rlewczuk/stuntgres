package reference_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathReferenceBindFailure(t *testing.T, p *fixture.Pair, executor fixture.Executor, query string, want fixture.Diagnostic, slot string, args ...any) {
	t.Helper()
	result, err := executor.ExecContext(p.Ctx, query, args...)
	if err == nil {
		t.Fatalf("invalid bind succeeded: %s", query)
	}
	if result != nil {
		t.Fatal("invalid bind published result")
	}
	got := fixture.ExtractDiagnostic(t, err)
	want.Context = "unnamed portal parameter $" + slot + " = '...'"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bind diagnostic got %#v want %#v", got, want)
	}
}

func jsonpathReferenceHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
}

func TestReferenceJSONPathConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, jsonpathReferenceStep(t, b, s)) }
		run(fixture.Exec("CREATE TABLE jp_frames(id INT,p jsonpath,q jsonpath)", 0))
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
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i, tc.arg))
			want = append(want, []any{int64(i), tc.want, tc.want})
			if i == 1 {
				buf[0] = 'x'
			}
		}
		run(fixture.Select("SELECT id,p,q FROM jp_frames ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, want))
		bad := jsonpathReferenceDiagnostic(jsonpathReferenceFailures[1])
		jsonpathReferenceBindFailure(t, p, held, q, bad, "2", 99, "last")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 99, `strict $`))
		update := "UPDATE jp_frames SET p=$1 WHERE id=$2"
		uh := jsonpathReferenceHeld(t, p, b, update)
		jsonpathReferenceBindFailure(t, p, uh, update, bad, "1", "last", 99)
		run(fixture.Select("SELECT p,q FROM jp_frames WHERE id=99", []string{"p", "q"}, []string{"JSONPATH", "JSONPATH"}, [][]any{{"strict $", "strict $"}}))
		fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Exec(update, 1, "$a.b", 99))
		multi := "INSERT INTO jp_frames VALUES(100,$1,$1),(101,$2,$2)"
		mh := jsonpathReferenceHeld(t, p, b, multi)
		jsonpathReferenceBindFailure(t, p, mh, multi, bad, "2", "$", "last")
		run(fixture.Select("SELECT id FROM jp_frames WHERE id >= 100 ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
		fixture.RunStep(t, p.Ctx, mh, b.Recorder, fixture.Exec(multi, 2, "$", "null"))
		run(fixture.Select("SELECT id,p,q FROM jp_frames WHERE id >= 99 ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(99), `$"a"."b"`, "strict $"}, {int64(100), "$", "$"}, {int64(101), "null", "null"}}))
		cast := "SELECT CAST($1 AS jsonpath) AS v"
		ch := jsonpathReferenceHeld(t, p, b, cast)
		for _, tc := range jsonpathReferenceOutputs[:4] {
			fixture.RunStep(t, p.Ctx, ch, b.Recorder, jsonpathReferenceStep(t, b, fixture.Select(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{tc.output}}, tc.input)))
		}
		// Encoding is a protocol/character-input boundary, not path grammar.
		for _, tc := range []struct {
			arg     any
			message string
		}{
			{string([]byte{0xff}), `invalid byte sequence for encoding "UTF8": 0xff`},
			{[]byte{'$', 0}, `invalid byte sequence for encoding "UTF8": 0x00`},
		} {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: tc.message, Context: "unnamed portal parameter $1"}
			s := fixture.ErrorDiagnostic(cast, want, tc.arg)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, ch, b.Recorder, s)
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			for _, sql := range []string{"BEGIN", "SAVEPOINT jp_before"} {
				if _, err := conn.ExecContext(p.Ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			jsonpathReferenceBindFailure(t, p, conn, "INSERT INTO jp_frames VALUES(102,$1,$1)", bad, "1", "last")
			for _, sql := range []string{"ROLLBACK TO SAVEPOINT jp_before", "INSERT INTO jp_frames VALUES(102,'$','$')", "COMMIT"} {
				if _, err := conn.ExecContext(p.Ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
		}()
		run(fixture.Select("SELECT p FROM jp_frames WHERE id=102", []string{"p"}, []string{"JSONPATH"}, [][]any{{"$"}}))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := b.DB.ExecContext(ctx, "INSERT INTO jp_frames VALUES(103,$1,$1)", "$"); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled bind: %v", err)
		}
		run(fixture.Select("SELECT id FROM jp_frames WHERE id=103", []string{"id"}, []string{"INT4"}, nil))
	}
}

func TestReferenceJSONPathFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, jsonpathReferenceStep(t, b, s)) }
		run(fixture.Exec("CREATE TABLE jp_smoke(id INT,p jsonpath,q jsonpath)", 0))
		inquiry := func(s fixture.Step) { run(jsonpathReferenceInquiry(b, s)) }
		inquiry(fixture.Select("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"jsonpath", int64(4072), int64(4073), int64(0)}}))
		inquiry(fixture.Select("SELECT typname::text,oid::bigint,typarray::bigint,typelem::bigint FROM pg_catalog.pg_type WHERE typname='_jsonpath'", []string{"typname", "oid", "typarray", "typelem"}, []string{"TEXT", "INT8", "INT8", "INT8"}, [][]any{{"_jsonpath", int64(4073), int64(0), int64(4072)}}))
		insert := "INSERT INTO jp_smoke VALUES($1,$2,$2)"
		ih := jsonpathReferenceHeld(t, p, b, insert)
		fixture.RunStep(t, p.Ctx, ih, b.Recorder, fixture.Exec(insert, 1, 1, `$.g ? (@.a == 1)`))
		run(fixture.Select("SELECT id,p,q FROM jp_smoke ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(1), `$."g"?(@."a" == 1)`, `$."g"?(@."a" == 1)`}}))
		update := "UPDATE jp_smoke SET p=$1 WHERE id=$2"
		uh := jsonpathReferenceHeld(t, p, b, update)
		fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Exec(update, 1, `strict $.a[1,2, 3 to 16]`, 1))
		run(fixture.Select("SELECT p,q FROM jp_smoke WHERE id=1", []string{"p", "q"}, []string{"JSONPATH", "JSONPATH"}, [][]any{{`strict $."a"[1,2,3 to 16]`, `$."g"?(@."a" == 1)`}}))
		cast := "SELECT CAST($1 AS jsonpath) AS v"
		ch := jsonpathReferenceHeld(t, p, b, cast)
		fixture.RunStep(t, p.Ctx, ch, b.Recorder, jsonpathReferenceStep(t, b, fixture.Select(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{`$"a"."b"`}}, "$a.b")))
		fixture.RunStep(t, p.Ctx, ch, b.Recorder, jsonpathReferenceStep(t, b, fixture.Select(cast, []string{"v"}, []string{"JSONPATH"}, [][]any{{`$?(@ like_regex "pattern" flag "ism")`}}, `$ ? (@ like_regex "pattern" flag "isim")`)))
		bad := jsonpathReferenceDiagnostic(jsonpathReferenceFailures[13])
		jsonpathReferenceBindFailure(t, p, uh, update, bad, "1", `$ ? (@ like_regex "(invalid pattern")`, 1)
		literal := bad
		literal.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT "+jsonReferenceQuote(`$ ? (@ like_regex "(invalid pattern")`)+"::jsonpath", literal)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		multi := "INSERT INTO jp_smoke VALUES(2,$1,$1),(3,$2,$2)"
		mh := jsonpathReferenceHeld(t, p, b, multi)
		jsonpathReferenceBindFailure(t, p, mh, multi, jsonpathReferenceDiagnostic(jsonpathReferenceFailures[1]), "2", "$", "last")
		run(fixture.Select("SELECT id FROM jp_smoke ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, uh, b.Recorder, fixture.Exec(update, 1, "$", 1))
		fixture.RunStep(t, p.Ctx, mh, b.Recorder, fixture.Exec(multi, 2, nil, "null"))
		run(fixture.Select("SELECT id,p,q FROM jp_smoke ORDER BY id", []string{"id", "p", "q"}, []string{"INT4", "JSONPATH", "JSONPATH"}, [][]any{{int64(1), "$", `$."g"?(@."a" == 1)`}, {int64(2), nil, nil}, {int64(3), "null", "null"}}))
	}
}
