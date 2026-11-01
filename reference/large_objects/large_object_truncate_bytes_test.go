package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncateBytes(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928301)
		s.create(928301, "616263646566")
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			s.t.Run(name, func(t *testing.T) {
				local := *s
				local.t = t
				s := &local
				s.run(fixture.Exec("BEGIN", 0))
				fd := s.open(928301, truncateWrite, 0)
				s.run(truncateCall(name, fd, 2))
				s.bytes(928301, 0, 20, "6162")
				s.bytes(928301, 2, 1, "")
				s.run(truncateCall(name, fd, 7))
				s.bytes(928301, 0, 20, "61620000000000")
				s.run(truncateCall(name, fd, 7))
				s.bytes(928301, 0, 20, "61620000000000")
				s.run(fixture.Exec("ROLLBACK", 0))

				// The write cursor is four, not the new EOF two.
				s.run(fixture.Exec("BEGIN", 0))
				fd = s.open(928301, truncateWrite, 0)
				s.read(fd, 4, "61626364")
				s.run(truncateCall(name, fd, 2))
				s.run(truncateValue("lowrite($1::integer,decode('5a','hex'))", "INT4", int64(1), fd))
				s.bytes(928301, 0, 20, "616200005a")
				s.read(fd, 1, "")
				s.run(fixture.Exec("ROLLBACK", 0))

				// Extension also leaves an existing read/write cursor alone.
				s.run(fixture.Exec("BEGIN", 0))
				fd = s.open(928301, truncateWrite, 0)
				s.read(fd, 4, "61626364")
				s.run(truncateCall(name, fd, 9))
				s.read(fd, 20, "6566000000")
				s.read(fd, 1, "")
				s.run(fixture.Exec("ROLLBACK", 0))

				// Rollback restores data, not the descriptor's current position.
				s.run(fixture.Exec("BEGIN", 0))
				fd = s.open(928301, truncateWrite, 0)
				s.run(fixture.Exec("SAVEPOINT before_shrink", 0))
				s.read(fd, 4, "61626364")
				s.run(truncateCall(name, fd, 2))
				s.read(fd, 2, "")
				s.run(fixture.Exec("ROLLBACK TO SAVEPOINT before_shrink", 0))
				s.bytes(928301, 0, 20, "616263646566")
				s.read(fd, 2, "6566")
				s.run(fixture.Exec("RELEASE SAVEPOINT before_shrink", 0))
				s.run(fixture.Exec("ROLLBACK", 0))

				for _, length := range []int{0, 1, 2047, 2048, 2049} {
					t.Run(fmt.Sprint(length), func(t *testing.T) {
						local := *s
						local.t = t
						s := &local
						s.run(fixture.Exec("BEGIN", 0))
						fd := s.open(928301, truncateWrite, 0)
						s.run(truncateValue("lowrite($1::integer,decode('00ff00ff','hex'))", "INT4", int64(4), fd))
						s.run(truncateCall(name, fd, length))
						prefix := "00ff00ff6566"
						if length < 6 { prefix = prefix[:length*2] } else { prefix += strings.Repeat("00", length-6) }
						s.bytes(928301, 0, length+1, prefix)
						s.bytes(928301, int64(length), 1, "")
						s.run(fixture.Exec("ROLLBACK", 0))
					})
				}
			})
		}
	})
}
