package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncateTransactions(t *testing.T) {
	truncateScenario(t, []string{"lot_truncate_actor"}, func(s *truncateSession) {
		defer s.cleanup(928305)
		peer := s.peer()
		defer peer.close()
		s.create(928305, "616263646566")
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			s.run(fixture.Exec("BEGIN", 0))
			frozen := s.open(928305, truncateRead, 0)
			w := s.open(928305, truncateWrite, 1)
			s.run(truncateCall(name, w, 2))
			s.bytes(928305, 0, 20, "6162")
			s.pages(928305, []any{int64(0), int64(2), "6162"})
			s.read(frozen, 20, "616263646566")
			s.read(frozen, 1, "")
			s.read(w, 20, "6162")
			s.read(w, 1, "")
			peer.bytes(928305, 0, 20, "616263646566")
			peer.pages(928305, []any{int64(0), int64(6), "616263646566"})
			s.run(fixture.Exec("ROLLBACK", 0))
			s.bytes(928305, 0, 20, "616263646566")
			s.pages(928305, []any{int64(0), int64(6), "616263646566"})
			s.run(truncateError("SELECT "+name+"($1::integer,1)", "42704", "invalid large-object descriptor: 1", w))

			// The truncate is reached before division by its zero result fails.
			s.run(fixture.Exec("BEGIN", 0))
			w = s.open(928305, truncateWrite, 0)
			s.recover(truncateError("SELECT 1/"+name+"($1::integer,2)", "22012", "division by zero", w))
			s.bytes(928305, 0, 20, "616263646566")
			s.pages(928305, []any{int64(0), int64(6), "616263646566"})
			s.read(w, 2, "6162")
			s.run(fixture.Exec("COMMIT", 0))
			s.run(truncateError("SELECT "+name+"($1::integer,1)", "42704", "invalid large-object descriptor: 0", w))
		}
		s.run(fixture.Exec("BEGIN", 0))
		w := s.open(928305, truncateWrite, 0)
		s.run(truncateCall("lo_truncate64", w, truncateMaximum))
		s.pages(928305, []any{int64(0), int64(6), "616263646566"}, []any{int64(2147483647), int64(0), ""})
		s.bytes(928305, truncateMaximum-2, 4, "0000")
		s.run(fixture.Exec("ROLLBACK", 0))
		s.bytes(928305, 6, 1, "")
		s.pages(928305, []any{int64(0), int64(6), "616263646566"})

		for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
			// Establish the transaction frontier before the peer's commit.
			s.run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
			s.run(truncateValue("1::integer", "INT4", int64(1)))
			frozen := s.open(928305, truncateRead, 0)
			live := s.open(928305, truncateWrite, 1)
			peer.run(fixture.Exec("BEGIN", 0))
			pw := peer.open(928305, truncateWrite, 0)
			peer.run(truncateCall("lo_truncate", pw, 2))
			s.bytes(928305, 0, 20, "616263646566")
			peer.run(fixture.Exec("COMMIT", 0))
			peer.bytes(928305, 0, 20, "6162")
			s.read(frozen, 20, "616263646566")
			s.read(live, 20, "6162")
			// A newly opened READ follows the SQL isolation frontier; WRITE
			// follows current committed bytes without forcing an RR conflict.
			fresh := s.open(928305, truncateRead, 2)
			want := "6162"
			if isolation == "REPEATABLE READ" { want = "616263646566" }
			s.read(fresh, 20, want)
			s.run(fixture.Exec("ROLLBACK", 0))
			peer.run(truncateValue("lo_put(928305::oid,0::bigint,decode('616263646566','hex'))", "VOID", ""))
		}
		// Own journal changes remain visible inside an established RR base.
		s.run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		s.run(truncateValue("1::integer", "INT4", int64(1)))
		w = s.open(928305, truncateWrite, 0)
		s.run(truncateCall("lo_truncate64", w, 2))
		r := s.open(928305, truncateRead, 1)
		s.read(r, 20, "6162")
		s.run(fixture.Exec("ROLLBACK", 0))

		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			s.run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 928305 TO lot_truncate_actor", 0))
			s.run(fixture.Exec("SET ROLE lot_truncate_actor", 0))
			s.run(fixture.Exec("BEGIN", 0))
			r := s.open(928305, truncateRead, 0)
			w := s.open(928305, truncateWrite, 1)
			step := truncateCall(name, w, 2)
			held, stmt := s.held(step.Query)
			func() {
				defer stmt.Close()
				peer.run(fixture.Exec("REVOKE SELECT,UPDATE ON LARGE OBJECT 928305 FROM lot_truncate_actor", 0))
				s.execute(held, step)
				s.recover(truncateError("SELECT lo_open(928305::oid,131072)", "42501", "permission denied for large object 928305"))
				s.recover(truncateError("SELECT "+name+"($1::integer,-1)", "55000", fmt.Sprintf("large object descriptor %d was not opened for writing", r), r))
				s.read(r, 20, "616263646566")
				s.read(w, 20, "6162")
			}()
			s.run(fixture.Exec("COMMIT", 0))
			s.run(fixture.Exec("RESET ROLE", 0))
			peer.bytes(928305, 0, 20, "6162")
			peer.pages(928305, []any{int64(0), int64(2), "6162"})
			peer.run(truncateValue("lo_put(928305::oid,0::bigint,decode('616263646566','hex'))", "VOID", ""))
		}
	})
}
