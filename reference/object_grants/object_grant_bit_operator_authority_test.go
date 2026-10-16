package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 pg_operator.dat1791-1796 select pg_proc bitand1673,
// bitor1674, bitxor1675, bitnot1676, bitshiftleft1677 and bitshiftright1678.
// These are strict INTERNAL primitives, not SQL wrappers. VARBIT inputs select
// the BIT1560 declaration; shifts select an INT4 count, including SMALLINT
// coercion. The existing bit-string runtime is independent of integral bitwise.
func TestReferenceObjectGrantBitOperatorAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, op, signature, bits string
		integer                   int64
		other, otherBits          string
	}{
		{"bitand", "&", "bit,bit", "00001", 1, "f | g", "11101"},
		{"bitor", "|", "bit,bit", "11101", 29, "f & g", "00001"},
		{"bitxor", "#", "bit,bit", "11100", 28, "f & g", "00001"},
		{"bitnot", "~", "bit", "01110", -18, "f & g", "00001"},
		{"bitshiftleft", "<<", "bit,integer", "01000", 136, "f >> n", "00010"},
		{"bitshiftright", ">>", "bit,integer", "00010", 2, "f << n", "01000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + tc.name + "(" + tc.signature + ")"
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_bit_authority(f BIT(5),g BIT(5),v VARBIT,w VARBIT,n INTEGER,small_n SMALLINT,nf BIT(5),nv VARBIT,nn INTEGER)", 0))
				run(fixture.Exec("INSERT INTO og_bit_authority VALUES(B'10001',B'01101',B'10001',B'01101',3,3,NULL,NULL,NULL)", 1))
				run(fixture.Exec("GRANT SELECT ON og_bit_authority TO og_object_reader", 0))

				// Existing typed-NULL transport avoids unsupported explicit BIT casts.
				bitNull := "(B'' OPERATOR(pg_catalog.&) NULL)"
				varbitNull := "(B'' OPERATOR(pg_catalog.||) NULL)"
				right, varyingRight, nullRight := "g", "w", "nf"
				literalRight, integerRight, literalNullRight := "B'01101'", "13::INTEGER", bitNull
				if tc.op == "<<" || tc.op == ">>" {
					right, varyingRight, nullRight = "n", "small_n", "nn"
					literalRight, integerRight, literalNullRight = "3", "3::INTEGER", "NULL::INTEGER"
				}
				fixed := "f " + tc.op + " " + right
				varying := "v OPERATOR(pg_catalog." + tc.op + ") " + varyingRight
				nullFixed := "nf " + tc.op + " " + right
				nullVarying := "nv " + tc.op + " " + varyingRight
				constant := "B'10001' " + tc.op + " " + literalRight
				literalNull := bitNull + " " + tc.op + " " + literalRight
				literalVarNull := varbitNull + " " + tc.op + " " + literalRight
				integer := "17::INTEGER " + tc.op + " " + integerRight
				rowRightNull := "f " + tc.op + " " + nullRight
				literalRightNull := "B'10001' " + tc.op + " " + literalNullRight
				if tc.op == "~" {
					fixed, varying = "~f", "OPERATOR(pg_catalog.~) v"
					nullFixed, nullVarying = "~nf", "~nv"
					constant, literalNull, literalVarNull = "~B'10001'", "~"+bitNull, "~"+varbitNull
					integer = "~17::INTEGER"
				}
				query := "SELECT " + fixed + " AS fixed," + varying + " AS varying FROM og_bit_authority"
				nullQuery := "SELECT " + nullFixed + " AS fixed," + nullVarying + " AS varying FROM og_bit_authority"
				want := fixture.Select(query, []string{"fixed", "varying"}, []string{"BIT", "BIT"}, [][]any{{tc.bits, tc.bits}})
				nullWant := fixture.Select(nullQuery, []string{"fixed", "varying"}, []string{"BIT", "BIT"}, [][]any{{nil, nil}})
				run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				nullStmt, err := c.PrepareContext(p.Ctx, nullQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer nullStmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				nullHeld := fixture.HeldStatement{Executor: c, Statement: nullStmt, SQL: nullQuery}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				denied := func(q string) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				phase := func(allowed bool) {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						if allowed {
							run(want)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
							run(nullWant)
							fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, nullWant)
							run(fixture.Select(query+" WHERE FALSE", []string{"fixed", "varying"}, []string{"BIT", "BIT"}, nil))
							run(fixture.Select("SELECT "+constant+" AS v", []string{"v"}, []string{"BIT"}, [][]any{{tc.bits}}))
						} else {
							run(denied(query))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, denied(query))
							// Row-owned NULL is not a planning-time strictness fact.
							run(denied(nullQuery))
							fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, denied(nullQuery))
							run(denied(query + " WHERE FALSE"))
							run(denied("SELECT " + constant + " AS v"))
						}
						if tc.op != "~" {
							q := "SELECT " + rowRightNull + " AS v FROM og_bit_authority"
							if allowed {
								run(fixture.Select(q, []string{"v"}, []string{"BIT"}, [][]any{{nil}}))
							} else {
								run(denied(q))
							}
							run(fixture.Select("SELECT "+literalRightNull+" AS v", []string{"v"}, []string{"BIT"}, [][]any{{nil}}))
						}
						// Strict literal NULL and a dead CASE arm do not invoke the
						// protected primitive, unlike either nonconstant NULL above.
						run(fixture.Select("SELECT "+literalNull+" AS v,"+literalVarNull+" AS w", []string{"v", "w"}, []string{"BIT", "BIT"}, [][]any{{nil, nil}}))
						run(fixture.Select("SELECT CASE WHEN FALSE THEN "+fixed+" ELSE B'"+tc.bits+"' END AS v FROM og_bit_authority", []string{"v"}, []string{"BIT"}, [][]any{{tc.bits}}))
						// Rights belong to the exact BIT primitive: the same token's
						// INTEGER overload and another BIT operation stay independent.
						run(fixture.Select("SELECT "+integer+" AS v", []string{"v"}, []string{"INT4"}, [][]any{{tc.integer}}))
						run(fixture.Select("SELECT "+tc.other+" AS v FROM og_bit_authority", []string{"v"}, []string{"BIT"}, [][]any{{tc.otherBits}}))
					})
				}
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				phase(false)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				phase(false)
			})
		})
	}
}
