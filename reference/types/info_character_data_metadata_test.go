package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCharacterDataColumnTypes(t *testing.T, columns []*sql.ColumnType, pg, declared bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	wantLength := int64(math.MaxInt64)
	if pg {
		wantLength = -5
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "VARCHAR" || c.ScanType() != reflect.TypeOf("") || length != wantLength || !lengthOK || precision != 0 || scale != 0 || decimalOK {
		t.Fatalf("metadata name=%q type=%q scan=%v length=(%d,%v) decimal=(%d,%d,%v) nullable=(%v,%v); want v VARCHAR string (%d,true) (0,0,false)", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, wantLength)
	}
	if (declared || pg) && (nullable != !pg || nullableOK != !pg) {
		t.Fatalf("nullable=(%v,%v) want (%v,%v), declared=%v", nullable, nullableOK, !pg, !pg, declared)
	}
}

func TestReferenceInfoCharacterDataMetadata(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCharacterDataSeed(t, p, conn, recorder, false)
		for i, s := range []fixture.Step{
			infoCharacterDataRows(backend.PG, "SELECT v FROM public.info_character_data_witness WHERE id=3", [][]any{{"tail  "}}),
			infoCharacterDataRows(backend.PG, "SELECT v FROM public.info_character_data_witness WHERE id=99", [][]any{}),
			infoCharacterDataRows(backend.PG, "SELECT v FROM public.info_character_data_witness WHERE id=6", [][]any{{nil}}),
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("result=%d/prepared=%v", i, prepared), func(t *testing.T) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
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
					infoCharacterDataColumnTypes(t, columns, backend.PG, true)
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
					infoCharacterDataColumnTypes(t, columns, backend.PG, true)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					infoCharacterDataColumnTypes(t, columns, backend.PG, true)
				})
			}
		}
	})
}

func infoCharacterDataIdentitySteps() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT 'information_schema.character_data'::regtype::text AS domain", []string{"domain"}, []string{"TEXT"}, [][]any{{"information_schema.character_data"}}),
		fixture.Select(`SELECT n.nspname AS namespace,t.typname AS name,t.typtype AS kind,t.typcategory AS category,b.oid::integer AS base_oid,bn.nspname AS base_schema,b.typname AS base_name,t.typtypmod AS typmod,t.typcollation::integer AS collation,c.collname AS collation_name,t.typnotnull AS required,t.typdefault AS default_value,t.typdefaultbin AS default_tree,t.typndims AS dimensions,t.typlen::integer AS physical_length,t.typbyval AS by_value,t.typalign AS alignment,t.typstorage AS storage,(t.oid>0 AND t.oid<>b.oid AND t.typarray>0 AND t.typarray<>t.oid AND a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS identity FROM (SELECT * FROM pg_catalog.pg_type WHERE typname='character_data') t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_type b ON b.oid=t.typbasetype JOIN pg_catalog.pg_namespace bn ON bn.oid=b.typnamespace JOIN pg_catalog.pg_type a ON a.oid=t.typarray JOIN pg_catalog.pg_collation c ON c.oid=t.typcollation WHERE n.nspname='information_schema' AND t.typname='character_data'`,
			[]string{"namespace", "name", "kind", "category", "base_oid", "base_schema", "base_name", "typmod", "collation", "collation_name", "required", "default_value", "default_tree", "dimensions", "physical_length", "by_value", "alignment", "storage", "identity"},
			[]string{"NAME", "NAME", "CHAR", "CHAR", "INT4", "NAME", "NAME", "INT4", "INT4", "NAME", "BOOL", "TEXT", "PG_NODE_TREE", "INT4", "INT4", "BOOL", "CHAR", "CHAR", "BOOL"},
			[][]any{{"information_schema", "character_data", "d", "S", int64(1043), "pg_catalog", "varchar", int64(-1), int64(950), "C", false, nil, nil, int64(0), int64(-1), false, "i", "x", true}}),
		fixture.Select(`SELECT t.typinput::integer AS input,t.typreceive::integer AS receive,t.typoutput::integer AS output,t.typsend::integer AS send,t.typmodin::integer AS modin,t.typmodout::integer AS modout,t.typsubscript::integer AS subscript,t.typanalyze::integer AS analyze FROM (SELECT * FROM pg_catalog.pg_type WHERE typname='character_data') t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='information_schema'`,
			[]string{"input", "receive", "output", "send", "modin", "modout", "subscript", "analyze"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2597), int64(2598), int64(1047), int64(2433), int64(0), int64(0), int64(0), int64(0)}}),
		fixture.Select(`SELECT (a.atttypid=t.oid AND a.atttypid<>1043) AS domain_identity,a.atttypmod AS typmod,a.attcollation::integer AS collation,a.attnotnull AS required FROM (SELECT * FROM pg_catalog.pg_attribute WHERE attname='v') a JOIN (SELECT * FROM pg_catalog.pg_class WHERE relname='info_character_data_witness') r ON r.oid=a.attrelid JOIN pg_catalog.pg_namespace rn ON rn.oid=r.relnamespace JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace tn ON tn.oid=t.typnamespace WHERE rn.nspname='public' AND r.relname='info_character_data_witness' AND a.attname='v' AND tn.nspname='information_schema' AND t.typname='character_data'`,
			[]string{"domain_identity", "typmod", "collation", "required"}, []string{"BOOL", "INT4", "INT4", "BOOL"}, [][]any{{true, int64(-1), int64(950), false}}),
		fixture.Select(`SELECT c.conname AS name FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_type t ON t.oid=c.contypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='information_schema' AND t.typname='character_data'`, []string{"name"}, []string{"NAME"}, [][]any{}),
	}
}

func infoCharacterDataIdentity(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for i, s := range infoCharacterDataIdentitySteps() {
		t.Run(fmt.Sprintf("catalog=%d", i), func(t *testing.T) {
			infoCharacterDataBoth(t, p, conn, recorder, s)
		})
	}
}

func TestReferenceInfoCharacterDataIdentity(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCharacterDataIdentity(t, p, conn, recorder)
	})
}
