package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorOperandDefaultBoundary(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
		// A call on either side is an ordinary operand; the generic tier remains
		// below multiplication even when neither operand is a literal.
		userOperatorValue(t, p, b, c, "SELECT abs(-2) #~# abs(-3)*4 AS v", "INT4", int64(14))
		userOperatorValue(t, p, b, c, "SELECT abs(-2) OPERATOR(public.#~#) abs(-3)*4 AS v", "INT4", int64(14))
		const direct = "CREATE TABLE userop_direct_default(v integer DEFAULT abs(-2))"
		if b.PG {
			userOperatorExec(t, p, b, c, direct)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_direct_default DEFAULT VALUES", 1))
			userOperatorValue(t, p, b, c, "SELECT v FROM userop_direct_default", "INT4", int64(2))
		} else {
			// This is the inherited native direct-selected-call DEFAULT boundary,
			// not a PostgreSQL limitation or permission to widen that consumer.
			userOperatorError(t, p, b, c, direct, "0A000", "functions are unsupported", "", "", strconv.Itoa(strings.Index(direct, "abs")+1), false)
			userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='userop_direct_default' AND relnamespace='public'::regnamespace", "INT8", int64(0))
		}
		const grouped = "CREATE TABLE userop_grouped_default(v integer DEFAULT (abs(-2)))"
		if b.PG {
			userOperatorExec(t, p, b, c, grouped)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_grouped_default DEFAULT VALUES", 1))
			userOperatorValue(t, p, b, c, "SELECT v FROM userop_grouped_default", "INT4", int64(2))
		} else {
			// Grouping admits the syntax, not an additional DEFAULT provider.
			userOperatorError(t, p, b, c, grouped, "0A000", "functions are unsupported", "", "", strconv.Itoa(strings.Index(grouped, "abs")+1), false)
			userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='userop_grouped_default' AND relnamespace='public'::regnamespace", "INT8", int64(0))
		}
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_operand_default(v integer DEFAULT (2 OPERATOR(public.#~#) 3))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_operand_default DEFAULT VALUES", 1))
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_operand_default", "INT4", int64(5))
	})
}
