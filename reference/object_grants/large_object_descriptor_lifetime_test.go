package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorLifetime(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910186, 910187)
		run(largeObjectValue("lo_from_bytea(910186::oid,decode('616263646566','hex'))", "OID", int64(910186)))
		run(largeObjectValue("lo_from_bytea(910187::oid,decode('58595a','hex'))", "OID", int64(910187)))
		peer, observe := descriptorPeer(t, p, b, prepared)
		for _, terminal := range []string{"COMMIT","ROLLBACK"} {
			run(fixture.Exec("BEGIN", 0))
			fd := descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
			run(fixture.Exec(terminal, 0))
			run(descriptorInvalid("SELECT loread($1::integer,1)", fd, fd))
		}
		fd := descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		run(descriptorInvalid("SELECT lo_close($1::integer)", fd, fd))
		run(fixture.Exec("BEGIN", 0))
		fd = descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		run(largeObjectError("SELECT loread(-1,1)", "42704", "invalid large-object descriptor: -1"))
		run(fixture.Exec("ROLLBACK", 0))
		run(descriptorInvalid("SELECT loread($1::integer,1)", fd, fd))

		run(fixture.Exec("BEGIN", 0))
		fd = descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		observe(descriptorInvalid("SELECT loread($1::integer,1)", fd, fd))
		observe(fixture.Exec("BEGIN", 0))
		pf := descriptorOpen(t, p, peer, prepared, 910187, descriptorRead, 0)
		run(descriptorReadStep(fd, 1, "a"))
		observe(descriptorReadStep(pf, 1, "X"))
		observe(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("ROLLBACK", 0))

		run(fixture.Exec("BEGIN", 0))
		parent := descriptorOpen(t, p, c, prepared, 910186, descriptorWrite, 0)
		run(fixture.Exec("SAVEPOINT outer_mark", 0))
		child := descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 1)
		run(descriptorWriteStep(parent, []byte("XY")))
		run(fixture.Exec("ROLLBACK TO outer_mark", 0))
		descriptorRecover(run, descriptorInvalid("SELECT loread($1::integer,1)", child, child))
		run(descriptorReadStep(parent, 2, "cd"))
		run(largeObjectBytes(910186, "616263646566"))
		run(fixture.Exec("ROLLBACK TO outer_mark", 0))
		run(descriptorReadStep(parent, 2, "ef"))
		run(descriptorClose(parent))
		reused := descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		run(fixture.Exec("ROLLBACK TO outer_mark", 0))
		descriptorRecover(run, descriptorInvalid("SELECT loread($1::integer,1)", reused, reused))
		// A close of a pre-savepoint descriptor is never resurrected.
		descriptorRecover(run, descriptorInvalid("SELECT lo_close($1::integer)", parent, parent))
		run(fixture.Exec("SAVEPOINT duplicate_mark", 0))
		run(fixture.Exec("SAVEPOINT duplicate_mark", 0))
		child = descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		run(fixture.Exec("RELEASE duplicate_mark", 0))
		run(descriptorReadStep(child, 1, "a"))
		run(fixture.Exec("ROLLBACK TO duplicate_mark", 0))
		descriptorRecover(run, descriptorInvalid("SELECT loread($1::integer,1)", child, child))
		run(fixture.Exec("ROLLBACK TO outer_mark", 0))
		run(fixture.Exec("ROLLBACK", 0))

		// Successful local unlink closes every matching handle, even if undone.
		run(fixture.Exec("BEGIN", 0))
		r := descriptorOpen(t, p, c, prepared, 910186, descriptorRead, 0)
		w := descriptorOpen(t, p, c, prepared, 910186, descriptorWrite, 1)
		observe(fixture.Exec("BEGIN", 0))
		pf = descriptorOpen(t, p, peer, prepared, 910186, descriptorRead, 0)
		run(fixture.Exec("SAVEPOINT unlink_mark", 0))
		run(largeObjectValue("lo_unlink(910186::oid)", "INT4", int64(1)))
		run(fixture.Exec("ROLLBACK TO unlink_mark", 0))
		run(largeObjectBytes(910186, "616263646566"))
		for _, d := range []int{r,w} { descriptorRecover(run, descriptorInvalid("SELECT loread($1::integer,1)", d, d)) }
		observe(descriptorReadStep(pf, 6, "abcdef"))
		observe(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("ROLLBACK", 0))

		// Force physical disposal via database/sql's documented negative idle
		// lifetime rather than pretending Conn.Close alone closes a backend.
		b.DB.SetMaxIdleConns(0)
		disposed, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		if _, err := disposed.ExecContext(p.Ctx, "BEGIN"); err != nil { t.Fatal(err) }
		old := descriptorOpen(t, p, disposed, prepared, 910186, descriptorRead, 0)
		if err := disposed.Close(); err != nil { t.Fatal(err) }
		replacement, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer replacement.Close()
		s := descriptorInvalid("SELECT loread($1::integer,1)", old, old)
		s.Prepared = prepared
		objectGrantStep(t, p.Ctx, replacement, b.Recorder, s)
	})
}
