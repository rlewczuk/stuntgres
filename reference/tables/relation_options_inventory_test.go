package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

type relationOptionSpec struct {
	name, kind string
	min, max   int64
	toast      bool
}

// Literal documented inventory, independent of the native registry.
var relationOptionInventory = []relationOptionSpec{
	{"fillfactor", "integer", 10, 100, false},
	{"toast_tuple_target", "integer", 128, 8160, false},
	{"parallel_workers", "integer", 0, 1024, false},
	{"autovacuum_enabled", "boolean", 0, 0, true},
	{"vacuum_index_cleanup", "enum", 0, 0, true},
	{"vacuum_truncate", "boolean", 0, 0, true},
	{"autovacuum_vacuum_threshold", "integer", 0, 2147483647, true},
	{"autovacuum_vacuum_max_threshold", "integer", -1, 2147483647, true},
	{"autovacuum_vacuum_insert_threshold", "integer", -1, 2147483647, true},
	{"autovacuum_vacuum_scale_factor", "floating point", 0, 100, true},
	{"autovacuum_vacuum_insert_scale_factor", "floating point", 0, 100, true},
	{"autovacuum_analyze_threshold", "integer", 0, 2147483647, false},
	{"autovacuum_analyze_scale_factor", "floating point", 0, 100, false},
	{"autovacuum_vacuum_cost_delay", "floating point", 0, 100, true},
	{"autovacuum_vacuum_cost_limit", "integer", 1, 10000, true},
	{"autovacuum_freeze_min_age", "integer", 0, 1000000000, true},
	{"autovacuum_freeze_max_age", "integer", 100000, 2000000000, true},
	{"autovacuum_freeze_table_age", "integer", 0, 2000000000, true},
	{"autovacuum_multixact_freeze_min_age", "integer", 0, 1000000000, true},
	{"autovacuum_multixact_freeze_max_age", "integer", 10000, 2000000000, true},
	{"autovacuum_multixact_freeze_table_age", "integer", 0, 2000000000, true},
	{"log_autovacuum_min_duration", "integer", -1, 2147483647, true},
	{"vacuum_max_eager_freeze_failure_rate", "floating point", 0, 1, true},
	{"user_catalog_table", "boolean", 0, 0, false},
}

func TestReferenceRelationOptionsInventory(t *testing.T) {
	toastCount := 0
	for _, spec := range relationOptionInventory {
		if spec.toast {
			toastCount++
		}
	}
	if len(relationOptionInventory) != 24 || toastCount != 18 {
		t.Fatal("incorrect independent inventory")
	}
	for _, spec := range relationOptionInventory {
		for _, prefix := range []string{"", "toast."} {
			if prefix != "" && !spec.toast {
				continue
			}
			t.Run(prefix+spec.name, func(t *testing.T) {
				relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
					if prefix == "" {
						run(fixture.Exec("ALTER TABLE ro SET(toast.vacuum_truncate='OFF')", 0))
					} else {
						run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70)", 0))
					}
					name := prefix + spec.name
					var baseline []string
					check := func() {
						if prefix == "" {
							run(relationOptionsState("ro", baseline...))
							relationOptionsToastState(t, p, b, c, prepared, "ro", "vacuum_truncate=OFF")
						} else {
							run(relationOptionsState("ro", "fillfactor=70"))
							relationOptionsToastState(t, p, b, c, prepared, "ro", baseline...)
						}
					}
					set := func(value string) {
						run(fixture.Exec("ALTER TABLE ro SET("+name+"='"+value+"')", 0))
						baseline = []string{spec.name + "=" + value}
						check()
					}
					bad := func(value, message, detail string) {
						before := ""
						if !b.PG {
							before = relationOptionsDump(t, p)
						}
						run(relationOptionsError("ALTER TABLE ro SET("+name+"='"+value+"')", "22023", message, detail))
						check()
						if !b.PG && relationOptionsDump(t, p) != before {
							t.Fatal("invalid option mutated retained base/TOAST state")
						}
					}
					switch spec.kind {
					case "boolean":
						for _, value := range []string{"true", "false", "yes", "no", "on", "off", "1", "0"} {
							set(value)
						}
					case "enum":
						for _, value := range []string{"auto", "on", "off", "true", "false", "yes", "no", "1", "0", "AUTO", "ON", "OFF"} {
							set(value)
						}
					default:
						set(strconv.FormatInt(spec.min, 10))
						set(strconv.FormatInt(spec.max, 10))
						for _, value := range []int64{spec.min - 1, spec.max + 1} {
							text := strconv.FormatInt(value, 10)
							if spec.kind == "integer" && value > 2147483647 {
								bad(text, fmt.Sprintf("invalid value for integer option %q: %s", spec.name, text), "")
								continue
							}
							detail := fmt.Sprintf("Valid values are between \"%d\" and \"%d\".", spec.min, spec.max)
							if spec.kind == "floating point" {
								detail = fmt.Sprintf("Valid values are between \"%.6f\" and \"%.6f\".", float64(spec.min), float64(spec.max))
							}
							bad(text, fmt.Sprintf("value %s out of bounds for option %q", text, spec.name), detail)
						}
					}
					detail := ""
					if spec.kind == "enum" {
						detail = "Valid values are \"on\", \"off\", and \"auto\"."
					}
					bad("abc", fmt.Sprintf("invalid value for %s option %q: abc", spec.kind, spec.name), detail)
					run(fixture.Exec("ALTER TABLE ro RESET("+name+")", 0))
					baseline = nil
					check()
				})
			})
		}
	}
}
