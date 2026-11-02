package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectSeekBounds(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928402)
		s.create(928402, "616263")
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928402, truncateWrite, 0)
		for _, position := range []int64{2047, 2048, 2049, 2147483646, 2147483647, 2147483648} {
			s.run(seek64(fd, position, 0, position))
			s.read(fd, 1, "")
			s.run(tell64(fd, position))
			s.pages(928402, []any{int64(0), int64(3), "616263"})
		}
		s.run(seek64(fd, 4294967296, 0, 4294967296))
		s.run(seek64(fd, 0, 2, 3))
		s.run(seek64(fd, 4294967296, 0, 4294967296))
		s.run(truncateValue("lowrite($1::integer,decode('666f6f','hex'))", "INT4", int64(3), fd))
		s.run(tell64(fd, 4294967299))
		s.run(seek64(fd, 0, 2, 4294967299))
		s.run(seek64(fd, 4294967294, 0, 4294967294))
		s.read(fd, 5, "0000666f6f")
		s.run(tell64(fd, 4294967299))
		s.pages(928402, []any{int64(0), int64(3), "616263"}, []any{int64(2097152), int64(3), "666f6f"})
		s.bytes(928402, 4294967294, 5, "0000666f6f")

		api := seekAPIs[0]
		s.run(seekValue(api, fd, 2147483647, 0, int64(2147483647)))
		// The descriptor predates the recovery savepoint. Result overflow is
		// after assignment; recovering the statement must not rewind its cursor.
		s.recover(seekFailure(seekValue(api, fd, 1, 1, nil), "22003", fmt.Sprintf("lo_lseek result out of range for large-object descriptor %d", fd)))
		s.run(tell64(fd, 2147483648))
		s.recover(seekFailure(tellValue(api, fd, nil), "22003", fmt.Sprintf("lo_tell result out of range for large-object descriptor %d", fd)))
		s.run(tell64(fd, 2147483648))
		s.run(seekValue(api, fd, -1, 1, int64(2147483647)))
		s.recover(seekFailure(seekValue(api, fd, 0, 2, nil), "22003", fmt.Sprintf("lo_lseek result out of range for large-object descriptor %d", fd)))
		s.run(tell64(fd, 4294967299))

		s.run(seek64(fd, truncateMaximum, 0, truncateMaximum))
		s.recover(truncateError("SELECT lowrite($1::integer,decode('5a','hex'))", "22023", "invalid large object write request size: 1", fd))
		s.run(tell64(fd, truncateMaximum))
		s.recover(seekFailure(seek64(fd, truncateMaximum+1, 0, 0), "22023", "invalid large object seek target: 4398046509057"))
		s.run(tell64(fd, truncateMaximum))
		s.pages(928402, []any{int64(0), int64(3), "616263"}, []any{int64(2097152), int64(3), "666f6f"})
		s.run(seek64(fd, truncateMaximum-1, 0, truncateMaximum-1))
		s.run(truncateValue("lowrite($1::integer,decode('5a','hex'))", "INT4", int64(1), fd))
		s.run(tell64(fd, truncateMaximum))
		s.run(seek64(fd, 0, 2, truncateMaximum))
		s.bytes(928402, truncateMaximum-2, 3, "005a")
		s.run(truncateValue("(SELECT count(*) FROM pg_largeobject WHERE loid=928402::oid)", "INT8", int64(3)))
		s.run(fixture.Select("SELECT pageno,octet_length(data) AS size,encode(substring(data FROM 2047 FOR 2),'hex') AS tail FROM pg_largeobject WHERE loid=928402::oid AND pageno=2147483646 ORDER BY pageno", []string{"pageno", "size", "tail"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(2147483646), int64(2048), "005a"}}))
		s.run(fixture.Exec("ROLLBACK", 0))
	})
}
