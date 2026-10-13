package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceACLItemNativeControls(t *testing.T) {
	roles := []string{"acl_control_a", "acl_control_b", "acl control quoted", "7654321"}
	aclitemScenario(t, roles, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_control (id INT,a aclitem,aa aclitem[])", 0))
		all := "acl_control_a=a*r*w*d*D*x*t*X*U*C*T*c*s*A*m*/acl_control_b"
		array := `[-1:0][3:4]={{acl_control_a=rw*/acl_control_b,NULL},{=r*/postgres,postgres=/postgres}}`
		quoted := `"acl control quoted"=r/7654321`
		run(fixture.Exec("INSERT INTO public.acl_control VALUES (1,$1::aclitem,$2::aclitem[])", 1, all, array))
		run(fixture.Prepared(fixture.Exec("INSERT INTO public.acl_control VALUES (2,$1::aclitem,$2::aclitem[]),(3,NULL,NULL)", 2, quoted, aclitemArray(quoted))))
		check := fixture.Select("SELECT id,a,aa FROM public.acl_control ORDER BY id", []string{"id", "a", "aa"}, []string{"INT4", "ACLITEM", "_ACLITEM"}, [][]any{{int64(1), all, array}, {int64(2), quoted, aclitemArray(quoted)}, {int64(3), nil, nil}})
		run(check)
		run(fixture.Prepared(check))
		if b.PG {
			return
		} // PostgreSQL has no native control API.
		setup := []fixture.Step{fixture.Exec("CREATE ROLE acl_control_spacer", 0)}
		for _, role := range roles {
			setup = append(setup, fixture.Exec("CREATE ROLE "+aclitemSQLName(role), 0))
		}
		fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check, setup...)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "CREATE ROLE") {
			t.Fatal("Dump serialized role DDL")
		}
		t.Run("rows_retained_through_restore", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			held, err := c.QueryContext(p.Ctx, check.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			metadata, err := held.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarAssertDescription(t, metadata, check.Columns, check.Types)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			run(check)
			run(fixture.Prepared(check))
			fixture.ScalarRows(t, false, held, check.Columns, check.Types, check.Rows, false)
			fixture.ScalarAssertDescription(t, metadata, check.Columns, check.Types)
		})
		// The same live-value SQL assertions below run on both backends above;
		// these explicit native controls assert atomic replay and public errors.
		run(fixture.Exec("CREATE TABLE public.acl_control_sentinel (v INT)", 0))
		run(fixture.Exec("INSERT INTO public.acl_control_sentinel VALUES (7)", 1))
		sentinel := fixture.Select("SELECT v FROM public.acl_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		for _, tc := range []struct{ input, typ, code, message, detail string }{
			{"missing_acl_role=r/postgres", "aclitem", "42704", `role "missing_acl_role" does not exist`, ""},
			{"postgres=z/postgres", "aclitem", "22P02", `invalid mode character: must be one of "arwdDxtXUCTcsAm"`, ""},
			{"{postgres=r/postgres,,NULL}", "aclitem[]", "22P02", `malformed array literal: "{postgres=r/postgres,,NULL}"`, `Unexpected "," character.`},
			{"{{postgres=r/postgres},{NULL,NULL}}", "aclitem[]", "22P02", `malformed array literal: "{{postgres=r/postgres},{NULL,NULL}}"`, "Multidimensional arrays must have sub-arrays with matching dimensions."},
		} {
			t.Run("staged_"+tc.code+"_"+tc.typ+"_"+tc.detail, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				held, err := c.QueryContext(p.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				// Restore parses the whole source stream once and executes its
				// original ASTs; diagnostic positions remain stream-relative.
				prefix := "CREATE TABLE public.acl_staged (a " + tc.typ + "); INSERT INTO public.acl_staged VALUES (NULL); INSERT INTO public.acl_staged VALUES ("
				stream := prefix + fixture.ArrayReferenceQuote(tc.input) + "::" + tc.typ + ");"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream))
				d := aclitemDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
				fixture.AssertDiagnostic(t, err, &d)
				run(sentinel)
				run(check)
				run(fixture.Error("SELECT a FROM public.acl_staged", "42P01"))
				fixture.ScalarRows(t, false, held, check.Columns, check.Types, check.Rows, false)
			})
		}
		failure := errors.New("aclitem control IO failure")
		if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure}); !errors.Is(err, failure) {
			t.Fatalf("Dump writer error=%v", err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
			t.Fatalf("Restore reader error=%v", err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled Restore=%v", err)
		}
		if result, err := c.ExecContext(ctx, "INSERT INTO public.acl_control_sentinel VALUES (99)"); result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled SQL result=%v error=%v", result, err)
		}
		run(sentinel)
		run(check)
		aclitemRestoreFence(t, p, dump.String(), roles, check)
	})
}

type aclitemGateReader struct {
	reader  io.Reader
	entered chan struct{}
	release chan struct{}
	first   bool
}

func (r *aclitemGateReader) Read(dst []byte) (int, error) {
	if !r.first {
		r.first = true
		close(r.entered)
		<-r.release
	}
	return r.reader.Read(dst)
}

func aclitemRestoreFence(t *testing.T, p *fixture.Pair, dump string, roles []string, sourceCheck fixture.Step) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	for _, role := range append([]string{"acl_fence_spacer"}, roles...) {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE "+aclitemSQLName(role), 0))
	}
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.acl_fence_sentinel (v INT)", 0))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.acl_fence_sentinel VALUES (7)", 1))
	reader := &aclitemGateReader{reader: strings.NewReader(dump), entered: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- stuntgres.Restore(target.Ctx, target.Native, reader) }()
	released, joined := false, false
	defer func() {
		if !released {
			close(reader.release)
		}
		if !joined {
			select {
			case <-result:
			case <-target.Ctx.Done():
				t.Error("Restore worker did not join before deadline")
			}
		}
	}()
	select {
	case <-reader.entered:
	case <-target.Ctx.Done():
		t.Fatal(target.Ctx.Err())
	}
	for _, q := range []string{"DROP ROLE acl_control_a", "CREATE ROLE acl_control_a"} {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
	}
	close(reader.release)
	released = true
	select {
	case err := <-result:
		joined = true
		if err == nil {
			t.Fatal("Restore adopted drop/recreated target identity")
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("identity fence is not ordinary error: %v", err)
		}
	case <-target.Ctx.Done():
		t.Fatal(target.Ctx.Err())
	}
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.acl_fence_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT a FROM public.acl_control", "42P01"))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, sourceCheck)
}
