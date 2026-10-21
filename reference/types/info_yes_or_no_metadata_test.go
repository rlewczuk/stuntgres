package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoYesOrNoMetadata(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoYesOrNoSeed(t, p, conn, recorder)
		for i, s := range []fixture.Step{
			infoYesOrNoStored(backend.PG),
			infoYesOrNoRows(backend.PG, "SELECT v FROM public.info_yes_or_no_witness WHERE id=99", [][]any{}),
			infoYesOrNoRows(backend.PG, "SELECT v FROM public.info_yes_or_no_witness WHERE id=3", [][]any{{nil}}),
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("result=%d/prepared=%v", i, prepared), func(t *testing.T) {
					step := s
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, step)
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, conn, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					infoYesOrNoColumnTypes(t, columns, backend.PG)
					n := 0
					for rows.Next() {
						var value any
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
						if n >= len(s.Rows) {
							t.Fatal("extra row")
						}
						s.CheckRaw(t, 0, n, value)
						n++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if n != len(s.Rows) {
						t.Fatalf("rows=%d want %d", n, len(s.Rows))
					}
					infoYesOrNoColumnTypes(t, columns, backend.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					infoYesOrNoColumnTypes(t, columns, backend.PG)
				})
			}
		}
	})
}

func infoYesOrNoIdentitySteps() []fixture.Step {
	// Prefilter only existing WHERE predicates before the catalog joins, retaining
	// every identity relationship and the complete independently expected rows.
	return []fixture.Step{
		fixture.Select("SELECT 'information_schema.yes_or_no'::regtype::text AS domain", []string{"domain"}, []string{"TEXT"}, [][]any{{"information_schema.yes_or_no"}}),
		fixture.Select(`SELECT n.nspname AS namespace,t.typname AS name,t.typtype AS kind,t.typcategory AS category,b.oid::integer AS base_oid,bn.nspname AS base_schema,b.typname AS base_name,t.typtypmod AS typmod,t.typcollation::integer AS collation,c.collname AS collation_name,t.typnotnull AS required,t.typdefault AS default_value,t.typdefaultbin AS default_tree,t.typndims AS dimensions,t.typlen::integer AS physical_length,t.typbyval AS by_value,t.typalign AS alignment,t.typstorage AS storage,(t.oid>0 AND t.oid<>b.oid AND t.typarray>0 AND t.typarray<>t.oid AND a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS identity FROM (SELECT * FROM pg_catalog.pg_type WHERE typname='yes_or_no') t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_type b ON b.oid=t.typbasetype JOIN pg_catalog.pg_namespace bn ON bn.oid=b.typnamespace JOIN pg_catalog.pg_type a ON a.oid=t.typarray JOIN pg_catalog.pg_collation c ON c.oid=t.typcollation WHERE n.nspname='information_schema' AND t.typname='yes_or_no'`,
			[]string{"namespace", "name", "kind", "category", "base_oid", "base_schema", "base_name", "typmod", "collation", "collation_name", "required", "default_value", "default_tree", "dimensions", "physical_length", "by_value", "alignment", "storage", "identity"},
			[]string{"NAME", "NAME", "CHAR", "CHAR", "INT4", "NAME", "NAME", "INT4", "INT4", "NAME", "BOOL", "TEXT", "PG_NODE_TREE", "INT4", "INT4", "BOOL", "CHAR", "CHAR", "BOOL"},
			[][]any{{"information_schema", "yes_or_no", "d", "S", int64(1043), "pg_catalog", "varchar", int64(7), int64(950), "C", false, nil, nil, int64(0), int64(-1), false, "i", "x", true}}),
		fixture.Select(`SELECT (a.atttypid=t.oid) AS domain_identity,a.atttypmod AS typmod,a.attcollation::integer AS collation,a.attnotnull AS required FROM (SELECT * FROM pg_catalog.pg_attribute WHERE attname='v') a JOIN (SELECT * FROM pg_catalog.pg_class WHERE relname='info_yes_or_no_witness') r ON r.oid=a.attrelid JOIN pg_catalog.pg_namespace rn ON rn.oid=r.relnamespace JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace tn ON tn.oid=t.typnamespace WHERE rn.nspname='public' AND r.relname='info_yes_or_no_witness' AND a.attname='v' AND tn.nspname='information_schema' AND t.typname='yes_or_no'`,
			[]string{"domain_identity", "typmod", "collation", "required"}, []string{"BOOL", "INT4", "INT4", "BOOL"}, [][]any{{true, int64(-1), int64(950), false}}),
		fixture.Select(`SELECT c.conname AS name,c.contype AS kind,c.convalidated AS validated,(c.oid>0 AND c.contypid=t.oid AND c.conrelid=0 AND c.connamespace=t.typnamespace) AS identity FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_type t ON t.oid=c.contypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='information_schema' AND t.typname='yes_or_no'`,
			[]string{"name", "kind", "validated", "identity"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"yes_or_no_check", "c", true, true}}),
	}
}

func infoYesOrNoIdentity(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, s := range infoYesOrNoIdentitySteps() {
		infoYesOrNoBoth(t, p, conn, recorder, s)
	}
}

func TestReferenceInfoYesOrNoIdentity(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for i, s := range infoYesOrNoIdentitySteps() {
			t.Run(fmt.Sprintf("catalog=%d", i), func(t *testing.T) {
				infoYesOrNoBoth(t, p, conn, recorder, s)
			})
		}
		t.Run("ExistingProviderBoundary", func(t *testing.T) {
			identity := "a.atttypid=1043"
			typmod := int64(7)
			if backend.PG {
				identity = "a.atttypid='information_schema.yes_or_no'::regtype::oid"
				typmod = -1
			}
			infoYesOrNoBoth(t, p, conn, recorder, fixture.Select(`SELECT a.attname AS name,(`+identity+`) AS identity,a.atttypmod AS typmod,a.attcollation::integer AS collation,a.attnotnull AS required FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class r ON r.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname='information_schema' AND r.relname='table_privileges' AND (a.attname='is_grantable' OR a.attname='with_hierarchy') ORDER BY a.attname`, []string{"name", "identity", "typmod", "collation", "required"}, []string{"NAME", "BOOL", "INT4", "INT4", "BOOL"}, [][]any{{"is_grantable", true, typmod, int64(950), false}, {"with_hierarchy", true, typmod, int64(950), false}}))
			for _, name := range []string{"is_grantable", "with_hierarchy"} {
				infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoRows(backend.PG, "SELECT "+name+" AS v FROM information_schema.table_privileges WHERE table_schema='public' AND table_name='info_yes_or_no_witness' AND privilege_type='SELECT'", [][]any{{"YES"}}))
			}
		})
		// General information-schema columns/domains providers are not native scope.
		if !backend.PG {
			return
		}
		for i, s := range []fixture.Step{
			fixture.Select(`SELECT data_type::text,domain_schema::text,domain_name::text,udt_schema::text,udt_name::text,character_maximum_length::integer,character_octet_length::integer,is_nullable::text,collation_schema::text,collation_name::text,(collation_catalog=current_database()) AS local_collation FROM information_schema.columns WHERE table_schema='public' AND table_name='info_yes_or_no_witness' AND column_name='v'`, []string{"data_type", "domain_schema", "domain_name", "udt_schema", "udt_name", "character_maximum_length", "character_octet_length", "is_nullable", "collation_schema", "collation_name", "local_collation"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"character varying", "information_schema", "yes_or_no", "pg_catalog", "varchar", int64(3), int64(12), "YES", "pg_catalog", "C", true}}),
			fixture.Select(`SELECT domain_schema::text,domain_name::text,data_type::text,udt_schema::text,udt_name::text,character_maximum_length::integer,character_octet_length::integer,domain_default::text,collation_schema::text,collation_name::text,(collation_catalog=current_database()) AS local_collation FROM information_schema.domains WHERE domain_schema='information_schema' AND domain_name='yes_or_no'`, []string{"domain_schema", "domain_name", "data_type", "udt_schema", "udt_name", "character_maximum_length", "character_octet_length", "domain_default", "collation_schema", "collation_name", "local_collation"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"information_schema", "yes_or_no", "character varying", "pg_catalog", "varchar", int64(3), int64(12), nil, "pg_catalog", "C", true}}),
		} {
			t.Run(fmt.Sprintf("PostgreSQLInformationSchema=%d", i), func(t *testing.T) {
				infoYesOrNoBoth(t, p, conn, recorder, s)
			})
		}
	})
}
