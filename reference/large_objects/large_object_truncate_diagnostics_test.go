package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncateDiagnostics(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928304)
		s.create(928304, "616263646566")
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			typ := "integer"
			lengths := []int64{-1, -2147483648}
			if name == "lo_truncate64" {
				typ = "bigint"
				lengths = append(lengths, -9223372036854775808, truncateMaximum+1, 9223372036854775807)
			}
			q := "SELECT "+name+"($1::integer,$2::"+typ+")"
			s.run(fixture.Exec("BEGIN", 0))
			r := s.open(928304, truncateRead, 0)
			w := s.open(928304, truncateWrite, 1)
			for _, d := range []int64{-1, -2147483648, 2147483647} {
				s.recover(truncateError(q, "42704", fmt.Sprintf("invalid large-object descriptor: %d", d), d, -1))
			}
			for _, n := range []int64{0, -1} {
				s.recover(truncateError(q, "55000", fmt.Sprintf("large object descriptor %d was not opened for writing", r), r, n))
			}
			for _, n := range lengths {
				s.recover(truncateError(q, "22023", fmt.Sprintf("invalid large object truncation target: %d", n), w, n))
				s.bytes(928304, 0, 20, "616263646566")
				s.pages(928304, []any{int64(0), int64(6), "616263646566"})
			}
			for _, args := range [][]any{{nil, -1}, {-1, nil}, {nil, nil}, {w, nil}, {r, nil}} {
				step := truncateCall(name, args[0], args[1])
				step.Rows = [][]any{{nil}}
				s.run(step)
			}
			s.bytes(928304, 0, 20, "616263646566")
			s.pages(928304, []any{int64(0), int64(6), "616263646566"})
			s.read(w, 2, "6162")
			s.run(truncateValue("lo_close($1::integer)", "INT4", int64(0), w))
			s.recover(truncateError(q, "42704", "invalid large-object descriptor: 1", w, 1))
			s.run(fixture.Exec("COMMIT", 0))
			s.run(truncateError(q, "42704", "invalid large-object descriptor: 0", r, 1))

			s.run(fixture.Exec("BEGIN READ ONLY", 0))
			r = s.open(928304, truncateRead, 0)
			for _, d := range []int64{r, -1} {
				s.recover(truncateError(q, "25006", "cannot execute "+name+"() in a read-only transaction", d, -1))
			}
			for _, args := range [][]any{{nil, -1}, {-1, nil}, {nil, nil}, {r, nil}} {
				step := truncateCall(name, args[0], args[1]); step.Rows = [][]any{{nil}}; s.run(step)
			}
			s.read(r, 6, "616263646566")
			s.run(fixture.Exec("ROLLBACK", 0))
			s.run(truncateError(q, "42704", "invalid large-object descriptor: 0", r, 1))

			for _, tc := range []struct { args, signature string }{
				{"", ""}, {"1::integer", "integer"}, {"true,1::integer", "boolean, integer"},
				{"1::integer,'1'::text", "integer, text"},
			} {
				step := fixture.ImplicitError("SELECT "+name+"("+tc.args+")", fixture.Diagnostic{
					Code:"42883", Message:"function "+name+"("+tc.signature+") does not exist",
					Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"8",
				})
				step.SelectRows = true
				s.run(step)
			}
		}
		// There is deliberately no lo_truncate(int4,int8) overload.
		bad := fixture.ImplicitError("SELECT lo_truncate(0::integer,2147483648::bigint)", fixture.Diagnostic{Code:"42883", Message:"function lo_truncate(integer, bigint) does not exist", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"8"})
		bad.SelectRows = true
		s.run(bad)
		s.run(truncateError("SELECT lo_truncate(0::integer,(2147483648::bigint)::integer)", "22003", "integer out of range"))
		s.run(truncateError("SELECT lo_truncate64(0::integer,(9223372036854775807::bigint+1))", "22003", "bigint out of range"))

		s.run(fixture.Exec("CREATE SEQUENCE lot_null_steps START 41 CACHE 1", 0))
		s.run(fixture.Exec("CREATE TABLE lot_runtime_null(n integer,z integer)", 0))
		s.run(fixture.Exec("INSERT INTO lot_runtime_null VALUES(NULL,0)", 1))
		s.run(fixture.Exec("BEGIN", 0))
		w := s.open(928304, truncateWrite, 0)
		const next = "nextval('lot_null_steps'::regclass)"
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			typ := "integer"
			if name == "lo_truncate64" { typ = "bigint" }
			for _, expr := range []string{
				name+"(NULL::integer,"+next+"::"+typ+")",
				name+"("+next+"::integer,NULL::"+typ+")",
				name+"((NULL::bigint+"+next+")::integer,-1::"+typ+")",
			} {
				s.run(truncateValue(expr, "INT4", nil))
				s.run(fixture.Select("SELECT last_value,is_called FROM lot_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}}))
				s.recover(truncateError("SELECT currval('lot_null_steps'::regclass)", "55000", "currval of sequence \"lot_null_steps\" is not yet defined in this session"))
			}
			// Child planning demand is not erased by the parent's constant NULL.
			s.recover(truncateError("SELECT "+name+"(NULL::integer,1/$1::"+typ+")", "22012", "division by zero", 0))
			s.recover(truncateError("SELECT "+name+"(1/$1::integer,NULL::"+typ+")", "22012", "division by zero", 0))
		}
		s.run(truncateValue(next, "INT8", int64(41)))
		i := int64(41)
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			typ := "integer"
			if name == "lo_truncate64" { typ = "bigint" }
			for _, expr := range []string{name+"(n,"+next+"::"+typ+")", name+"("+next+"::integer,n::"+typ+")"} {
				i++
				s.run(fixture.Select("SELECT "+expr+" AS result FROM lot_runtime_null", []string{"result"}, []string{"INT4"}, [][]any{{nil}}))
				s.run(fixture.Select("SELECT last_value,is_called FROM lot_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{i, true}}))
				s.run(truncateValue("currval('lot_null_steps'::regclass)", "INT8", i))
			}
			for _, expr := range []string{name+"(n,(1/z)::"+typ+")", name+"(1/z,n::"+typ+")"} {
				s.recover(truncateError("SELECT "+expr+" FROM lot_runtime_null", "22012", "division by zero"))
			}
		}
		s.bytes(928304, 0, 20, "616263646566")
		s.pages(928304, []any{int64(0), int64(6), "616263646566"})
		s.read(w, 6, "616263646566")
		s.run(fixture.Exec("ROLLBACK", 0))
	})
}
