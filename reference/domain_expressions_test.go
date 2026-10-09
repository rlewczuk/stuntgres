package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDomainExpressions(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.dj AS integer",
			"CREATE DOMAIN domain_s.dt AS text", "CREATE DOMAIN domain_s.db AS boolean",
			"CREATE TABLE domain_s.expr (id integer,i domain_s.di,s domain_s.dt,b domain_s.db)",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO domain_s.expr VALUES (1,7,' hi ',TRUE),(2,7,' hi ',FALSE),(3,NULL,NULL,NULL)", 3))
		steps := []referenceStep{
			referenceSelect("SELECT i,i+1 AS base,(i+1)::domain_s.di AS restored,btrim(s) AS trimmed,b AND TRUE AS truth FROM domain_s.expr ORDER BY id", []string{"i", "base", "restored", "trimmed", "truth"}, []string{"INT4", "INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(7), int64(8), int64(8), "hi", true}, {int64(7), int64(8), int64(8), "hi", false}, {nil, nil, nil, nil, nil}}),
			referenceSelect("SELECT id FROM domain_s.expr WHERE b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect("SELECT id FROM domain_s.expr WHERE i=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, int64(7)),
			referenceSelect("SELECT i FROM domain_s.expr GROUP BY i ORDER BY i NULLS LAST", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}),
			referenceSelect("SELECT i,count(*) AS n FROM domain_s.expr GROUP BY i ORDER BY i NULLS LAST", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(2)}, {nil, int64(1)}}),
			referenceSelect("SELECT count(i) AS n,bool_and(b) AS all_true,bool_or(b) AS any_true FROM domain_s.expr", []string{"n", "all_true", "any_true"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(2), false, true}}),
			referenceSelect("SELECT count(i) AS n,bool_and(b) AS all_true,bool_or(b) AS any_true FROM domain_s.expr WHERE id=3", []string{"n", "all_true", "any_true"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(0), nil, nil}}),
			referenceSelect("SELECT count(i) AS n,bool_and(b) AS all_true,bool_or(b) AS any_true FROM domain_s.expr WHERE id=99", []string{"n", "all_true", "any_true"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(0), nil, nil}}),
			referenceSelect("SELECT CASE WHEN b THEN i ELSE NULL::domain_s.di END AS same,CASE WHEN b THEN i ELSE 9 END AS mixed,coalesce(i,8::domain_s.di) AS same_coalesce,coalesce(i,8::domain_s.dj) AS other_coalesce FROM domain_s.expr ORDER BY id", []string{"same", "mixed", "same_coalesce", "other_coalesce"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(7), int64(7)}, {nil, int64(9), int64(7), int64(7)}, {nil, int64(9), int64(8), int64(8)}}),
			referenceSelect("VALUES (7::domain_s.di),(NULL::domain_s.di)", []string{"column1"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}),
			referenceSelect("VALUES (7::domain_s.di),(8::integer)", []string{"column1"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}),
			referenceSelect("SELECT i+1 AS base,(i+1)::domain_s.di AS restored FROM domain_s.expr WHERE id=99", []string{"base", "restored"}, []string{"INT4", "INT4"}, [][]any{}),
		}
		for _, s := range steps {
			run(s)
			run(referencePrepared(s))
		}
		// Explicit external-only identity controls: RowDescription flattens domains.
		// These controls do not introduce pg_typeof into the native SQL dialect.
		if backend.pg {
			identity := []referenceStep{
				referenceSelect("SELECT pg_typeof(i)::text AS bare,pg_typeof(i+1)::text AS base,pg_typeof((i+1)::domain_s.di)::text AS restored FROM domain_s.expr WHERE id=1", []string{"bare", "base", "restored"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"domain_s.di", "integer", "domain_s.di"}}),
				referenceSelect("SELECT pg_typeof(coalesce(NULL::domain_s.di,7::domain_s.di))::text AS same,pg_typeof(coalesce(NULL::domain_s.di,7::integer))::text AS mixed,pg_typeof(coalesce(NULL::domain_s.di,7::domain_s.dj))::text AS distinct", []string{"same", "mixed", "distinct"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"domain_s.di", "integer", "integer"}}),
				referenceSelect("SELECT pg_typeof(CASE WHEN TRUE THEN 7::domain_s.di ELSE NULL::domain_s.di END)::text AS same,pg_typeof(CASE WHEN TRUE THEN 7::domain_s.di ELSE 8 END)::text AS mixed", []string{"same", "mixed"}, []string{"TEXT", "TEXT"}, [][]any{{"domain_s.di", "integer"}}),
				referenceSelect("SELECT pg_typeof(v)::text AS same FROM (VALUES (7::domain_s.di),(NULL::domain_s.di)) AS x(v)", []string{"same"}, []string{"TEXT"}, [][]any{{"domain_s.di"}, {"domain_s.di"}}),
			}
			for _, s := range identity {
				run(s)
				run(referencePrepared(s))
			}
		}
	})
}
