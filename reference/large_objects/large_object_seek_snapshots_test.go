package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectSeekSnapshots(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(isolation, func(t *testing.T) {
			truncateScenario(t, nil, func(s *truncateSession) {
				defer s.cleanup(928404, 928405)
				s.create(928404, "616263646566")
				s.create(928405, "616263646566")
				peer := s.peer()
				defer peer.close()
				begin := func() {
					s.run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
					// Establish RR through ordinary SQL, not descriptor opening.
					s.run(truncateValue("1::integer", "INT4", int64(1)))
				}
				checkEnd := func(read, write, readSize, writeSize int64) {
					for _, api := range seekAPIs {
						s.run(seekValue(api, read, 0, 2, readSize))
						s.run(tellValue(api, read, readSize))
						s.run(seekValue(api, write, 0, 2, writeSize))
						s.run(tellValue(api, write, writeSize))
					}
				}
				begin()
				r := s.open(928404, truncateRead, 0)
				w := s.open(928404, truncateWrite, 1)
				s.run(seek64(w, 6, 0, 6))
				s.run(truncateValue("lowrite($1::integer,decode('6768','hex'))", "INT4", int64(2), w))
				checkEnd(r, w, 6, 8)
				s.run(truncateCall("lo_truncate64", w, 3))
				checkEnd(r, w, 6, 3)
				s.run(fixture.Exec("ROLLBACK", 0))

				begin()
				r = s.open(928404, truncateRead, 0)
				w = s.open(928404, truncateWrite, 1)
				peer.run(fixture.Exec("BEGIN", 0))
				pw := peer.open(928404, truncateWrite, 0)
				peer.run(seek64(pw, 6, 0, 6))
				peer.run(truncateValue("lowrite($1::integer,decode('6768','hex'))", "INT4", int64(2), pw))
				checkEnd(r, w, 6, 6)
				peer.run(fixture.Exec("COMMIT", 0))
				checkEnd(r, w, 6, 8)
				peer.run(fixture.Exec("BEGIN", 0))
				pw = peer.open(928404, truncateWrite, 0)
				peer.run(truncateCall("lo_truncate64", pw, 3))
				checkEnd(r, w, 6, 8)
				peer.run(fixture.Exec("COMMIT", 0))
				checkEnd(r, w, 6, 3)
				s.run(fixture.Exec("ROLLBACK", 0))

				// Peer unlink leaves existing handles intact. inv_getsize scans
				// pages: frozen READ sees six, current WRITE sees no pages/zero.
				begin()
				r = s.open(928405, truncateRead, 0)
				w = s.open(928405, truncateWrite, 1)
				peer.run(fixture.Exec("BEGIN", 0))
				peer.run(truncateValue("lo_unlink(928405::oid)", "INT4", int64(1)))
				checkEnd(r, w, 6, 6)
				peer.run(fixture.Exec("COMMIT", 0))
				checkEnd(r, w, 6, 0)
				for _, api := range seekAPIs {
					for _, fd := range []int64{r, w} {
						s.run(seekValue(api, fd, 2, 0, int64(2)))
						s.run(seekValue(api, fd, 1, 1, int64(3)))
						s.run(tellValue(api, fd, int64(3)))
					}
				}
				s.run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
