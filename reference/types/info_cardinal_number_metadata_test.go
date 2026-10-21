package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoCardinalNumberMetadata(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCardinalNumberSeed(t, p, conn, recorder)
		for i, s := range []fixture.Step{
			infoCardinalNumberStored(backend.PG),
			infoCardinalNumberRows(backend.PG, "SELECT v FROM public.info_cardinal_number_witness WHERE id=99", [][]any{}),
			infoCardinalNumberRows(backend.PG, "SELECT v FROM public.info_cardinal_number_witness WHERE id=3", [][]any{{nil}}),
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
					infoCardinalNumberColumnTypes(t, columns, backend.PG)
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
					infoCardinalNumberColumnTypes(t, columns, backend.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					infoCardinalNumberColumnTypes(t, columns, backend.PG)
				})
			}
		}
	})
}

func infoCardinalNumberIdentitySteps() []fixture.Step {
	// Prefilter only existing WHERE predicates before the catalog joins, retaining
	// every identity relationship and the complete independently expected rows.
	return []fixture.Step{
		fixture.Select("SELECT 'information_schema.cardinal_number'::regtype::text AS domain", []string{"domain"}, []string{"TEXT"}, [][]any{{"information_schema.cardinal_number"}}),
		fixture.Select(`SELECT n.nspname AS namespace,t.typname AS name,t.typtype AS kind,t.typcategory AS category,b.oid::integer AS base_oid,bn.nspname AS base_schema,b.typname AS base_name,t.typtypmod AS typmod,t.typcollation::integer AS collation,t.typnotnull AS required,t.typdefault AS default_value,t.typdefaultbin AS default_tree,t.typndims AS dimensions,t.typlen::integer AS physical_length,t.typbyval AS by_value,t.typalign AS alignment,t.typstorage AS storage,(t.oid>0 AND t.oid<>b.oid AND t.typarray>0 AND t.typarray<>t.oid AND a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS identity FROM (SELECT * FROM pg_catalog.pg_type WHERE typname='cardinal_number') t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_type b ON b.oid=t.typbasetype JOIN pg_catalog.pg_namespace bn ON bn.oid=b.typnamespace JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE n.nspname='information_schema' AND t.typname='cardinal_number'`,
			[]string{"namespace", "name", "kind", "category", "base_oid", "base_schema", "base_name", "typmod", "collation", "required", "default_value", "default_tree", "dimensions", "physical_length", "by_value", "alignment", "storage", "identity"},
			[]string{"NAME", "NAME", "CHAR", "CHAR", "INT4", "NAME", "NAME", "INT4", "INT4", "BOOL", "TEXT", "PG_NODE_TREE", "INT4", "INT4", "BOOL", "CHAR", "CHAR", "BOOL"},
			[][]any{{"information_schema", "cardinal_number", "d", "N", int64(23), "pg_catalog", "int4", int64(-1), int64(0), false, nil, nil, int64(0), int64(4), true, "i", "p", true}}),
		fixture.Select(`SELECT (a.atttypid=t.oid) AS domain_identity,a.atttypmod AS typmod,a.attcollation::integer AS collation,a.attnotnull AS required FROM (SELECT * FROM pg_catalog.pg_attribute WHERE attname='v') a JOIN (SELECT * FROM pg_catalog.pg_class WHERE relname='info_cardinal_number_witness') r ON r.oid=a.attrelid JOIN pg_catalog.pg_namespace rn ON rn.oid=r.relnamespace JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace tn ON tn.oid=t.typnamespace WHERE rn.nspname='public' AND r.relname='info_cardinal_number_witness' AND a.attname='v' AND tn.nspname='information_schema' AND t.typname='cardinal_number'`,
			[]string{"domain_identity", "typmod", "collation", "required"}, []string{"BOOL", "INT4", "INT4", "BOOL"}, [][]any{{true, int64(-1), int64(0), false}}),
		fixture.Select(`SELECT c.conname AS name,c.contype AS kind,c.convalidated AS validated,(c.oid>0 AND c.contypid=t.oid AND c.conrelid=0 AND c.connamespace=t.typnamespace) AS identity FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_type t ON t.oid=c.contypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='information_schema' AND t.typname='cardinal_number'`,
			[]string{"name", "kind", "validated", "identity"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"cardinal_number_domain_check", "c", true, true}}),
		fixture.Select(`SELECT (t.typinput=2597 AND t.typreceive=2598 AND t.typoutput=43 AND t.typsend=2407 AND t.typmodin=0 AND t.typmodout=0 AND t.typsubscript=0 AND t.typanalyze=0) AS routines,(t.typarray<>t.typbasetype) AS distinct_companion FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='information_schema' AND t.typname='cardinal_number'`,
			[]string{"routines", "distinct_companion"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
	}
}

func infoCardinalNumberIdentity(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, s := range infoCardinalNumberIdentitySteps() {
		infoCardinalNumberBoth(t, p, conn, recorder, s)
	}
}

func TestReferenceInfoCardinalNumberIdentity(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for i, s := range infoCardinalNumberIdentitySteps() {
			t.Run(fmt.Sprintf("catalog=%d", i), func(t *testing.T) {
				infoCardinalNumberBoth(t, p, conn, recorder, s)
			})
		}
		// General information-schema columns/domains providers are not native scope.
		if !backend.PG {
			return
		}
		for i, s := range []fixture.Step{
			fixture.Select(`SELECT data_type::text,domain_schema::text,domain_name::text,udt_schema::text,udt_name::text,numeric_precision::integer,numeric_precision_radix::integer,numeric_scale::integer,is_nullable::text,character_maximum_length::integer,character_octet_length::integer,collation_schema::text,collation_name::text FROM information_schema.columns WHERE table_schema='public' AND table_name='info_cardinal_number_witness' AND column_name='v'`, []string{"data_type", "domain_schema", "domain_name", "udt_schema", "udt_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "is_nullable", "character_maximum_length", "character_octet_length", "collation_schema", "collation_name"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "TEXT", "TEXT"}, [][]any{{"integer", "information_schema", "cardinal_number", "pg_catalog", "int4", int64(32), int64(2), int64(0), "YES", nil, nil, nil, nil}}),
			fixture.Select(`SELECT domain_schema::text,domain_name::text,data_type::text,udt_schema::text,udt_name::text,numeric_precision::integer,numeric_precision_radix::integer,numeric_scale::integer,domain_default::text,character_maximum_length::integer,character_octet_length::integer,collation_schema::text,collation_name::text FROM information_schema.domains WHERE domain_schema='information_schema' AND domain_name='cardinal_number'`, []string{"domain_schema", "domain_name", "data_type", "udt_schema", "udt_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "domain_default", "character_maximum_length", "character_octet_length", "collation_schema", "collation_name"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "TEXT", "TEXT"}, [][]any{{"information_schema", "cardinal_number", "integer", "pg_catalog", "int4", int64(32), int64(2), int64(0), nil, nil, nil, nil, nil}}),
		} {
			t.Run(fmt.Sprintf("PostgreSQLInformationSchema=%d", i), func(t *testing.T) {
				infoCardinalNumberBoth(t, p, conn, recorder, s)
			})
		}
	})
}
