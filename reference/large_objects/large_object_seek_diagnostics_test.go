package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectSeekDiagnostics(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928403)
		s.create(928403, "616263646566")
		// A guarded FROM prevents SQL-function inlining. Context is a full
		// release-derived candidate for pinned-oracle discovery, not discarded.
		s.run(fixture.Exec("CREATE TABLE los_seek_guard(x integer)", 0))
		s.run(fixture.Exec("INSERT INTO los_seek_guard VALUES(1)", 1))
		s.run(fixture.Exec("CREATE FUNCTION los_seek_wrapped(integer) RETURNS bigint LANGUAGE SQL VOLATILE AS 'SELECT lo_lseek64($1,0::bigint,3) FROM los_seek_guard'", 0))
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928403, truncateWrite, 0)
		s.run(seek64(fd, 3, 0, 3))
		for _, api := range seekAPIs {
			for _, whence := range []int64{-1, 3, -2147483648, 2147483647} {
				s.recover(seekFailure(seekValue(api, fd, 0, whence, nil), "22023", fmt.Sprintf("invalid whence setting: %d", whence)))
				s.run(tell64(fd, 3))
			}
			for _, tc := range []struct{ offset int64; whence int; target int64 }{{-1,0,-1}, {-4,1,-1}, {-7,2,-1}} {
				s.recover(seekFailure(seekValue(api, fd, tc.offset, tc.whence, nil), "22023", fmt.Sprintf("invalid large object seek target: %d", tc.target)))
				s.run(tell64(fd, 3))
			}
			for _, bad := range []int64{-1, -2147483648, 2147483647} {
				for _, step := range []fixture.Step{seekValue(api, bad, -1, 3, nil), tellValue(api, bad, nil)} {
					s.recover(seekFailure(step, "42704", fmt.Sprintf("invalid large-object descriptor: %d", bad)))
				}
			}
			for _, args := range [][]any{{nil,-1,3}, {-1,nil,3}, {-1,-1,nil}, {nil,nil,nil}, {fd,nil,0}} {
				s.run(seekValue(api, args[0], args[1], args[2], nil))
				s.run(tell64(fd, 3))
			}
			s.run(tellValue(api, nil, nil))
		}
		for _, target := range []int64{truncateMaximum+1, -9223372036854775808, 9223372036854775807} {
			s.recover(seekFailure(seek64(fd, target, 0, 0), "22023", fmt.Sprintf("invalid large object seek target: %d", target)))
			s.run(tell64(fd, 3))
		}
		wrapped := fixture.ImplicitError("SELECT los_seek_wrapped($1::integer)", fixture.Diagnostic{Code:"22023", Message:"invalid whence setting: 3", Context:"SQL function \"los_seek_wrapped\" statement 1"}, fd)
		s.recover(wrapped)
		s.run(tell64(fd, 3))
		s.bytes(928403, 0, 20, "616263646566")
		s.pages(928403, []any{int64(0), int64(6), "616263646566"})

		// Addition inside inv_seek is not SQL argument arithmetic. These wrapped
		// targets are independently specified release candidates for the oracle.
		s.run(seek64(fd, 1, 0, 1))
		for _, tc := range []struct{ offset int64; whence int; target int64 }{
			{9223372036854775807,1,-9223372036854775808},
			{9223372036854775807,2,-9223372036854775803},
			{-9223372036854775808,1,-9223372036854775807},
		} {
			s.recover(seekFailure(seek64(fd, tc.offset, tc.whence, 0), "22023", fmt.Sprintf("invalid large object seek target: %d", tc.target)))
			s.run(tell64(fd, 1))
		}
		s.recover(truncateError("SELECT lo_lseek(0::integer,(2147483648::bigint)::integer,0::integer)", "22003", "integer out of range"))
		s.recover(truncateError("SELECT lo_lseek64(0::integer,(9223372036854775807::bigint+1),0::integer)", "22003", "bigint out of range"))
		s.run(tell64(fd, 1))
		s.run(fixture.Exec("ROLLBACK", 0))

		// Every API rejects closed, committed, rolled-back and locally unlinked
		// descriptors. Unlink rollback restores bytes but not the closed handle.
		for _, ending := range []string{"close", "COMMIT", "ROLLBACK", "unlink"} {
			s.run(fixture.Exec("BEGIN", 0))
			fd = s.open(928403, truncateWrite, 0)
			switch ending {
			case "close": s.run(truncateValue("lo_close($1::integer)", "INT4", int64(0), fd))
			case "unlink":
				s.run(fixture.Exec("SAVEPOINT los_unlink", 0))
				s.run(truncateValue("lo_unlink(928403::oid)", "INT4", int64(1)))
				s.run(fixture.Exec("ROLLBACK TO SAVEPOINT los_unlink", 0))
			default: s.run(fixture.Exec(ending, 0))
			}
			for _, api := range seekAPIs {
				for _, step := range []fixture.Step{seekValue(api, fd, 0, 0, nil), tellValue(api, fd, nil)} {
					failure := seekFailure(step, "42704", fmt.Sprintf("invalid large-object descriptor: %d", fd))
					if ending == "close" || ending == "unlink" { s.recover(failure) } else { s.run(failure) }
				}
			}
			if ending == "close" || ending == "unlink" { s.run(fixture.Exec("ROLLBACK", 0)) }
		}

		s.run(fixture.Exec("CREATE SEQUENCE los_null_steps START 41 CACHE 1", 0))
		s.run(fixture.Exec("CREATE TABLE los_runtime_null(n integer,z integer)", 0))
		s.run(fixture.Exec("INSERT INTO los_runtime_null VALUES(NULL,0)", 1))
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928403, truncateWrite, 0)
		s.run(seek64(fd, 3, 0, 3))
		const next = "nextval('los_null_steps'::regclass)"
		for _, api := range seekAPIs {
			for _, expr := range []string{
				api.seek+"(NULL::integer,"+next+"::"+api.offsetType+",0)",
				api.seek+"("+next+"::integer,NULL::"+api.offsetType+",0)",
				api.seek+"(0,"+next+"::"+api.offsetType+",NULL::integer)",
				api.tell+"((NULL::bigint+"+next+")::integer)",
			} {
				s.run(truncateValue(expr, api.resultType, nil))
				s.run(fixture.Select("SELECT last_value,is_called FROM los_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}}))
			}
			for _, expr := range []string{
				api.seek+"(NULL::integer,1/$1::"+api.offsetType+",0)",
				api.seek+"(1/$1::integer,NULL::"+api.offsetType+",0)",
				api.seek+"($1::integer,1/$2::"+api.offsetType+",0)",
			} {
				args := []any{0}
				if expr == api.seek+"($1::integer,1/$2::"+api.offsetType+",0)" { args = []any{nil,0} }
				s.recover(truncateError("SELECT "+expr, "22012", "division by zero", args...))
			}
			s.recover(truncateError("SELECT "+api.tell+"((NULL::integer+1/$1::integer))", "22012", "division by zero", 0))
			s.recover(truncateError("SELECT "+api.tell+"(($1::integer+1/$2::integer))", "22012", "division by zero", nil, 0))
		}
		s.run(truncateValue(next, "INT8", int64(41)))
		last := int64(41)
		for _, api := range seekAPIs {
			for _, expr := range []string{
				api.seek+"(n,"+next+"::"+api.offsetType+",0)",
				api.seek+"("+next+"::integer,n::"+api.offsetType+",0)",
				api.seek+"(0,"+next+"::"+api.offsetType+",n)",
				api.tell+"((n::bigint+"+next+")::integer)",
			} {
				last++
				s.run(fixture.Select("SELECT "+expr+" AS result FROM los_runtime_null", []string{"result"}, []string{api.resultType}, [][]any{{nil}}))
				s.run(truncateValue("currval('los_null_steps'::regclass)", "INT8", last))
			}
			for _, expr := range []string{
				api.seek+"(n,(1/z)::"+api.offsetType+",0)",
				api.seek+"(1/z,n::"+api.offsetType+",0)",
				api.seek+"(0,n::"+api.offsetType+",1/z)",
				api.tell+"(n+1/z)",
			} { s.recover(truncateError("SELECT "+expr+" FROM los_runtime_null", "22012", "division by zero")) }
		}
		s.run(tell64(fd, 3))
		s.bytes(928403, 0, 20, "616263646566")
		s.pages(928403, []any{int64(0), int64(6), "616263646566"})
		s.run(fixture.Exec("ROLLBACK", 0))
	})
}
