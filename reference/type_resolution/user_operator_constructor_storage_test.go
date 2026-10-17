package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorComparisonConsumerBatching(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_batch")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TYPE public.userop_batch_pair AS (v integer)", "CREATE SCHEMA userop_batch",
			"CREATE OPERATOR userop_batch.= (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne)",
			"CREATE OPERATOR userop_batch.<> (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)",
			"SET search_path TO userop_batch,pg_catalog,public")
		for _, tc := range []struct {
			query string
			value any
		}{
			{"SELECT ROW(2)::public.userop_batch_pair IN (ROW(2)::public.userop_batch_pair,ROW(2)::public.userop_batch_pair) AS v", true},
			{"SELECT ROW(2)::public.userop_batch_pair NOT IN (ROW(2)::public.userop_batch_pair,ROW(2)::public.userop_batch_pair) AS v", false},
			{"SELECT ROW(NULL::int4)::public.userop_batch_pair IN (ROW(NULL::int4)::public.userop_batch_pair,ROW(NULL::int4)::public.userop_batch_pair) AS v", true},
			{"SELECT ROW(NULL::int4)::public.userop_batch_pair NOT IN (ROW(NULL::int4)::public.userop_batch_pair,ROW(NULL::int4)::public.userop_batch_pair) AS v", false},
		} {
			t.Run(tc.query, func(t *testing.T) { userOperatorValue(t, p, b, c, tc.query, "BOOL", tc.value) })
		}
		for _, tc := range []struct {
			query string
			value bool
		}{
			{"SELECT ROW(2) IN (ROW(2),ROW(2)) AS v", false},
			{"SELECT ROW(2) NOT IN (ROW(2),ROW(2)) AS v", true},
		} {
			t.Run(tc.query, func(t *testing.T) {
				if b.PG {
					userOperatorValue(t, p, b, c, tc.query, "BOOL", tc.value)
				} else {
					userOperatorError(t, p, b, c, tc.query, "0A000", "custom operators are unsupported in this comparison context", "", "", "15", true)
				}
			})
		}
		userOperatorExec(t, p, b, c, "SET search_path TO pg_catalog,public")
		for _, tc := range []struct {
			query string
			value any
		}{
			{"SELECT ROW(NULL::int4)::public.userop_batch_pair IN (ROW(NULL::int4)::public.userop_batch_pair,ROW(NULL::int4)::public.userop_batch_pair) AS v", true},
			{"SELECT ROW(NULL::int4)::public.userop_batch_pair NOT IN (ROW(NULL::int4)::public.userop_batch_pair,ROW(NULL::int4)::public.userop_batch_pair) AS v", false},
			{"SELECT ROW(NULL::int4) IN (ROW(NULL::int4),ROW(NULL::int4)) AS v", nil},
			{"SELECT ROW(NULL::int4) NOT IN (ROW(NULL::int4),ROW(NULL::int4)) AS v", nil},
		} {
			t.Run(tc.query, func(t *testing.T) { userOperatorValue(t, p, b, c, tc.query, "BOOL", tc.value) })
		}
	})
}

func TestReferenceUserOperatorComparisonConsumerStoredConstructors(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			userOperatorExec(t, p, b, c, "SET search_path TO pg_catalog,public",
				"CREATE TYPE public.userop_stored_pair AS (v integer)",
				"CREATE VIEW public.userop_stored_in AS SELECT ROW(2)::public.userop_stored_pair IN (ROW(2)::public.userop_stored_pair) AS v",
				"CREATE VIEW public.userop_stored_null AS SELECT ROW(NULL::int4)::public.userop_stored_pair IN (ROW(NULL::int4)::public.userop_stored_pair) AS v",
				"CREATE VIEW public.userop_stored_coerced AS SELECT ROW(5.9)::public.userop_stored_pair IN (ROW(6)::public.userop_stored_pair) AS v",
				"CREATE VIEW public.userop_stored_structural AS SELECT ROW(NULL::int4)::public.userop_stored_pair = ROW(NULL::int4)::public.userop_stored_pair AS v")
			check := func() {
				t.Helper()
				userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_stored_in", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_stored_null", "BOOL", nil)
				userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_stored_coerced", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_stored_structural", "BOOL", true)
			}
			check()
			if b.PG {
				if len(emitted) == 0 {
					t.Fatal("actual native emitted SQL is required")
				}
				userOperatorExec(t, p, b, c, "DROP VIEW public.userop_stored_in,public.userop_stored_null,public.userop_stored_coerced,public.userop_stored_structural", "DROP TYPE public.userop_stored_pair")
				if _, err = c.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("actual native emitted SQL replay", err)
				}
				check()
				return
			}
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}
