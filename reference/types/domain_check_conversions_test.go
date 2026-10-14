package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckConversionsNull(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				for _, q := range []string{
					"CREATE DOMAIN domain_s.positive AS integer CONSTRAINT positive_check CHECK (VALUE>0)",
					"CREATE DOMAIN domain_s.nonnull AS integer CONSTRAINT nonnull_check CHECK (VALUE IS NOT NULL)",
					"CREATE TABLE domain_s.values_subject (id integer,v domain_s.positive)",
					"CREATE TABLE domain_s.null_subject (id integer,v domain_s.nonnull)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO domain_s.values_subject VALUES (1,1),(2,NULL)", 2))
				run(fixture.Exec("INSERT INTO domain_s.null_subject VALUES (1,1)", 1))
				s := fixture.Select("SELECT v-1 AS base FROM domain_s.values_subject ORDER BY id", []string{"base"}, []string{"INT4"}, [][]any{{int64(0)}, {nil}})
				s.Prepared = prepared
				run(s)
				for _, tc := range []struct {
					q            string
					args         []any
					rows, portal bool
				}{
					{"SELECT (v-1)::domain_s.positive AS v FROM domain_s.values_subject WHERE id=1", nil, true, false},
					{"SELECT 0::domain_s.positive AS v FROM public.one", nil, true, false},
					{"SELECT '0'::domain_s.positive AS v FROM public.one", nil, true, false},
					{"SELECT domain_s.positive '0' AS v FROM public.one", nil, true, false},
					{"INSERT INTO domain_s.values_subject VALUES (3,0)", nil, false, false},
					{"INSERT INTO domain_s.values_subject VALUES (3,2),(4,0)", nil, false, false},
					{"UPDATE domain_s.values_subject SET v=v-1 WHERE id=1", nil, false, false},
					{"INSERT INTO domain_s.values_subject SELECT 3,v-1 FROM domain_s.values_subject WHERE id=1", nil, false, false},
					{"INSERT INTO domain_s.values_subject VALUES (3,$1)", []any{int64(0)}, false, true},
					{"INSERT INTO domain_s.values_subject VALUES (3,$1::integer)", []any{int64(0)}, false, false},
				} {
					t.Run(tc.q, func(t *testing.T) {
						s := domainCheckFailure(tc.q, "positive", "positive_check", tc.rows, prepared, tc.args...)
						if tc.portal {
							s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
						}
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
						fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,v FROM domain_s.values_subject ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}}))
					})
				}
				for _, tc := range []struct {
					q            string
					args         []any
					rows, portal bool
				}{
					{"SELECT NULL::domain_s.nonnull AS v FROM public.one", nil, true, false},
					{"SELECT $1::domain_s.nonnull AS v FROM public.one", []any{nil}, true, true},
					{"INSERT INTO domain_s.null_subject VALUES (2,NULL)", nil, false, false},
					{"INSERT INTO domain_s.null_subject VALUES (2,$1)", []any{nil}, false, true},
					{"INSERT INTO domain_s.null_subject VALUES (2,$1::integer)", []any{nil}, false, false},
					{"INSERT INTO domain_s.null_subject(id) VALUES (2)", nil, false, false},
					{"INSERT INTO domain_s.null_subject DEFAULT VALUES", nil, false, false},
				} {
					t.Run(tc.q, func(t *testing.T) {
						s := domainCheckFailure(tc.q, "nonnull", "nonnull_check", tc.rows, prepared, tc.args...)
						if tc.portal {
							s.Diagnostic.Context = "unnamed portal parameter $1"
						}
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
						fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,v FROM domain_s.null_subject ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
					})
				}
				s = fixture.Select("SELECT n.v AS v FROM public.one o LEFT JOIN domain_s.null_subject n ON n.id=99", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				s.Prepared = prepared
				run(s)
				s = fixture.Exec("INSERT INTO domain_s.null_subject VALUES (2,(SELECT v FROM domain_s.null_subject WHERE false))", 1)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,v FROM domain_s.null_subject ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}}))
				// Same-domain assignments preserve already-domain NULL, without recheck.
				s = fixture.Exec("UPDATE domain_s.null_subject SET v=v WHERE id=2", 1)
				s.Prepared = prepared
				run(s)
			})
		})
	}
}

func TestReferenceDomainCheckAncestors(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.parent AS integer CONSTRAINT z_parent CHECK (VALUE>0)",
			"CREATE DOMAIN domain_s.middle AS domain_s.parent",
			"CREATE DOMAIN domain_s.child AS domain_s.middle CONSTRAINT a_child CHECK (VALUE>10)",
			"CREATE DOMAIN domain_s.inner_value AS integer CHECK (VALUE>0)",
			"CREATE DOMAIN domain_s.outer_value AS integer CHECK ((VALUE+1)::domain_s.inner_value>VALUE AND VALUE=1)",
			"CREATE DOMAIN domain_s.short AS varchar(4) CHECK (VALUE='abcd')",
			"CREATE TABLE domain_s.short_values (v domain_s.short)",
			"CREATE DOMAIN domain_s.ordered AS integer CONSTRAINT a_positive CHECK (VALUE>0) CONSTRAINT z_divide CHECK (10/VALUE>0)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ value, constraint string }{{"0", "z_parent"}, {"5", "a_child"}} {
				t.Run(tc.value+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT "+tc.value+"::domain_s.child AS v FROM public.one", "child", tc.constraint, true, prepared))
				})
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT 11::domain_s.child AS v,NULL::domain_s.child AS n FROM public.one", []string{"v", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(11), nil}}),
				fixture.Select("SELECT 1::domain_s.outer_value AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT 'abcde'::domain_s.short AS v FROM public.one", []string{"v"}, []string{"VARCHAR"}, [][]any{{"abcd"}}),
			} {
				s.Prepared = prepared
				run(s)
			}
			t.Run(map[bool]string{false: "typmod_direct", true: "typmod_prepared"}[prepared], func(t *testing.T) {
				s := fixture.ErrorDiagnostic("INSERT INTO domain_s.short_values VALUES ('abcde')", fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(4)"))
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
			})
			t.Run(map[bool]string{false: "ordered_direct", true: "ordered_prepared"}[prepared], func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 0::domain_s.ordered AS v FROM public.one", "ordered", "a_positive", true, prepared))
			})
		}
		run(fixture.Select("SELECT v FROM domain_s.short_values", []string{"v"}, []string{"VARCHAR"}, [][]any{}))
	})
}
