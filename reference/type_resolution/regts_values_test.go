package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// regtsScenario shares the complete public SQL body between native and PostgreSQL.
func regtsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "Reg.TS", "Reg.TS.Renamed", "reg_ts_left", "reg_ts_right")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func regtsLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func regtsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, n))
}
func regtsInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regtsExec(t, p, b, c, `CREATE SCHEMA "Reg.TS"`, 0)
	regtsExec(t, p, b, c, `CREATE EXTENSION unaccent WITH SCHEMA "Reg.TS"`, 0)
}
func regtsDictionaryOID(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
	t.Helper()
	var namespace int64
	if err := c.QueryRowContext(p.Ctx, "SELECT extnamespace FROM pg_catalog.pg_extension WHERE extname='unaccent'").Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	if namespace <= 0 || namespace >= 4294967295 {
		t.Fatalf("invalid installed namespace OID %d", namespace)
	}
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_ts_dict WHERE dictnamespace=$1::oid AND dictname='unaccent'", namespace).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid >= 4294967295 {
		t.Fatalf("invalid allocated dictionary OID %d", oid)
	}
	var named int64
	if err := c.QueryRowContext(p.Ctx, `SELECT '"Reg.TS".unaccent'::regdictionary::oid::bigint`).Scan(&named); err != nil {
		t.Fatal(err)
	}
	if named != oid {
		t.Fatalf("named dictionary OID %d differs from actual installed descriptor %d", named, oid)
	}
	return oid
}
func regtsSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, names, types []string, want [][]any, args ...any) {
	t.Helper()
	s := fixture.Select(q, names, types, want, args...)
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if types[col] != "REGCONFIG" && types[col] != "REGDICTIONARY" {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, v)
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cts, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, ct := range cts {
			if types[i] != "REGCONFIG" && types[i] != "REGDICTIONARY" {
				continue
			}
			wantScan := reflect.TypeFor[string]()
			if b.PG {
				wantScan = reflect.TypeFor[any]()
			}
			l, lk := ct.Length()
			precision, scale, dk := ct.DecimalSize()
			if ct.ScanType() != wantScan || l != 0 || lk || precision != 0 || scale != 0 || dk {
				t.Fatalf("alias metadata %s: scan=%v length=%d/%t decimal=%d,%d/%t", ct.Name(), ct.ScanType(), l, lk, precision, scale, dk)
			}
		}
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		if len(types) != 1 || (types[0] != "REGCONFIG" && types[0] != "REGDICTIONARY") {
			return
		}
		var text sql.NullString
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		v := want[index][0]
		if text.Valid != (v != nil) || v != nil && text.String != v {
			t.Fatalf("NullString=%#v want %#v", text, v)
		}
		var bytes []byte
		if err := rows.Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		if v == nil && bytes != nil || v != nil && string(bytes) != v {
			t.Fatalf("byte scan=%q want %#v", bytes, v)
		}
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func TestReferenceRegTSValuesMetadata(t *testing.T) {
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		for _, alias := range []string{"regconfig", "regdictionary"} {
			for _, tc := range []struct{ input, output string }{{"-", "-"}, {"0", "-"}, {"010", "8"}, {"4294967295", "4294967295"}} {
				q := "SELECT " + regtsLiteral(tc.input) + "::" + alias + " AS v"
				regtsSelect(t, p, b, c, q, []string{"v"}, []string{strings.ToUpper(alias)}, [][]any{{tc.output}})
				stmt, err := c.PrepareContext(p.Ctx, "SELECT $1::"+alias+" AS v")
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT $1::" + alias + " AS v"}
				regtsSelect(t, p, b, held, held.SQL, []string{"v"}, []string{strings.ToUpper(alias)}, [][]any{{tc.output}}, tc.input)
				stmt.Close()
			}
			for _, suffix := range []string{"", " WHERE false"} {
				want := [][]any{{nil}}
				if suffix != "" {
					want = nil
				}
				regtsSelect(t, p, b, c, "SELECT NULL::"+alias+" AS v"+suffix, []string{"v"}, []string{strings.ToUpper(alias)}, want)
			}
			regtsSelect(t, p, b, c, "SELECT (-1)::integer::"+alias+" AS v", []string{"v"}, []string{strings.ToUpper(alias)}, [][]any{{"4294967295"}})
			regtsSelect(t, p, b, c, "SELECT '4294967295'::"+alias+"::integer AS signed", []string{"signed"}, []string{"INT4"}, [][]any{{int64(-1)}})
			regtsSelect(t, p, b, c, "SELECT '8'::oid::"+alias+" AS v", []string{"v"}, []string{strings.ToUpper(alias)}, [][]any{{"8"}})
			regtsSelect(t, p, b, c, "SELECT '8'::text::"+alias+"::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"8"}})
			regtsSelect(t, p, b, c, "SELECT '8'::"+alias+"='010'::"+alias+" AS same,'0'::"+alias+" IS NULL AS missing", []string{"same", "missing"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}})
		}
		regtsSelect(t, p, b, c, "SELECT 'regconfig'::regtype::oid::bigint AS c,'regdictionary'::regtype::oid::bigint AS d,'regconfig[]'::regtype::oid::bigint AS ca,'regdictionary[]'::regtype::oid::bigint AS da", []string{"c", "d", "ca", "da"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3734), int64(3769), int64(3735), int64(3770)}})
		// pg_type is a genuine PostgreSQL-only catalog projection, not a
		// native provider. Shared identities above and native codec units
		// independently retain scalar/array IDs and four-byte inline storage.
		if b.PG {
			regtsSelect(t, p, b, c, "SELECT typname::text AS n,oid::bigint AS id,typarray::bigint AS a,typlen::integer AS size,typbyval AS bv,typcategory::text AS cat FROM pg_catalog.pg_type WHERE typname IN ('regconfig','regdictionary') ORDER BY typname", []string{"n", "id", "a", "size", "bv", "cat"}, []string{"TEXT", "INT8", "INT8", "INT4", "BOOL", "TEXT"}, [][]any{{"regconfig", int64(3734), int64(3735), int64(4), true, "N"}, {"regdictionary", int64(3769), int64(3770), int64(4), true, "N"}})
		}
		oid := regtsDictionaryOID(t, p, c)
		regtsSelect(t, p, b, c, fmt.Sprintf("SELECT '%d'::regdictionary AS d,'%d'::regconfig AS c", oid, oid), []string{"d", "c"}, []string{"REGDICTIONARY", "REGCONFIG"}, [][]any{{`"Reg.TS".unaccent`, fmt.Sprint(oid)}})
		regtsSelect(t, p, b, c, `SELECT '"Reg.TS".unaccent'::regdictionary::oid::bigint AS id`, []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		regtsExec(t, p, b, c, "CREATE DOMAIN reg_ts_domain AS regdictionary", 0)
		regtsSelect(t, p, b, c, "SELECT '-'::reg_ts_domain AS v", []string{"v"}, []string{"REGDICTIONARY"}, [][]any{{"-"}})
	})
}
