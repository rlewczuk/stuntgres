package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func domainReferenceCheckMetadata(t *testing.T, rows *sql.Rows, pg bool, expression bool, names []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(names) {
		t.Fatalf("metadata columns=%d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		var scan reflect.Type
		var wantLength, wantPrecision, wantScale int64
		wantLengthOK, wantDecimalOK := false, false
		switch names[i] {
		case "INT4":
			scan = reflect.TypeOf(int64(0))
			if pg {
				scan = reflect.TypeOf(int32(0))
			}
		case "NUMERIC":
			scan = reflect.TypeOf("")
			if pg {
				scan = reflect.TypeOf((*any)(nil)).Elem()
			}
			wantPrecision, wantScale, wantDecimalOK = 5, 2, true
		case "VARCHAR":
			scan = reflect.TypeOf("")
			wantLength, wantLengthOK = 3, true
		case "_NAME":
			scan = reflect.TypeOf("")
			if pg {
				scan = reflect.TypeOf((*any)(nil)).Elem()
			}
		default:
			t.Fatalf("unexpected metadata fixture %s", names[i])
		}
		if c.DatabaseTypeName() != names[i] || c.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != wantPrecision || scale != wantScale || decimalOK != wantDecimalOK {
			t.Fatalf("column %s: type=%s scan=%v length=%d,%v decimal=%d,%d,%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK)
		}
		if pg {
			if nullable || nullableOK {
				t.Fatalf("pq nullable=%v,%v", nullable, nullableOK)
			}
		} else if !expression && (!nullable || !nullableOK) {
			t.Fatalf("native nullable domain declaration=%v,%v", nullable, nullableOK)
		}
	}
}

func TestReferenceDomainMetadata(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.dn AS numeric(5,2)",
			"CREATE DOMAIN domain_s.dv AS varchar(3)", "CREATE DOMAIN domain_s.da AS name[]",
			"CREATE TABLE domain_s.metadata (id integer,i domain_s.di,n domain_s.dn,v domain_s.dv,a domain_s.da)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.metadata VALUES (1,7,12.345,'abc','{alpha,public}'),(2,NULL,NULL,NULL,NULL)", 2))
		queries := []struct {
			step       fixture.Step
			expression bool
		}{
			{fixture.Select("SELECT i,n,v,a FROM domain_s.metadata ORDER BY id", []string{"i", "n", "v", "a"}, []string{"INT4", "NUMERIC", "VARCHAR", "_NAME"}, [][]any{{int64(7), "12.35", "abc", "{alpha,public}"}, {nil, nil, nil, nil}}), false},
			{fixture.Select("SELECT i,i+1 AS base,(i+1)::domain_s.di AS restored,n,v FROM domain_s.metadata ORDER BY id", []string{"i", "base", "restored", "n", "v"}, []string{"INT4", "INT4", "INT4", "NUMERIC", "VARCHAR"}, [][]any{{int64(7), int64(8), int64(8), "12.35", "abc"}, {nil, nil, nil, nil, nil}}), true},
			{fixture.Select("SELECT $1::domain_s.di AS i,$2::domain_s.dn AS n,$3::domain_s.dv AS v,$4::domain_s.da AS a FROM public.one", []string{"i", "n", "v", "a"}, []string{"INT4", "NUMERIC", "VARCHAR", "_NAME"}, [][]any{{int64(8), "12.35", "abc", "{alpha,public}"}}, int64(8), "12.345", "abc", "{alpha,public}"), true},
			{fixture.Select("SELECT $1::domain_s.di AS i,$2::domain_s.dn AS n,$3::domain_s.dv AS v,$4::domain_s.da AS a FROM public.one", []string{"i", "n", "v", "a"}, []string{"INT4", "NUMERIC", "VARCHAR", "_NAME"}, [][]any{{nil, nil, nil, nil}}, nil, nil, nil, nil), true},
			{fixture.Select("SELECT i,n,v,a FROM domain_s.metadata WHERE id=99", []string{"i", "n", "v", "a"}, []string{"INT4", "NUMERIC", "VARCHAR", "_NAME"}, [][]any{}), false},
		}
		for _, tc := range queries {
			for _, prepared := range []bool{false, true} {
				s := tc.step
				s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
					domainReferenceCheckMetadata(t, rows, backend.PG, tc.expression, s.Types)
				}
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				// Metadata exists even when the execution produces no rows.
				if len(s.Rows) == 0 {
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, conn, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer func() {
							if err := closeQuery(); err != nil {
								t.Error(err)
							}
						}()
						domainReferenceCheckMetadata(t, rows, backend.PG, tc.expression, s.Types)
					}()
				}
			}
		}
		if backend.PG {
			run(fixture.Select("SELECT t.typtype::text AS kind,t.typbasetype::regtype::text AS base,t.typtypmod AS modifier,t.typndims AS dimensions,t.typnotnull AS required FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname='dn'", []string{"kind", "base", "modifier", "dimensions", "required"}, []string{"TEXT", "TEXT", "INT4", "INT4", "BOOL"}, [][]any{{"d", "numeric", int64(327686), int64(0), false}}))
		}
	})
}
