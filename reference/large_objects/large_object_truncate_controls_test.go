package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncateControls(t *testing.T) {
	truncateScenario(t, []string{"lot_truncate_owner", "lot_truncate_reader"}, func(s *truncateSession) {
		defer s.cleanup(928307, 928308, 928309)
		s.run(fixture.Exec("SET ROLE lot_truncate_owner", 0))
		s.create(928307, "616263")
		s.create(928308, "")
		s.create(928309, "")
		s.run(fixture.Exec("BEGIN", 0))
		zero := s.open(928307, truncateWrite, 0)
		interior := s.open(928308, truncateWrite, 1)
		maximum := s.open(928309, truncateWrite, 2)
		s.run(truncateCall("lo_truncate", zero, 0))
		s.run(truncateCall("lo_truncate64", interior, 2048))
		s.run(truncateCall("lo_truncate64", interior, 4096))
		s.run(truncateValue("lo_put(928308::oid,8192::bigint,decode('5a','hex'))", "VOID", ""))
		s.run(truncateCall("lo_truncate64", maximum, truncateMaximum))
		s.run(fixture.Exec("COMMIT", 0))
		s.run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 928308 TO lot_truncate_reader", 0))
		s.run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 928309 FROM lot_truncate_owner", 0))
		s.run(fixture.Exec("RESET ROLE", 0))
		verify := func(v *truncateSession) {
			v.pages(928307, []any{int64(0), int64(0), ""})
			v.pages(928308, []any{int64(1), int64(0), ""}, []any{int64(2), int64(0), ""}, []any{int64(4), int64(1), "5a"})
			v.pages(928309, []any{int64(2147483647), int64(0), ""})
			v.bytes(928307, 0, 1, "")
			v.bytes(928308, 8190, 4, "00005a")
			v.bytes(928309, truncateMaximum-2, 4, "0000")
			v.bytes(928309, truncateMaximum, 1, "")
			v.run(fixture.Select("SELECT oid::bigint,lomowner=(SELECT oid FROM pg_roles WHERE rolname='lot_truncate_owner') AS owned,lomacl FROM pg_largeobject_metadata WHERE oid IN (928307,928308,928309) ORDER BY oid",
				[]string{"oid", "owned", "lomacl"}, []string{"INT8", "BOOL", "_ACLITEM"}, [][]any{
					{int64(928307), true, nil},
					{int64(928308), true, "{lot_truncate_owner=rw/lot_truncate_owner,lot_truncate_reader=r/lot_truncate_owner}"},
					{int64(928309), true, "{}"},
				}))
		}
		denied := func(v *truncateSession) {
			v.run(fixture.Exec("SET ROLE lot_truncate_owner", 0))
			v.run(truncateError("SELECT lo_open(928309::oid,131072)", "42501", "permission denied for large object 928309"))
			v.run(fixture.Exec("RESET ROLE", 0))
		}
		verify(s)
		denied(s)
		if s.b.PG { return } // Public controls have no PostgreSQL equivalent.

		step := truncateCall("lo_truncate64", int64(0), 5)
		held, stmt := s.held(step.Query)
		defer stmt.Close()
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928307, truncateWrite, 0)
		s.execute(held, truncateCall("lo_truncate64", fd, 5))
		s.pages(928307, []any{int64(0), int64(5), "0000000000"})
		var saved bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &saved); err != nil { s.t.Fatal(err) }
		for _, expected := range []string{
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928307::pg_catalog.oid,131072::pg_catalog.int4),0::pg_catalog.int8);",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928308::pg_catalog.oid,131072::pg_catalog.int4),2048::pg_catalog.int8);",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928308::pg_catalog.oid,131072::pg_catalog.int4),4096::pg_catalog.int8);",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928309::pg_catalog.oid,131072::pg_catalog.int4),4398046509056::pg_catalog.int8);",
		} {
			if !strings.Contains(saved.String(), expected) { s.t.Fatalf("missing endpoint replay %s", expected) }
		}
		for _, call := range []string{"loread(", "lowrite(", "lo_close("} {
			if strings.Contains(saved.String(), call) { s.t.Fatal("serialized source descriptor operation", call) }
		}
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil { s.t.Fatal(err) }
		_, err := stmt.ExecContext(s.p.Ctx, fd, 1)
		fixture.TransactionOrdinary(s.t, err, "stuntgres: catalog replaced before publication")
		s.run(fixture.Exec("ROLLBACK", 0))
		s.run(truncateError("SELECT lo_truncate64(0,1)", "42704", "invalid large-object descriptor: 0"))
		s.run(fixture.Select("SELECT oid::bigint FROM pg_largeobject_metadata WHERE oid IN (928307,928308,928309) ORDER BY oid", []string{"oid"}, []string{"INT8"}, nil))
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(saved.Bytes())); err != nil { s.t.Fatal(err) }
		verify(s) // The private length five was not captured.
		denied(s)
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928307, truncateWrite, 0)
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(saved.Bytes())); err != nil { s.t.Fatal(err) }
		_, err = stmt.ExecContext(s.p.Ctx, fd, 1)
		fixture.TransactionOrdinary(s.t, err, "stuntgres: catalog replaced before publication")
		s.run(fixture.Exec("ROLLBACK", 0))
		s.run(truncateError("SELECT lo_truncate64(0,1)", "42704", "invalid large-object descriptor: 0"))
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928307, truncateWrite, 0)
		s.execute(held, truncateCall("lo_truncate64", fd, 1))
		s.bytes(928307, 0, 2, "00")
		s.run(fixture.Exec("ROLLBACK", 0))
		verify(s)

		// A fresh Driver owns a genuinely isolated destination. Every SQL
		// witness, including cleanup, uses its own recording adapter.
		connector, err := stuntgres.NewDriver().OpenConnector("truncate-destination/"+s.t.Name())
		if err != nil { s.t.Fatal(err) }
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(s.t, connector)
		defer recorded.Close()
		conn, err := recorded.Conn(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		dest := &truncateSession{t:s.t, p:s.p, b:fixture.Backend{DB:recorded, Recorder:recorder}, c:conn, prepared:s.prepared}
		defer dest.close()
		defer dest.cleanup(928307, 928308, 928309, 928310)
		for _, role := range []string{"lot_truncate_owner", "lot_truncate_reader"} { dest.run(fixture.Exec("CREATE ROLE "+role, 0)) }
		dest.run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE lot_truncate_owner GRANT SELECT ON LARGE OBJECTS TO lot_truncate_reader", 0))
		dest.run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE lot_truncate_owner REVOKE ALL ON LARGE OBJECTS FROM lot_truncate_owner", 0))
		if err := stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(saved.Bytes())); err != nil { s.t.Fatal(err) }
		verify(dest)
		denied(dest)
		var redump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, raw, &redump); err != nil { s.t.Fatal(err) }
		if err := stuntgres.Reset(s.p.Ctx, raw); err != nil { s.t.Fatal(err) }
		if err := stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(redump.Bytes())); err != nil { s.t.Fatal(err) }
		verify(dest)
		var repeated bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, raw, &repeated); err != nil { s.t.Fatal(err) }
		if !bytes.Equal(redump.Bytes(), repeated.Bytes()) { s.t.Fatal("restore/redump changed canonical stream") }
		dest.create(928310, "5a00")
		prefix := saved.String()+"\nSELECT pg_catalog.lo_put(928308::pg_catalog.oid,8192::pg_catalog.int8,pg_catalog.decode('aabb','hex'));\n"
		for _, invalid := range []string{
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928307::pg_catalog.oid,131072::pg_catalog.int4),0::pg_catalog.int8) WHERE true;",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928307::pg_catalog.oid,262144::pg_catalog.int4),0::pg_catalog.int8);",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928307::pg_catalog.oid,131072::pg_catalog.int4),1::pg_catalog.int8);",
			"SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(928307::pg_catalog.oid,131072::pg_catalog.int4),4398046509057::pg_catalog.int8);",
		} {
			if err := stuntgres.Restore(s.p.Ctx, raw, strings.NewReader(prefix+invalid)); err == nil { s.t.Fatal("widened nested replay succeeded", invalid) }
			verify(dest)
			dest.bytes(928310, 0, 4, "5a00")
			dest.pages(928310, []any{int64(0), int64(2), "5a00"})
			dest.run(fixture.Select("SELECT oid::bigint,lomowner=(SELECT oid FROM pg_roles WHERE rolname='postgres') AS owned,lomacl FROM pg_largeobject_metadata WHERE oid=928310::oid",
				[]string{"oid", "owned", "lomacl"}, []string{"INT8", "BOOL", "_ACLITEM"}, [][]any{{int64(928310), true, nil}}))
			denied(dest) // Failed replay did not leak object authority.
		}
		verify(s) // Destination publication never changed the source engine.
	})
}
