package reference_test

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// REL_18_0 execMain.c: ExecPartitionCheckEmitError uses the named target's
// modified-column bitmap; ExecBuildSlotValueDescription includes supplied or
// SELECT-authorized columns, in physical order. These are independent initial
// expectations, to be checked against the pinned oracle before production edits.
func TestReferenceRangePartitionConstraintPrivacy(t *testing.T) {
	roleReferenceCleanup(t, []string{"rp_constraint_actor"})
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		for _, query := range []string{
			"CREATE USER rp_constraint_actor",
			"CREATE TABLE rp_constraint_root(k INT,payload TEXT DEFAULT 'private-value',readable INT DEFAULT 99) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_constraint_leaf PARTITION OF rp_constraint_root FOR VALUES FROM(1) TO(3)",
			"CREATE TABLE rp_constraint_mid PARTITION OF rp_constraint_root FOR VALUES FROM(3) TO(5) PARTITION BY RANGE(readable)",
			"CREATE TABLE rp_constraint_deep PARTITION OF rp_constraint_mid FOR VALUES FROM(10) TO(20)",
			"INSERT INTO rp_constraint_root VALUES(1,'sentinel',11),(3,'sentinel',11)",
		} {
			count := int64(0)
			if query == "INSERT INTO rp_constraint_root VALUES(1,'sentinel',11),(3,'sentinel',11)" {
				count = 2
			}
			run(referenceExec(query, count))
		}
		fail := func(target, detail string, supplied bool) {
			for _, prepared := range []bool{false, true} {
				query := "INSERT INTO " + target + "(k) VALUES(9)"
				var args []any
				if prepared {
					query = "INSERT INTO " + target + "(k) VALUES($1)"
					args = []any{int64(9)}
				}
				if supplied {
					// Assignment order is deliberately not disclosure order.
					query = "INSERT INTO " + target + "(payload,k) VALUES('supplied-value',9)"
					if prepared {
						query = "INSERT INTO " + target + "(payload,k) VALUES($1,$2)"
						args = []any{"supplied-value", int64(9)}
					}
				}
				s := referenceRangeError(query, "23514", `new row for relation "`+target+`" violates partition constraint`, referenceDiagnostic{Schema: "public", Table: target, Detail: detail})
				s.prepared, s.args = prepared, args
				run(s)
			}
		}
		for _, target := range []string{"rp_constraint_leaf", "rp_constraint_mid"} {
			// Shared owner path also proves that the virtual target's ancestor
			// failure wins over the readable=99 descendant-routing miss.
			fail(target, "Failing row contains (9, private-value, 99).", false)
		}
		if b.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO rp_constraint_actor", 0))
		}
		run(referenceExec("SET SESSION AUTHORIZATION rp_constraint_actor", 0))
		for _, target := range []string{"rp_constraint_leaf", "rp_constraint_mid"} {
			for _, prepared := range []bool{false, true} {
				s := referenceRangeError("INSERT INTO "+target+"(k) VALUES(9)", "42501", "permission denied for table "+target, referenceDiagnostic{})
				s.prepared = prepared
				run(s)
			}
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Supported table/column GRANT and plain REVOKE exercise the same
		// independently expected disclosure manifests on both backends.
		// Companion engine units retain private ACL/frontier coverage.
		for _, target := range []string{"rp_constraint_leaf", "rp_constraint_mid"} {
			run(referenceExec("GRANT INSERT ON "+target+" TO rp_constraint_actor", 0))
			for _, mode := range []struct {
				grant  string
				detail string
			}{
				{"", "Failing row contains (k) = (9)."},
				{"SELECT(readable)", "Failing row contains (k, readable) = (9, 99)."},
				{"SELECT(k,payload,readable)", "Failing row contains (k, payload, readable) = (9, private-value, 99)."},
				{"SELECT", "Failing row contains (9, private-value, 99)."},
			} {
				if mode.grant != "" {
					run(referenceExec("GRANT "+mode.grant+" ON "+target+" TO rp_constraint_actor", 0))
				}
				run(referenceExec("SET SESSION AUTHORIZATION rp_constraint_actor", 0))
				fail(target, mode.detail, false)
				if mode.grant == "" {
					fail(target, "Failing row contains (k, payload) = (9, supplied-value).", true)
				}
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				if mode.grant != "" {
					run(referenceExec("REVOKE "+mode.grant+" ON "+target+" FROM rp_constraint_actor", 0))
				}
			}
		}
		for _, target := range []string{"rp_constraint_root", "rp_constraint_leaf", "rp_constraint_mid", "rp_constraint_deep"} {
			rows := [][]any{{int64(1), "sentinel", int64(11)}, {int64(3), "sentinel", int64(11)}}
			if target == "rp_constraint_leaf" {
				rows = rows[:1]
			} else if target != "rp_constraint_root" {
				rows = rows[1:]
			}
			run(referenceSelect("SELECT * FROM "+target+" ORDER BY k", []string{"k", "payload", "readable"}, []string{"INT4", "TEXT", "INT4"}, rows))
		}
	})
}

// Physical constraints identify the failing leaf, but disclose using the
// original named INSERT target. Supported table/column GRANT is shared;
// TestRangePartitionPhysicalConstraintPrivacy supplements private ACL manifests.
func TestReferenceRangePartitionPhysicalConstraintPrivacy(t *testing.T) {
	roleReferenceCleanup(t, []string{"rp_physical_actor"})
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		for _, query := range []string{
			"CREATE USER rp_physical_actor",
			"CREATE TABLE rp_physical_root(k INT,required INT,secret TEXT DEFAULT 'private-value') PARTITION BY RANGE(k)",
			"CREATE TABLE rp_physical_nn PARTITION OF rp_physical_root(required NOT NULL) FOR VALUES FROM(1) TO(3)",
			"CREATE TABLE rp_physical_mid PARTITION OF rp_physical_root FOR VALUES FROM(3) TO(7) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_physical_deep_nn PARTITION OF rp_physical_mid(required NOT NULL) FOR VALUES FROM(3) TO(5)",
			"CREATE TABLE rp_physical_check PARTITION OF rp_physical_mid(CONSTRAINT rp_physical_key CHECK(k<>6)) FOR VALUES FROM(5) TO(7)",
			"CREATE TABLE rp_physical_ordinary(k INT,required INT NOT NULL,secret TEXT DEFAULT 'private-value')",
		} {
			run(referenceExec(query, 0))
		}
		run(referenceExec("INSERT INTO rp_physical_root VALUES(1,7,'sentinel'),(3,7,'sentinel'),(5,7,'sentinel')", 3))
		run(referenceExec("INSERT INTO rp_physical_ordinary VALUES(0,7,'sentinel')", 1))
		unchanged := func(t *testing.T) {
			for _, table := range []string{"rp_physical_root", "rp_physical_nn", "rp_physical_mid", "rp_physical_deep_nn", "rp_physical_check", "rp_physical_ordinary"} {
				rows := [][]any{{int64(1), int64(7), "sentinel"}, {int64(3), int64(7), "sentinel"}, {int64(5), int64(7), "sentinel"}}
				switch table {
				case "rp_physical_nn":
					rows = rows[:1]
				case "rp_physical_mid":
					rows = rows[1:]
				case "rp_physical_deep_nn":
					rows = rows[1:2]
				case "rp_physical_check":
					rows = rows[2:]
				case "rp_physical_ordinary":
					rows = [][]any{{int64(0), int64(7), "sentinel"}}
				}
				referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SELECT * FROM "+table+" ORDER BY k", []string{"k", "required", "secret"}, []string{"INT4", "INT4", "TEXT"}, rows))
			}
		}
		cases := []struct {
			name, target, leaf string
			check              bool
			bad, good          int64
			selectSource       bool
		}{
			{"root_nn", "rp_physical_root", "rp_physical_nn", false, 1, 5, false},
			{"mid_nn", "rp_physical_mid", "rp_physical_deep_nn", false, 3, 5, false},
			{"leaf_nn", "rp_physical_nn", "rp_physical_nn", false, 1, 2, false},
			{"ordinary_nn", "rp_physical_ordinary", "rp_physical_ordinary", false, 1, 2, false},
			{"root_check", "rp_physical_root", "rp_physical_check", true, 6, 1, false},
			{"mid_check", "rp_physical_mid", "rp_physical_check", true, 6, 3, false},
			{"leaf_check", "rp_physical_check", "rp_physical_check", true, 6, 5, false},
		}
		for _, c := range cases {
			c.name += "_select"
			c.selectSource = true
			cases = append(cases, c)
		}
		for _, c := range cases {
			if !t.Run(c.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
				fail := func(mode string) {
					for _, prepared := range []bool{false, true} {
						for _, batch := range []bool{false, true} {
							if c.selectSource && batch {
								continue
							}
							columns := "k"
							values := strconv.FormatInt(c.bad, 10)
							args := []any{c.bad}
							if prepared {
								values = "$1"
							}
							required, secret := "null", "private-value"
							names := "k"
							visibleValues := strconv.FormatInt(c.bad, 10)
							if batch {
								// The root/mid first row routes to another leaf. A
								// late physical failure must publish neither row.
								columns = "required,k"
								requiredSQL := "NULL"
								var requiredArg any
								if c.check {
									required, requiredSQL, requiredArg = "7", "7", int64(7)
								}
								values = "7," + strconv.FormatInt(c.good, 10) + "),(" + requiredSQL + "," + strconv.FormatInt(c.bad, 10)
								args = []any{int64(7), c.good, requiredArg, c.bad}
								if prepared {
									values = "$1,$2),($3,$4"
								}
								names, visibleValues = "k, required", strconv.FormatInt(c.bad, 10)+", "+required
							}
							if mode == "supplied" {
								secret = "supplied-value"
								columns = "secret," + columns
								if batch {
									requiredSQL := "NULL"
									var requiredArg any
									if c.check {
										requiredSQL, requiredArg = "7", int64(7)
									}
									values = "'supplied-value',7," + strconv.FormatInt(c.good, 10) + "),('supplied-value'," + requiredSQL + "," + strconv.FormatInt(c.bad, 10)
									args = []any{secret, int64(7), c.good, secret, requiredArg, c.bad}
									if prepared {
										values = "$1,$2,$3),($4,$5,$6"
									}
								} else {
									values = "'supplied-value'," + strconv.FormatInt(c.bad, 10)
									args = []any{secret, c.bad}
									if prepared {
										values = "$1,$2"
									}
								}
							}
							if mode == "supplied" || mode == "column_select" {
								names += ", secret"
								visibleValues += ", " + secret
							}
							detail := "Failing row contains (" + names + ") = (" + visibleValues + ")."
							if mode == "owner" || mode == "table_select" {
								detail = "Failing row contains (" + strconv.FormatInt(c.bad, 10) + ", " + required + ", " + secret + ")."
							}
							fields := referenceDiagnostic{Schema: "public", Table: c.leaf, Detail: detail, Column: "required"}
							code, message := "23502", `null value in column "required" of relation "`+c.leaf+`" violates not-null constraint`
							if c.check {
								fields.Column, fields.Constraint = "", "rp_physical_key"
								code, message = "23514", `new row for relation "`+c.leaf+`" violates check constraint "rp_physical_key"`
							}
							query := "INSERT INTO " + c.target + "(" + columns + ") VALUES(" + values + ")"
							if c.selectSource {
								query = "INSERT INTO " + c.target + "(" + columns + ") SELECT " + values
							}
							s := referenceRangeError(query, code, message, fields)
							s.prepared = prepared
							if prepared {
								s.args = args
							}
							referenceRunStep(t, p.ctx, conn, b.recorder, s)
						}
					}
				}
				fail("owner")
				unchanged(t)
				if b.pg {
					run(referenceExec("GRANT USAGE ON SCHEMA public TO rp_physical_actor", 0))
				}
				run(referenceExec("GRANT INSERT ON "+c.target+" TO rp_physical_actor", 0))
				for _, mode := range []string{"insert_only", "leaf_select", "column_select", "table_select", "supplied"} {
					if mode == "leaf_select" && c.target == c.leaf {
						continue
					}
					switch mode {
					case "leaf_select":
						run(referenceExec("GRANT SELECT ON "+c.leaf+" TO rp_physical_actor", 0))
					case "column_select":
						run(referenceExec("GRANT SELECT(secret) ON "+c.target+" TO rp_physical_actor", 0))
					case "table_select":
						run(referenceExec("GRANT SELECT ON "+c.target+" TO rp_physical_actor", 0))
					}
					run(referenceExec("SET SESSION AUTHORIZATION rp_physical_actor", 0))
					fail(mode)
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					switch mode {
					case "leaf_select":
						run(referenceExec("REVOKE SELECT ON "+c.leaf+" FROM rp_physical_actor", 0))
					case "column_select":
						run(referenceExec("REVOKE SELECT(secret) ON "+c.target+" FROM rp_physical_actor", 0))
					case "table_select":
						run(referenceExec("REVOKE SELECT ON "+c.target+" FROM rp_physical_actor", 0))
					}
					unchanged(t)
				}
				run(referenceExec("REVOKE INSERT ON "+c.target+" FROM rp_physical_actor", 0))
			}) {
				return
			}
		}
	})
}
