package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorPrepared(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910189)
		run(fixture.Select("SELECT oid,proname,proargtypes::text AS args,prorettype,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid IN (952,953,954,955) ORDER BY oid", []string{"oid","proname","args","prorettype","proisstrict","provolatile","proparallel"}, []string{"OID","NAME","TEXT","OID","BOOL","CHAR","CHAR"}, [][]any{
			{int64(952),"lo_open","26 23",int64(23),true,"v","u"},
			{int64(953),"lo_close","23",int64(23),true,"v","u"},
			{int64(954),"loread","23 23",int64(17),true,"v","u"},
			{int64(955),"lowrite","23 17",int64(23),true,"v","u"},
		}))
		run(largeObjectValue("lo_from_bytea(910189::oid,decode('616263','hex'))", "OID", int64(910189)))
		queries := []string{
			"SELECT lo_open($1::oid,$2::integer) AS v",
			"SELECT loread($1::integer,$2::integer) AS v",
			"SELECT lowrite($1::integer,$2::bytea) AS v",
			"SELECT lo_close($1::integer) AS v",
			"SELECT lo_open(0::oid,0) AS v",
			"SELECT lo_open(0::oid,262144) AS v",
			"SELECT loread(-1,1) AS v",
			"SELECT lowrite(-1,decode('','hex')) AS v",
			"SELECT lo_close(-1) AS v",
		}
		held := make([]*sql.Stmt, len(queries))
		for i, q := range queries {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal("prepare must be quiet", q, err) }
			held[i] = s
			defer s.Close()
		}
		execute := func(i int, s fixture.Step) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor:c, Statement:held[i], SQL:queries[i]}, b.Recorder, s)
		}
		// Preparation neither consumed the first slot nor touched bytes/cursors.
		for range 2 {
			run(fixture.Exec("BEGIN", 0))
			execute(0, largeObjectValue("lo_open($1::oid,$2::integer)", "INT4", int64(0), int64(910189), descriptorWrite))
			execute(1, descriptorReadStep(0, 1, "a"))
			execute(2, descriptorWriteStep(0, []byte("Z")))
			execute(1, descriptorReadStep(0, 10, "c"))
			execute(3, descriptorClose(0))
			run(fixture.Exec("ROLLBACK", 0))
			run(largeObjectBytes(910189, "616263"))
		}
		execute(4, largeObjectError(queries[4], "22023", "invalid flags for opening a large object: 0"))
		execute(5, largeObjectMissing(queries[5], 0))
		for _, i := range []int{6,7,8} { execute(i, descriptorInvalid(queries[i], -1)) }
		// Invalid arity and client-only spellings remain ordinary resolver errors.
		for _, x := range []struct{ q, signature string }{
			{"SELECT lo_open(1::integer)","lo_open(integer)"},
			{"SELECT lo_close(1::integer,2::integer)","lo_close(integer, integer)"},
			{"SELECT loread(1::integer)","loread(integer)"},
			{"SELECT lowrite(1::integer,2::integer)","lowrite(integer, integer)"},
			{"SELECT lo_read(1::integer,2::integer)","lo_read(integer, integer)"},
			{"SELECT lo_write(1::integer,decode('','hex'))","lo_write(integer, bytea)"},
		} {
			s := fixture.ImplicitError(x.q, fixture.Diagnostic{Code:"42883", Message:"function "+x.signature+" does not exist", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"8"})
			s.SelectRows = true
			run(s)
		}
	})
}
