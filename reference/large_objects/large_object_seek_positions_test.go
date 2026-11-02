package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

type seekAPI struct { seek, tell, offsetType, resultType string }

var seekAPIs = []seekAPI{
	{"lo_lseek", "lo_tell", "integer", "INT4"},
	{"lo_lseek64", "lo_tell64", "bigint", "INT8"},
}

func seekValue(api seekAPI, fd, offset any, whence any, want any) fixture.Step {
	return truncateValue(api.seek+"($1::integer,$2::"+api.offsetType+",$3::integer)", api.resultType, want, fd, offset, whence)
}

func tellValue(api seekAPI, fd, want any) fixture.Step {
	return truncateValue(api.tell+"($1::integer)", api.resultType, want, fd)
}

func seek64(fd, offset int64, whence int, want int64) fixture.Step {
	return seekValue(seekAPIs[1], fd, offset, whence, want)
}

func tell64(fd, want int64) fixture.Step { return tellValue(seekAPIs[1], fd, want) }

func seekFailure(step fixture.Step, code, message string) fixture.Step {
	return truncateError(step.Query, code, message, step.Args...)
}

func TestReferenceLargeObjectSeekPositions(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928401)
		s.create(928401, "616263646566")
		for _, api := range seekAPIs {
			for _, mode := range []int{truncateRead, truncateWrite, truncateRead|truncateWrite} {
				s.run(fixture.Exec("BEGIN", 0))
				fd := s.open(928401, mode, 0)
				other := s.open(928401, truncateRead, 1)
				s.run(tellValue(api, fd, int64(0)))
				s.run(seekValue(api, fd, 2, 0, int64(2)))
				s.run(seekValue(api, fd, 1, 1, int64(3)))
				s.run(seekValue(api, fd, -1, 2, int64(5)))
				s.read(fd, 1, "66")
				s.run(tellValue(api, fd, int64(6)))
				s.run(tellValue(api, other, int64(0)))
				s.run(seekValue(api, fd, 0, 2, int64(6)))
				s.run(seekValue(api, fd, -6, 2, int64(0)))
				s.run(seekValue(api, fd, 4, 1, int64(4)))
				s.run(seekValue(api, fd, -3, 1, int64(1)))
				s.run(fixture.Exec("ROLLBACK", 0))
			}
			s.run(fixture.Exec("BEGIN READ ONLY", 0))
			fd := s.open(928401, truncateRead, 0)
			s.run(seekValue(api, fd, 2, 0, int64(2)))
			s.run(tellValue(api, fd, int64(2)))
			s.run(seekValue(api, fd, -1, 1, int64(1)))
			s.run(seekValue(api, fd, -1, 2, int64(5)))
			s.run(fixture.Exec("ROLLBACK", 0))

			s.run(fixture.Exec("BEGIN", 0))
			fd = s.open(928401, truncateWrite, 0)
			s.run(seekValue(api, fd, 10, 0, int64(10)))
			s.bytes(928401, 0, 20, "616263646566")
			s.pages(928401, []any{int64(0), int64(6), "616263646566"})
			s.read(fd, 2, "")
			s.run(tellValue(api, fd, int64(10)))
			s.run(truncateValue("lowrite($1::integer,decode('00ff','hex'))", "INT4", int64(2), fd))
			s.run(tellValue(api, fd, int64(12)))
			s.bytes(928401, 0, 20, "6162636465660000000000ff")
			s.run(seekValue(api, fd, 0, 2, int64(12)))
			s.run(seekValue(api, fd, 20, 0, int64(20)))
			s.run(truncateValue("lowrite($1::integer,decode('','hex'))", "INT4", int64(0), fd))
			s.run(tellValue(api, fd, int64(20)))
			s.pages(928401, []any{int64(0), int64(12), "6162636465660000000000ff"})
			s.run(seekValue(api, fd, 0, 2, int64(12)))
			s.run(fixture.Exec("ROLLBACK", 0))
		}
	})
}
