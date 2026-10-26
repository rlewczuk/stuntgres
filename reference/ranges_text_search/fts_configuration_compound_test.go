package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// This ordinary SQL oracle is independent of native control success and of the
// native emitted stream consumed by the stronger collision scenario.
func TestReferenceFTSConfigurationCompoundValues(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "config_controls_schema")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA config_controls_schema", 0))
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION config_controls_schema.config_restored (COPY=pg_catalog.simple)", 0))
		for _, query := range ftsConfigurationCompoundSetup() {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec(ftsConfigurationCompoundInsert(), 1))
		for _, step := range ftsConfigurationCompoundChecks() {
			run(step)
			run(fixture.Prepared(step))
		}
		query, diagnostic := ftsConfigurationCompoundMalformed()
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic(query, diagnostic)
			step.Prepared, step.ErrorStage = prepared, "execute"
			run(step)
			for _, unchanged := range ftsConfigurationCompoundChecks() {
				run(unchanged)
			}
		}
	})
}

// The same actual configuration is present at every leaf. Inclusive equal range
// endpoints avoid allocator-order assumptions while exercising both bound walkers.
func ftsConfigurationCompoundSetup() []string {
	return []string{
		"CREATE DOMAIN public.config_controls_domain AS regconfig",
		"CREATE TYPE public.config_controls_pair AS (v regconfig,d public.config_controls_domain)",
		"CREATE TYPE public.config_controls_span AS RANGE(SUBTYPE=regconfig,SUBTYPE_OPCLASS=pg_catalog.oid_ops)",
		"CREATE TABLE public.config_controls_compound(a regconfig[],d public.config_controls_domain,c public.config_controls_pair,r public.config_controls_span)",
	}
}

func ftsConfigurationCompoundInsert() string {
	const config = "'config_controls_schema.config_restored'::regconfig"
	return "INSERT INTO public.config_controls_compound VALUES(ARRAY[" + config + ",NULL::regconfig," + config + "]," + config + ",ROW(" + config + "," + config + ")::public.config_controls_pair,public.config_controls_span(" + config + "," + config + ",'[]'))"
}

func ftsConfigurationCompoundChecks() []fixture.Step {
	const name = "config_controls_schema.config_restored"
	const from = " FROM public.config_controls_compound"
	leaves := []string{"a[1]", "a[3]", "d::regconfig", "(c).v", "(c).d::regconfig", "lower(r)", "upper(r)"}
	steps := []fixture.Step{
		fixture.Select("SELECT cardinality(a) AS n,a[2] IS NULL AS missing,lower_inc(r) AS lo,upper_inc(r) AS hi,isempty(r) AS empty"+from,
			[]string{"n", "missing", "lo", "hi", "empty"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(3), true, true, true, false}}),
	}
	for _, leaf := range leaves {
		steps = append(steps,
			fixture.Select("SELECT ("+leaf+")::text AS name,("+leaf+")::oid='"+name+"'::regconfig::oid AS imported"+from,
				[]string{"name", "imported"}, []string{"TEXT", "BOOL"}, [][]any{{name, true}}),
			ftsConfigurationVector("SELECT to_tsvector("+leaf+",'The Fat Rats') AS normalized"+from, "'fat':2 'rats':3 'the':1"),
		)
	}
	return steps
}

func ftsConfigurationCompoundMalformed() (string, fixture.Diagnostic) {
	want := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
	want.Detail = `Unexpected "," character.`
	return "INSERT INTO public.config_controls_compound(a) VALUES ('{1,,2}'::text::regconfig[]);", want
}
