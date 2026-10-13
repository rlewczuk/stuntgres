package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemIdentity(t *testing.T) {
	aclitemScenario(t, []string{"acl_identity_a", "acl_identity_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		identity := func(role string) int64 {
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", role).Scan(&id); err != nil || id <= 0 {
				t.Fatalf("role %s identity=%d error=%v", role, id, err)
			}
			return id
		}
		oldA, oldB := identity("acl_identity_a"), identity("acl_identity_b")
		run(fixture.Exec("CREATE TABLE public.acl_identity (id INT,a aclitem,aa aclitem[])", 0))
		value := "acl_identity_a=rw*/acl_identity_b"
		array := "[0:1]={acl_identity_a=rw*/acl_identity_b,NULL}"
		run(fixture.Exec("INSERT INTO public.acl_identity VALUES (1,$1::aclitem,$2::aclitem[])", 1, value, array))
		literalQ := "SELECT 'acl_identity_a=rw*/acl_identity_b'::aclitem AS a,'[0:1]={acl_identity_a=rw*/acl_identity_b,NULL}'::aclitem[] AS aa"
		literal, err := c.PrepareContext(p.Ctx, literalQ)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		parameterQ := "SELECT $1::aclitem AS a,$2::aclitem[] AS aa"
		parameter, err := c.PrepareContext(p.Ctx, parameterQ)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		check := func(executor fixture.Executor, q string, wantA, wantAA string, args ...any) {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{wantA, wantAA}}, args...))
		}
		literalExecutor := fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalQ}
		parameterExecutor := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterQ}
		check(literalExecutor, literalQ, value, array)
		check(parameterExecutor, parameterQ, value, array, value, array)
		storedQ := "SELECT a,aa FROM public.acl_identity ORDER BY id"
		var held *sql.Rows
		var metadata []*sql.ColumnType
		if !b.PG {
			held, err = c.QueryContext(p.Ctx, storedQ)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			metadata = aclitemMetadata(t, false, held, []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, []bool{true, true}, false)
		}
		for _, role := range []string{"acl_identity_a", "acl_identity_b"} {
			run(fixture.Exec("DROP ROLE "+role, 0))
			run(fixture.Exec("CREATE ROLE "+role, 0))
			newID := identity(role)
			oldID := oldA
			if role == "acl_identity_b" {
				oldID = oldB
			}
			if newID == oldID {
				t.Fatalf("recreated %s adopted old identity %d", role, oldID)
			}
			staleA := strconv.FormatInt(oldA, 10) + "=rw*/acl_identity_b"
			if role == "acl_identity_b" {
				staleA = strconv.FormatInt(oldA, 10) + "=rw*/" + strconv.FormatInt(oldB, 10)
			}
			staleAA := "[0:1]={" + staleA + ",NULL}"
			check(c, storedQ, staleA, staleAA)
			check(literalExecutor, literalQ, staleA, staleAA)
			check(parameterExecutor, parameterQ, value, array, value, array)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT a=$1::aclitem AS same_scalar,aa=$2::aclitem[] AS same_array FROM public.acl_identity", []string{"same_scalar", "same_array"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}, value, array)
				s.Prepared = prepared
				run(s)
				s = fixture.Select("SELECT a=a AS same_scalar,aa=aa AS same_array FROM public.acl_identity", []string{"same_scalar", "same_array"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
				s.Prepared = prepared
				run(s)
			}
		}
		if held != nil {
			fixture.ScalarRows(t, false, held, []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{value, array}}, false)
			fixture.ScalarAssertDescription(t, metadata, []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"})
		}
	})
}
