package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

type aclitemValuer struct {
	value driver.Value
	err   error
}

func (v aclitemValuer) Value() (driver.Value, error) { return v.value, v.err }

func TestReferenceACLItemBinds(t *testing.T) {
	aclitemScenario(t, []string{"acl_binds_a", "acl_binds_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_binds (id INT,a aclitem,aa aclitem[])", 0))
		input := "acl_binds_a=wr**/acl_binds_b"
		canonical := "acl_binds_a=r*w/acl_binds_b"
		array := "[0:2]={acl_binds_a=wr**/acl_binds_b,=r*/postgres,NULL}"
		canonicalArray := "[0:2]={acl_binds_a=r*w/acl_binds_b,=r*/postgres,NULL}"
		id := 1
		want := [][]any{}
		for _, prepared := range []bool{false, true} {
			for _, representation := range []any{input, []byte(input), aclitemValuer{value: input}, aclitemValuer{value: []byte(input)}} {
				s := fixture.Exec("INSERT INTO public.acl_binds VALUES ($1,$2::aclitem,$3::aclitem[])", 1, id, representation, []byte(array))
				s.Prepared = prepared
				run(s)
				want = append(want, []any{int64(id), canonical, canonicalArray})
				id++
			}
			s := fixture.Exec("INSERT INTO public.acl_binds VALUES ("+fixture.Position(id)+","+fixture.ArrayReferenceQuote(input)+"::aclitem,"+fixture.ArrayReferenceQuote(array)+"::aclitem[])", 1)
			s.Prepared = prepared
			run(s)
			want = append(want, []any{int64(id), canonical, canonicalArray})
			id++
		}
		// The imported bytes must not remain caller-owned backing storage.
		bytes := []byte(input)
		run(fixture.Exec("INSERT INTO public.acl_binds VALUES ($1,$2,$3)", 1, id, bytes, aclitemValuer{value: array}))
		for i := range bytes {
			bytes[i] = 'z'
		}
		want = append(want, []any{int64(id), canonical, canonicalArray})
		id++
		for _, null := range []any{nil, []byte(nil), aclitemValuer{value: nil}} {
			run(fixture.Exec("INSERT INTO public.acl_binds VALUES ($1,$2,$3)", 1, id, null, null))
			want = append(want, []any{int64(id), nil, nil})
			id++
		}
		q := "SELECT $1::aclitem AS a,$1::aclitem AS again,$2::aclitem[] AS aa"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct{ scalar, array, out string }{
			{input, array, canonical}, {"=r*/postgres", "{NULL}", "=r*/postgres"}, {input, array, canonical},
		} {
			arrayOut := canonicalArray
			if tc.array == "{NULL}" {
				arrayOut = "{NULL}"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"a", "again", "aa"}, []string{"ACLITEM", "ACLITEM", "_ACLITEM"}, [][]any{{tc.out, tc.out, arrayOut}}, tc.scalar, tc.array))
		}
		failure := errors.New("aclitem Valuer failure")
		for _, prepared := range []bool{false, true} {
			var result sql.Result
			var callErr error
			if prepared {
				statement, err := c.PrepareContext(p.Ctx, "INSERT INTO public.acl_binds VALUES (999,$1,NULL)")
				if err != nil {
					t.Fatal(err)
				}
				result, callErr = statement.ExecContext(p.Ctx, aclitemValuer{err: failure})
				statement.Close()
			} else {
				result, callErr = c.ExecContext(p.Ctx, "INSERT INTO public.acl_binds VALUES (999,$1,NULL)", aclitemValuer{err: failure})
			}
			if result != nil || !errors.Is(callErr, failure) {
				t.Fatalf("Valuer result=%v error=%v", result, callErr)
			}
			if _, ok := callErr.(interface{ SQLState() string }); ok {
				t.Fatalf("Valuer failure became PostgreSQL error: %v", callErr)
			}
		}
		check := fixture.Select("SELECT id,a,aa FROM public.acl_binds ORDER BY id", []string{"id", "a", "aa"}, []string{"INT4", "ACLITEM", "_ACLITEM"}, want)
		run(check)
		run(fixture.Prepared(check))
	})
}
