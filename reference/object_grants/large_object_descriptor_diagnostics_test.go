package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorDiagnostics(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910185)
		run(largeObjectValue("lo_from_bytea(910185::oid,decode('616263','hex'))", "OID", int64(910185)))
		for _, fd := range []int{-1,2147483647} {
			for _, q := range []string{"SELECT lo_close($1::integer)","SELECT loread($1::integer,0)","SELECT loread($1::integer,-1)","SELECT lowrite($1::integer,decode('','hex'))"} {
				run(descriptorInvalid(q, fd, fd))
			}
		}
		for _, mode := range []int{0,1,-2147483648} {
			run(largeObjectError("SELECT lo_open(0::oid,$1::integer)", "22023", fmt.Sprintf("invalid flags for opening a large object: %d", mode), mode))
		}
		run(largeObjectMissing("SELECT lo_open(0::oid,262144)", 0))
		run(fixture.Exec("BEGIN", 0))
		fd := descriptorOpen(t, p, c, prepared, 910185, descriptorRead, 0)
		descriptorRecover(run, largeObjectError("SELECT lowrite($1::integer,decode('','hex'))", "55000", fmt.Sprintf("large object descriptor %d was not opened for writing", fd), fd))
		// Source-derived allocation expectation, intentionally pending PG18.6 discovery.
		for _, d := range []int{fd,-1} {
			descriptorRecover(run, largeObjectError("SELECT loread($1::integer,1073741824)", "XX000", "invalid memory alloc request size 1073741828", d))
		}
		run(descriptorReadStep(fd, 1, "a"))
		run(descriptorClose(fd))
		for _, q := range []string{"SELECT lo_close($1::integer)","SELECT loread($1::integer,1)","SELECT lowrite($1::integer,decode('','hex'))"} {
			descriptorRecover(run, descriptorInvalid(q, fd, fd))
		}
		run(fixture.Exec("ROLLBACK", 0))

		run(fixture.Exec("BEGIN READ ONLY", 0))
		fd = descriptorOpen(t, p, c, prepared, 910185, descriptorRead, 0)
		for _, mode := range []int{descriptorWrite,descriptorRead|descriptorWrite} {
			descriptorRecover(run, largeObjectError("SELECT lo_open(0::oid,$1::integer)", "25006", "cannot execute lo_open(INV_WRITE) in a read-only transaction", mode))
		}
		for _, d := range []int{fd,-1} {
			descriptorRecover(run, largeObjectError("SELECT lowrite($1::integer,decode('','hex'))", "25006", "cannot execute lowrite() in a read-only transaction", d))
		}
		// Every argument position is strict even beside invalid values/readonly.
		for _, x := range []struct{ expression, typ string; args []any }{
			{"lo_open($1::oid,0)","INT4",[]any{nil}},
			{"lo_open($1::oid,131072)","INT4",[]any{nil}},
			{"lo_open(0::oid,$1::integer)","INT4",[]any{nil}},
			{"lo_close($1::integer)","INT4",[]any{nil}},
			{"loread($1::integer,-1)","BYTEA",[]any{nil}},
			{"loread($1::integer,1073741824)","BYTEA",[]any{nil}},
			{"loread(-1,$1::integer)","BYTEA",[]any{nil}},
			{"lowrite($1::integer,decode('','hex'))","INT4",[]any{nil}},
			{"lowrite(-1,$1::bytea)","INT4",[]any{nil}},
			{"loread(0,$1::integer)","BYTEA",[]any{nil}},
			{"lowrite(0,$1::bytea)","INT4",[]any{nil}},
		} {
			run(largeObjectValue(x.expression, x.typ, nil, x.args...))
		}
		run(descriptorReadStep(fd, 1, "a"))
		run(descriptorClose(fd))
		// NULL open did not reserve any slots, and NULL close did not close fd.
		fd = descriptorOpen(t, p, c, prepared, 910185, descriptorRead, 0)
		run(descriptorReadStep(fd, 3, "abc"))
		run(fixture.Exec("ROLLBACK", 0))

		run(fixture.Exec("BEGIN", 0))
		w := descriptorOpen(t, p, c, prepared, 910185, descriptorWrite, 0)
		run(largeObjectValue("lowrite($1::integer,$2::bytea)", "INT4", nil, w, nil))
		run(largeObjectValue("loread($1::integer,$2::integer)", "BYTEA", nil, w, nil))
		run(descriptorWriteStep(w, []byte("Z")))
		run(descriptorReadStep(w, 3, "bc"))
		run(largeObjectBytes(910185, "5a6263"))
		for _, q := range []string{
			"SELECT lo_open($1::oid,1/$2::integer)",
			"SELECT loread($1::integer,1/$2::integer)",
			"SELECT lowrite(1/$2::integer,$1::bytea)",
		} {
			descriptorRecover(run, largeObjectError(q, "22012", "division by zero", nil, 0))
		}
		run(fixture.Exec("ROLLBACK", 0))
	})
}
