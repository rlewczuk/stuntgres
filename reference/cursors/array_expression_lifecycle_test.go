package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayExpressionPrepared(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayExpressionFixture(t, p, b, c)
		q := "SELECT ARRAY[$1::int,2] AS a,($2::int[])[$3] AS v FROM public.one"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			args []any
			row  []any
		}{{[]any{7, "{10,20}", 2}, []any{"{7,2}", int64(20)}}, {[]any{nil, "{10,20}", 1}, []any{"{NULL,2}", int64(10)}}, {[]any{9, nil, 1}, []any{"{9,2}", nil}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ArrayReferenceSelect(q, []string{"a", "v"}, []string{"_INT4", "INT4"}, []string{"_INT4", "INT4"}, [][]any{tc.row}, b.PG, tc.args...))
		}
		q = "UPDATE public.ax SET a[$1]=$2 WHERE id=1"
		update, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer update.Close()
		held = fixture.HeldStatement{Executor: c, Statement: update, SQL: q}
		for _, tc := range []struct {
			args []any
			want string
		}{{[]any{2, 7}, "{10,7,30}"}, {[]any{5, 9}, "{10,7,30,NULL,9}"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, tc.args...))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect("SELECT a FROM public.ax WHERE id=1", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22004", "array subscript in assignment must not be null"), nil, 3))
		q = "UPDATE public.ax SET a[$1:$2]=$3 WHERE id=1"
		slice, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer slice.Close()
		held = fixture.HeldStatement{Executor: c, Statement: slice, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1, 2, "{8,9}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "source array too small"), 1, 2, "{7}"))
		bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		bad.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, bad, "bad", 2, "{7,8}"))
		bad = fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		bad.Detail = `Unexpected "," character.`
		bad.Context = "unnamed portal parameter $3 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, bad, 1, 2, "{1,,2}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1, 2, "{4,5}"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect("SELECT a FROM public.ax WHERE id=1", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{4,5,30,NULL,9}"}}, b.PG))
	})
}

func arrayExpressionStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE TABLE public.ax_stored (id INT,a INT[] DEFAULT ARRAY[1,2],g INT GENERATED ALWAYS AS (a[1]) STORED,CONSTRAINT ax_positive CHECK (a[1]>0))",
		"CREATE INDEX ax_index ON public.ax_stored ((a[1]))",
		"CREATE VIEW public.ax_view AS SELECT id,a[1] AS e,a[1:2] AS s,ARRAY[id] AS c FROM public.ax_stored",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO public.ax_stored(id) VALUES(1)", 1))
	run(fixture.Prepared(fixture.Exec("INSERT INTO public.ax_stored(id,a) VALUES(2,NULL)", 1)))
	check := fixture.ArrayReferenceSelect("SELECT id,e,s,c FROM public.ax_view ORDER BY id", []string{"id", "e", "s", "c"}, []string{"INT4", "INT4", "_INT4", "_INT4"}, []string{"INT4", "INT4", "_INT4", "_INT4"}, [][]any{{int64(1), int64(1), "{1,2}", "{1}"}, {int64(2), nil, nil, "{2}"}}, b.PG)
	arrayExpressionBoth(t, p, b, c, check)
	run(fixture.Select("SELECT id,g FROM public.ax_stored WHERE a[1]>0 ORDER BY a[1]", []string{"id", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
	return check
}

func TestReferenceArrayExpressionStoredConsumers(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayExpressionStored(t, p, b, c)
	})
}

func TestReferenceArrayExpressionNativeControls(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := arrayExpressionStored(t, p, b, c)
		if b.PG {
			return
		}
		fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, script := range []string{
			"CREATE TABLE public.ax_reject (a INT[] CHECK (a[$1]>0));",
			"/*é*/ CREATE TABLE public.ax_reject (a INT[], CHECK (a[abs($1)]>0));",
			"CREATE TABLE public.ax_reject (v INT DEFAULT (ARRAY[$1::int])[1]);",
			"CREATE TABLE public.ax_reject (a INT[],v INT GENERATED ALWAYS AS (a[$1]) STORED);",
			"CREATE TABLE public.ax_reject (a INT[]); INSERT INTO public.ax_reject(a[$1]) VALUES(7);",
			"CREATE TABLE public.ax_reject (a INT[]); INSERT INTO public.ax_reject VALUES(ARRAY[$1::int]);",
		} {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
			want := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
			want.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
			fixture.AssertDiagnostic(t, err, &want)
			var rejected bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &rejected); err != nil {
				t.Fatal(err)
			}
			if rejected.String() != dump.String() {
				t.Fatal("failed Restore changed catalog")
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore changed catalog")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := c.QueryContext(ctx, "SELECT (ARRAY[1,2])[1] FROM public.one"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled access: %v", err)
		}
	})
}

func TestReferenceArrayExpressionFeatureSmoke(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.ax_smoke (id INT,m INT[])", 0))
		run(fixture.Exec("INSERT INTO public.ax_smoke VALUES(1,ARRAY[[1,2],[3,4]])", 1))
		q := "INSERT INTO public.ax_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "{{5,6},{7,8}}"))
		run(fixture.ArrayReferenceSelect("SELECT m[2][1] AS e,m[2][1:1] AS s,m[9:10] AS z,m[NULL][1] AS n FROM public.ax_smoke WHERE id=1", []string{"e", "s", "z", "n"}, []string{"INT4", "_INT4", "_INT4", "INT4"}, []string{"INT4", "_INT4", "_INT4", "INT4"}, [][]any{{int64(3), "{{1},{3}}", "{}", nil}}, b.PG))
		run(fixture.Exec("UPDATE public.ax_smoke SET m[2][1]=9 WHERE id=1", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE public.ax_smoke SET m[1:2][1:1]=ARRAY[8,9] WHERE id=1", 1)))
		check := fixture.ArrayReferenceSelect("SELECT id,m FROM public.ax_smoke ORDER BY id", []string{"id", "m"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{{8,2},{9,4}}"}, {int64(2), "{{5,6},{7,8}}"}}, b.PG)
		run(check)
		run(fixture.ArrayReferenceSelect("SELECT ARRAY[]::int[] AS a FROM public.one", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{}"}}, b.PG))
		run(fixture.ErrorDiagnostic("UPDATE public.ax_smoke SET m=ARRAY[ARRAY[1],ARRAY[2,3]] WHERE id=1", fixture.ControlsReferenceDiagnostic("2202E", "multidimensional arrays must have array expressions with matching dimensions")))
		run(fixture.ErrorDiagnostic("UPDATE public.ax_smoke SET m[1:2][1:1]=ARRAY[7] WHERE id=1", fixture.ControlsReferenceDiagnostic("2202E", "source array too small")))
		run(check)
		run(fixture.Exec("UPDATE public.ax_smoke SET m[1][1]=m[2][1],m[2][1]=m[1][1] WHERE id=1", 1))
		check.Rows = [][]any{{int64(1), "{{9,2},{8,4}}"}, {int64(2), "{{5,6},{7,8}}"}}
		run(check)
		q = "UPDATE public.ax_smoke SET m[$1][1]=$2 WHERE id=1"
		update, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer update.Close()
		held = fixture.HeldStatement{Executor: c, Statement: update, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22004", "array subscript in assignment must not be null"), nil, 3))
		run(check)
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
	})
}
