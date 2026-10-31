package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorBytes(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910181)
		run(largeObjectValue("lo_from_bytea(910181::oid,decode('00ff616200','hex'))", "OID", int64(910181)))
		run(fixture.Exec("BEGIN", 0))
		r := descriptorOpen(t, p, c, prepared, 910181, descriptorRead, 0)
		other := descriptorOpen(t, p, c, prepared, 910181, descriptorRead, 1)
		for _, x := range []struct{ n int; bytes string }{{0,""},{-1,""},{2,string([]byte{0,255})},{2,"ab"},{20,string([]byte{0})},{1,""},{1,""}} {
			run(descriptorReadStep(r, x.n, x.bytes))
		}
		run(descriptorReadStep(other, 2, string([]byte{0,255})))
		run(descriptorClose(r))
		reused := descriptorOpen(t, p, c, prepared, 910181, descriptorRead, r)
		run(descriptorReadStep(reused, 2, string([]byte{0,255})))
		run(fixture.Exec("COMMIT", 0))
		run(largeObjectPut(910181, 0, "616263646566"))
		run(fixture.Exec("BEGIN", 0))
		w := descriptorOpen(t, p, c, prepared, 910181, descriptorWrite, 0)
		run(descriptorWriteStep(w, []byte("XY")))
		run(descriptorWriteStep(w, []byte{0,255}))
		run(descriptorWriteStep(w, []byte{}))
		run(largeObjectBytes(910181, "585900ff6566"))
		run(descriptorReadStep(w, 20, "ef"))
		run(descriptorReadStep(w, 1, ""))
		run(descriptorWriteStep(w, []byte{}))
		run(largeObjectBytes(910181, "585900ff6566"))
		run(descriptorClose(w))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
