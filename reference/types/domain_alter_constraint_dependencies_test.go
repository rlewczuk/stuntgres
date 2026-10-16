package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintDependencies(t *testing.T) {
	for _, carrier := range []string{"grandchild", "second_attribute", "second_table", "inheritance", "partition", "materialized"} {
		t.Run(carrier, func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer", 0))
				run(fixture.Exec("CREATE TABLE domain_s.early(v domain_s.d)", 0))
				run(fixture.Exec("INSERT INTO domain_s.early VALUES(1)", 1))
				table, column, repair := "late", "v", "UPDATE domain_s.late SET v=2"
				switch carrier {
				case "grandchild":
					run(fixture.Exec("CREATE DOMAIN domain_s.child AS domain_s.d", 0))
					run(fixture.Exec("CREATE DOMAIN domain_s.grandchild AS domain_s.child", 0))
					run(fixture.Exec("CREATE TABLE domain_s.late(v domain_s.grandchild)", 0))
					run(fixture.Exec("INSERT INTO domain_s.late VALUES(-1)", 1))
				case "second_attribute":
					column, repair = "b", "UPDATE domain_s.late SET b=2"
					run(fixture.Exec("CREATE TABLE domain_s.late(a domain_s.d,b domain_s.d)", 0))
					run(fixture.Exec("INSERT INTO domain_s.late VALUES(1,-1)", 1))
				case "second_table":
					run(fixture.Exec("CREATE TABLE domain_s.late(v domain_s.d)", 0))
					run(fixture.Exec("INSERT INTO domain_s.late VALUES(-1)", 1))
				case "inheritance":
					run(fixture.Exec("CREATE TABLE domain_s.root(v domain_s.d)", 0))
					run(fixture.Exec("CREATE TABLE domain_s.late() INHERITS(domain_s.root)", 0))
					run(fixture.Exec("INSERT INTO domain_s.late VALUES(-1)", 1))
				case "partition":
					run(fixture.Exec("CREATE TABLE domain_s.root(k integer,v domain_s.d) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE domain_s.late PARTITION OF domain_s.root FOR VALUES FROM(0) TO(10)", 0))
					run(fixture.Exec("INSERT INTO domain_s.root VALUES(1,-1)", 1))
				case "materialized":
					run(fixture.Exec("CREATE TABLE domain_s.seed(v integer)", 0))
					run(fixture.Exec("INSERT INTO domain_s.seed VALUES(-1)", 1))
					run(fixture.Exec("CREATE MATERIALIZED VIEW domain_s.late AS SELECT v::domain_s.d AS v FROM domain_s.seed", 1))
					repair = "DROP MATERIALIZED VIEW domain_s.late"
				}
				var childID int64
				if carrier == "partition" {
					childID = domainAlterOID(t, p, c, "SELECT 'domain_s.late'::regclass::oid")
				}
				checkAttached := func(value int64) {
					if carrier != "partition" {
						return
					}
					if got := domainAlterOID(t, p, c, "SELECT 'domain_s.late'::regclass::oid"); got != childID {
						t.Fatalf("physical child identity %d -> %d", childID, got)
					}
					// Reading through the partition root proves the physical child
					// is attached; direct identity alone cannot establish that edge.
					run(fixture.Select("SELECT k,v FROM domain_s.root", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
					run(fixture.Select("SELECT k,v FROM domain_s.late", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
				}
				checkAttached(-1)
				add := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
				t.Run("no_partial_add", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure(add, table, column)) })
				run(domainAlterFlags("d", [][]any{}))
				run(fixture.Exec(add+" NOT VALID", 0))
				checkAttached(-1)
				t.Run("no_partial_flag", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", table, column))
				})
				run(domainAlterFlags("d", [][]any{{"positive", false}}))
				count := int64(1)
				if carrier == "materialized" {
					count = 0
				}
				// Native UPDATE excludes attached partition members, even when
				// named directly or with ONLY. Repair the same detached physical
				// relation, then restore its edge before historical certification.
				if carrier == "partition" {
					run(fixture.Exec("ALTER TABLE domain_s.root DETACH PARTITION domain_s.late", 0))
				}
				run(fixture.Exec(repair, count))
				if carrier == "partition" {
					run(fixture.Exec("ALTER TABLE domain_s.root ATTACH PARTITION domain_s.late FOR VALUES FROM(0) TO(10)", 0))
				}
				checkAttached(2)
				run(fixture.Exec("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", 0))
				run(domainAlterFlags("d", [][]any{{"positive", true}}))
			})
		})
	}
	t.Run("visible_versions_and_transient_use", func(t *testing.T) {
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.d AS integer",
				"CREATE TABLE domain_s.t(id integer,v domain_s.d,gone domain_s.d)",
				"CREATE VIEW domain_s.transient AS SELECT (-1)::domain_s.d AS v FROM public.one",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.t VALUES(1,-1,-1),(2,-2,-2)", 2))
			run(fixture.Exec("DELETE FROM domain_s.t WHERE id=1", 1))
			run(fixture.Exec("UPDATE domain_s.t SET v=2 WHERE id=2", 1))
			run(fixture.Exec("ALTER TABLE domain_s.t DROP COLUMN gone", 0))
			run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)", 0))
			run(fixture.Select("SELECT id,v FROM domain_s.t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
		})
	})
	t.Run("physical_uses_are_scanned_once", func(t *testing.T) {
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.d AS integer",
				"CREATE TABLE domain_s.root(v domain_s.d)",
				"CREATE TABLE domain_s.child() INHERITS(domain_s.root)",
				"CREATE TABLE domain_s.multi(a domain_s.d,b domain_s.d)",
				"CREATE TABLE domain_s.partitioned(k integer,v domain_s.d) PARTITION BY RANGE(k)",
				"CREATE TABLE domain_s.leaf PARTITION OF domain_s.partitioned FOR VALUES FROM(0) TO(10)",
				"CREATE SEQUENCE domain_s.scan_calls",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.child VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO domain_s.multi VALUES(1,2)", 1))
			run(fixture.Exec("INSERT INTO domain_s.partitioned VALUES(1,1)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW domain_s.mv AS SELECT 1::domain_s.d AS v FROM public.one", 1))
			run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT counted CHECK(nextval('domain_s.scan_calls')>0)", 0))
			run(fixture.Select("SELECT last_value,is_called FROM domain_s.scan_calls", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), true}}))
			run(fixture.Exec("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT counted", 0))
			run(fixture.Select("SELECT last_value,is_called FROM domain_s.scan_calls", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), true}}))
		})
	})
	t.Run("selected_provider_edge", func(t *testing.T) {
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.provider AS integer",
				"CREATE DOMAIN domain_s.other_provider AS integer",
				"CREATE DOMAIN domain_s.d AS integer CONSTRAINT keep CHECK(VALUE::domain_s.other_provider<10)",
				"CREATE TABLE domain_s.t(v domain_s.d)",
				"ALTER DOMAIN domain_s.d ADD CONSTRAINT remove_me CHECK(VALUE::domain_s.provider>0)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.t VALUES(1)", 1))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.provider because other objects depend on it")
			want.Detail, want.Hint = "constraint remove_me depends on type domain_s.provider", "Use DROP ... CASCADE to drop the dependent objects too."
			t.Run("provider_in_use", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("DROP DOMAIN domain_s.provider RESTRICT", want))
			})
			run(fixture.Exec("ALTER DOMAIN domain_s.d DROP CONSTRAINT remove_me", 0))
			run(fixture.Exec("DROP DOMAIN domain_s.provider RESTRICT", 0))
			run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			t.Run("other_provider_retained", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Error("DROP DOMAIN domain_s.other_provider RESTRICT", "2BP01"))
			})
			t.Run("other_check_retained", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 11::domain_s.d FROM public.one", "d", "keep", true, false))
			})
		})
	})
}

func TestReferenceDomainAlterConstraintContainers(t *testing.T) {
	for _, carrier := range []string{"array_empty", "array_null", "array_data", "derived_array", "composite", "nested_array", "domain_composite", "domain_array_composite", "table_row", "view_row", "partition", "materialized"} {
		t.Run(carrier, func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer", 0))
				columnType, datum, bad := "domain_s.d[]", "'{-1,NULL}'", "'{-2}'::domain_s.d[]"
				errorDomain := "d"
				switch carrier {
				case "derived_array":
					run(fixture.Exec("CREATE DOMAIN domain_s.child AS domain_s.d", 0))
					columnType, bad, errorDomain = "domain_s.child[]", "'{-2}'::domain_s.child[]", "child"
				case "composite", "nested_array", "domain_composite", "domain_array_composite":
					run(fixture.Exec("CREATE TYPE domain_s.pair AS(v domain_s.d)", 0))
					columnType, datum, bad = "domain_s.pair", "'(-1)'", "'(-2)'::domain_s.pair"
					if carrier == "nested_array" {
						columnType, datum, bad = "domain_s.pair[]", `' {"(-1)"}'`, `'{"(-2)"}'::domain_s.pair[]`
					}
					if carrier == "domain_composite" || carrier == "domain_array_composite" {
						base := "domain_s.pair"
						if carrier == "domain_array_composite" {
							base, datum = "domain_s.pair[]", `'{"(-1)"}'`
						}
						run(fixture.Exec("CREATE DOMAIN domain_s.wrapper AS "+base, 0))
						columnType = "domain_s.wrapper"
						bad = "'(-2)'::domain_s.wrapper"
						if carrier == "domain_array_composite" {
							bad = `'{"(-2)"}'::domain_s.wrapper`
						}
					}
				case "table_row", "view_row":
					run(fixture.Exec("CREATE TABLE domain_s.origin(v domain_s.d)", 0))
					columnType, datum, bad = "domain_s.origin", "'(-1)'", "'(-2)'::domain_s.origin"
					if carrier == "view_row" {
						run(fixture.Exec("CREATE VIEW domain_s.origin_view AS SELECT v FROM domain_s.origin", 0))
						columnType, bad = "domain_s.origin_view", "'(-2)'::domain_s.origin_view"
					}
				}
				table := "holder"
				switch carrier {
				case "partition":
					run(fixture.Exec("CREATE TABLE domain_s.holder(k integer,x "+columnType+") PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE domain_s.leaf PARTITION OF domain_s.holder FOR VALUES FROM(0) TO(10)", 0))
				case "materialized":
					run(fixture.Exec("CREATE MATERIALIZED VIEW domain_s.holder AS SELECT NULL::domain_s.d[] AS x FROM public.one", 1))
				default:
					run(fixture.Exec("CREATE TABLE domain_s.holder(x "+columnType+")", 0))
				}
				if carrier == "array_null" {
					run(fixture.Exec("INSERT INTO domain_s.holder VALUES(NULL)", 1))
				}
				if carrier == "array_data" || carrier == "composite" || carrier == "nested_array" || carrier == "domain_composite" || carrier == "domain_array_composite" || carrier == "table_row" || carrier == "view_row" {
					run(fixture.Exec("INSERT INTO domain_s.holder VALUES("+datum+")", 1))
				}
				add := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
				t.Run("validating_add", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure(add, errorDomain, table, "x"))
				})
				run(domainAlterFlags("d", [][]any{}))
				run(fixture.Exec(add+" NOT VALID", 0))
				t.Run("validate", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", errorDomain, table, "x"))
				})
				run(domainAlterFlags("d", [][]any{{"positive", false}}))
				t.Run("new_nested_input", func(t *testing.T) {
					s := domainCheckFailure("SELECT "+bad+" AS v FROM public.one", errorDomain, "positive", true, false)
					s.Diagnostic.Position = "8"
					fixture.RunStep(t, p.Ctx, c, r, s)
				})
			})
		})
	}
	t.Run("unstored_composite_is_not_a_blocker", func(t *testing.T) {
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer", "CREATE TYPE domain_s.unstored AS(v domain_s.d)", "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)", "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
			}
		})
	})
}
