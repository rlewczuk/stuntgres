package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 inv_truncate stores its endpoint even when it is an empty page.
func TestReferenceLargeObjectTruncateSparse(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928302, 928303)
		s.create(928302, "")
		s.create(928303, "616263")
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			s.run(fixture.Exec("BEGIN", 0))
			fd := s.open(928302, truncateWrite, 0)
			s.run(truncateCall(name, fd, 0))
			s.pages(928302, []any{int64(0), int64(0), ""})
			s.run(fixture.Exec("ROLLBACK", 0))

			s.run(fixture.Exec("BEGIN", 0))
			fd = s.open(928302, truncateWrite, 0)
			s.run(truncateCall(name, fd, 2048))
			s.pages(928302, []any{int64(1), int64(0), ""})
			s.run(truncateCall(name, fd, 4096))
			s.pages(928302, []any{int64(1), int64(0), ""}, []any{int64(2), int64(0), ""})
			s.run(truncateValue("lo_put(928302::oid,8192::bigint,decode('5a','hex'))", "VOID", ""))
			s.pages(928302, []any{int64(1), int64(0), ""}, []any{int64(2), int64(0), ""}, []any{int64(4), int64(1), "5a"})
			s.bytes(928302, 8190, 4, "00005a")
			s.run(fixture.Exec("ROLLBACK", 0))

			s.run(fixture.Exec("BEGIN", 0))
			fd = s.open(928303, truncateWrite, 0)
			s.run(truncateCall(name, fd, 2048))
			s.pages(928303, []any{int64(0), int64(3), "616263"}, []any{int64(1), int64(0), ""})
			s.run(truncateCall(name, fd, 2049))
			s.pages(928303, []any{int64(0), int64(3), "616263"}, []any{int64(1), int64(1), "00"})
			s.run(truncateCall(name, fd, 2047))
			s.pages(928303, []any{int64(0), int64(2047), "616263"+strings.Repeat("00", 2044)})
			s.run(fixture.Exec("ROLLBACK", 0))

			// An equal aligned EOF does not justify skipping the endpoint operation.
			s.run(fixture.Exec("BEGIN", 0))
			fd = s.open(928302, truncateWrite, 0)
			s.run(truncateValue("lowrite($1::integer,decode($2::text,'hex'))", "INT4", int64(2048), fd, strings.Repeat("ab", 2048)))
			s.pages(928302, []any{int64(0), int64(2048), strings.Repeat("ab", 2048)})
			s.run(truncateCall(name, fd, 2048))
			s.pages(928302, []any{int64(0), int64(2048), strings.Repeat("ab", 2048)}, []any{int64(1), int64(0), ""})
			s.run(fixture.Exec("ROLLBACK", 0))
		}
		for _, tc := range []struct { name string; length, page, size int64 }{
			{"lo_truncate", 2147483647, 1048575, 2047},
			{"lo_truncate64", 2147483647, 1048575, 2047},
			{"lo_truncate64", 2147483648, 1048576, 0},
			{"lo_truncate64", truncateMaximum, 2147483647, 0},
		} {
			s.run(fixture.Exec("BEGIN", 0))
			fd := s.open(928302, truncateWrite, 0)
			s.run(truncateCall(tc.name, fd, tc.length))
			s.pages(928302, []any{tc.page, tc.size, strings.Repeat("00", int(tc.size))})
			s.bytes(928302, tc.length-2, 4, "0000")
			s.bytes(928302, tc.length, 1, "")
			s.run(fixture.Exec("ROLLBACK", 0))
			s.pages(928302)
		}
	})
}
