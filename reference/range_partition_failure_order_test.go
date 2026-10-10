package reference_test

import "testing"

// REL_18_0 parse_utilcmd.c: transformPartitionBound preflights both arities;
// transformPartitionRangeBounds transforms/coerces/evaluates/NULL-checks each
// datum, then validates infinite suffixes, lower tuple before upper tuple.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_utilcmd.c
func TestReferenceRangePartitionFailureOrder(t *testing.T) {
	cases := []struct {
		name, lower, upper, code, message, token string
		drawn                                    bool
	}{
		{"lower_null_before_upper_column", "NULL,0", "k,0", "42P17", "cannot specify NULL in range bound", "", false},
		{"lower_draw_before_upper_column", "nextval('rp_order_seq'),0", "k,0", "0A000", "cannot use column reference in partition bound expression", "k,0", true},
		{"lower_draw_before_later_lower_column", "nextval('rp_order_seq'),k", "20,0", "0A000", "cannot use column reference in partition bound expression", "k) TO", true},
		{"from_arity_before_null_and_column", "NULL", "k,0", "42P16", "FROM must specify exactly one value per partitioning column", "", false},
		{"from_arity_before_draw", "nextval('rp_order_seq')", "k,0", "42P16", "FROM must specify exactly one value per partitioning column", "", false},
		{"to_arity_before_null", "NULL,0", "k", "42P16", "TO must specify exactly one value per partitioning column", "", false},
		{"to_arity_before_draw", "nextval('rp_order_seq'),0", "k", "42P16", "TO must specify exactly one value per partitioning column", "", false},
	}
	for _, sentinel := range []string{"MINVALUE", "MAXVALUE"} {
		for _, side := range []string{"lower", "upper"} {
			for _, finite := range []struct{ name, expression, code, message, token string }{
				{"column", "k", "0A000", "cannot use column reference in partition bound expression", "k)"},
				{"invalid_input", "'no'", "22P02", `invalid input syntax for type bigint: "no"`, "'no'"},
				{"null", "NULL", "42P17", "cannot specify NULL in range bound", ""},
				{"draw", "nextval('rp_order_seq')", "42804", "every bound following " + sentinel + " must also be " + sentinel, "nextval"},
			} {
				lower, upper := "0,0", "20,0"
				if side == "lower" {
					lower = sentinel + "," + finite.expression
					// A malformed upper must not mask the lower tuple's suffix error.
					upper = "k,0"
				} else {
					upper = sentinel + "," + finite.expression
				}
				cases = append(cases, struct {
					name, lower, upper, code, message, token string
					drawn                                    bool
				}{sentinel + "_" + side + "_" + finite.name, lower, upper, finite.code, finite.message, finite.token, finite.name == "draw"})
			}
		}
	}
	for _, c := range cases {
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(c.name+"/"+route.name, func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE SEQUENCE rp_order_seq START 10", 0))
					run(referenceExec("CREATE TABLE rp_order(k BIGINT,j BIGINT,payload INT) PARTITION BY RANGE(k,j)", 0))
					run(referenceExec("CREATE TABLE rp_order_def PARTITION OF rp_order DEFAULT", 0))
					run(referenceExec("INSERT INTO rp_order VALUES(30,0,7)", 1))
					q := "CREATE TABLE bad PARTITION OF rp_order(CONSTRAINT rp_order_bad_unique UNIQUE(payload),CONSTRAINT rp_order_bad_check CHECK(payload>0)) FOR VALUES FROM(" + c.lower + ") TO(" + c.upper + ")"
					fields := referenceDiagnostic{}
					if c.token != "" {
						fields.Position = referenceRangePosition(q, c.token)
					}
					step := referenceRangeError(q, c.code, c.message, fields)
					step.prepared = route.prepared
					run(step)
					// last_value alone cannot distinguish an untouched START 10
					// generator from one draw; is_called and the next draw do.
					run(referenceSelect("SELECT last_value,is_called FROM rp_order_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), c.drawn}}))
					next := int64(10)
					if c.drawn {
						next = 11
					}
					run(referenceSelect("SELECT nextval('rp_order_seq') AS next", []string{"next"}, []string{"INT8"}, [][]any{{next}}))
					run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='bad' OR relname='rp_order_bad_unique'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					for _, missing := range []struct{ sql, name, token string }{
						{"SELECT NULL::bad", "bad", "bad"},
						{`SELECT NULL::"_bad"`, "_bad", `"_bad"`},
					} {
						run(referenceImplicitError(missing.sql, referenceDiagnostic{Code: "42704", Message: `type "` + missing.name + `" does not exist`, Position: referenceRangePosition(missing.sql, missing.token)}))
					}
					run(referenceSelect("SELECT COUNT(*) FROM pg_constraint WHERE conname='rp_order_bad_unique' OR conname='rp_order_bad_check'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='rp_order' OR relname='rp_order_def' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"rp_order"}, {"rp_order_def"}}))
					run(referenceSelect("SELECT * FROM rp_order", []string{"k", "j", "payload"}, []string{"INT8", "INT8", "INT4"}, [][]any{{int64(30), int64(0), int64(7)}}))
					run(referenceExec("INSERT INTO rp_order VALUES(10,0,8)", 1))
					run(referenceSelect("SELECT * FROM rp_order_def ORDER BY k", []string{"k", "j", "payload"}, []string{"INT8", "INT8", "INT4"}, [][]any{{int64(10), int64(0), int64(8)}, {int64(30), int64(0), int64(7)}}))
				})
			})
		}
	}
}
