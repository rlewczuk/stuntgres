package reference_test

import (
	"embed"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent release facts: REL_18_6 pg_proc.dat, pg_proc.h defaults,
// pg_type.dat (including generated array identities), and system_functions.sql
// CREATE OR REPLACE declarations. These are not native observations or copies
// of production display/signature/property data. Only represented membership
// comes from the actual routine declaration/cast completion and fixed conversion,
// access-method, BRIN, shared-memory, generic-file, control-data, runtime-information, session-activity, session-endpoint, trigger-depth, trigger-builtin, event-trigger rewrite and settings/options descriptor owners. The existing 1719
// information_schema fact approach is reused, not mistaken for complete membership.
// The fourteen parser/vector/DDL-receiver and twelve RI metadata descriptors are included.
// The four pg_get_ruledef/pg_get_triggerdef executable overloads use the same
// independent released facts, including pg_proc.h's parallel-safe default.
// The four oid->text routine-declaration inquiries follow the same released
// strict/stable/parallel-safe contract and internal implementation symbols.
// The five OID->BOOL text-search/statistics visibility inquiries retain released
// cost 10, strict/stable/parallel-safe metadata through the same inventory.
// The two one-OID partition inquiries retain the released strict/stable/
// parallel-safe scalar TEXT metadata and unnamed input formals.
// The two publication inquiries retain released variadic/output formals,
// strict/stable/parallel-safe metadata and fixed scalar/set-returning types.
// Sources:
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.h
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_type.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/system_functions.sql
//
// Each TSV record is one overload, in pgProcInventoryColumns order. The \N
// sentinel is NULL, distinct from empty text/arrays. Numeric OID projections
// retain exact fixed identities; raw vector/tree carriers and metadata types have separate public
// shape/property witnesses. Tree presence here is independent of backend tree
// serialization: native structural trees deliberately are not nodeToString.
// The bounded data files remain readable, uncompressed, test-owned release facts.
//
//go:embed testdata/pg_proc_builtin_inventory_*.tsv
var pgProcInventoryFiles embed.FS

var pgProcInventoryColumns = []string{
	"oid", "proname", "pronamespace", "prolang", "procost", "prorows",
	"provariadic", "prosupport", "prokind", "prosecdef", "proleakproof",
	"proisstrict", "proretset", "provolatile", "proparallel", "pronargs",
	"pronargdefaults", "prorettype", "proargtypes", "proallargtypes",
	"proargmodes", "proargnames", "has_defaults", "protrftypes", "prosrc",
	"probin", "has_sqlbody", "proconfig",
}

var pgProcInventoryTypes = []string{
	"INT8", "TEXT", "INT8", "INT8", "FLOAT4", "FLOAT4",
	"INT8", "INT8", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT",
	"TEXT", "INT8", "INT8", "INT8", "TEXT", "TEXT", "TEXT", "TEXT",
	"BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT",
}

const pgProcInventoryProjection = `oid::bigint AS oid,proname::text AS proname,
pronamespace::bigint AS pronamespace,prolang::bigint AS prolang,procost,prorows,
provariadic::bigint AS provariadic,prosupport::oid::bigint AS prosupport,
prokind::text AS prokind,prosecdef,proleakproof,proisstrict,proretset,
provolatile::text AS provolatile,proparallel::text AS proparallel,
pronargs::bigint AS pronargs,pronargdefaults::bigint AS pronargdefaults,
prorettype::bigint AS prorettype,proargtypes::text AS proargtypes,
proallargtypes::text AS proallargtypes,proargmodes::text AS proargmodes,
proargnames::text AS proargnames,proargdefaults IS NOT NULL AS has_defaults,
protrftypes::text AS protrftypes,prosrc,probin,
prosqlbody IS NOT NULL AS has_sqlbody,proconfig::text AS proconfig`

func pgProcInventoryFacts(t *testing.T) [][]any {
	t.Helper()
	var facts [][]any
	seen := make(map[int64]bool)
	for part := 1; part <= 5; part++ {
		name := fmt.Sprintf("testdata/pg_proc_builtin_inventory_%d.tsv", part)
		f, err := pgProcInventoryFiles.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		reader := csv.NewReader(f)
		reader.Comma = '\t'
		reader.FieldsPerRecord = len(pgProcInventoryColumns)
		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = f.Close()
				t.Fatalf("%s record %d: %v", name, len(facts), err)
			}
			fact := make([]any, len(record))
			for col, text := range record {
				if text == `\N` {
					continue
				}
				var value any
				switch pgProcInventoryTypes[col] {
				case "INT8":
					value, err = strconv.ParseInt(text, 10, 64)
				case "FLOAT4":
					value, err = strconv.ParseFloat(text, 64)
				case "BOOL":
					value, err = strconv.ParseBool(text)
				default:
					value = text
				}
				if err != nil {
					_ = f.Close()
					t.Fatalf("%s record %d column %s: %v", name, len(facts), pgProcInventoryColumns[col], err)
				}
				fact[col] = value
			}
			oid := fact[0].(int64)
			if seen[oid] {
				t.Fatalf("duplicate released overload %d", oid)
			}
			seen[oid] = true
			facts = append(facts, fact)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// This is the exact represented membership, not a guessed PostgreSQL total:
	// 2073 existing declarations, four text-search debug/stat overloads,
	// eight JSON text-search overloads, five timezone-aware JSONPath query
	// declarations, two publication inquiries, and four match routines.
	if len(facts) != 2096 {
		t.Fatalf("released represented overloads: got %d, want 2096", len(facts))
	}
	return facts
}

func pgProcInventoryIDs(facts [][]any) string {
	ids := make([]string, len(facts))
	for i, fact := range facts {
		ids[i] = strconv.FormatInt(fact[0].(int64), 10)
	}
	return strings.Join(ids, ",")
}

func TestReferencePGProcBuiltinInventory(t *testing.T) {
	facts := pgProcInventoryFacts(t)
	t.Run("membership", func(t *testing.T) {
		p := fixture.NewScenario(t)
		want := make([][]any, len(facts))
		for i, fact := range facts {
			want[i] = []any{fact[0]}
		}
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				// Native must expose the complete fixed-owned bag and nothing
				// lookup-only. PostgreSQL has many additional executable owners;
				// compare its exact supported-ID subset, never its total count.
				where := "oid < 16384"
				if b.PG {
					where = "oid IN (" + pgProcInventoryIDs(facts) + ")"
				}
				step := fixture.Select("SELECT oid::bigint AS oid FROM pg_catalog.pg_proc WHERE "+where,
					[]string{"oid"}, []string{"INT8"}, want)
				step.Bag = true
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(step))
			})
		}
	})
	// Serial bounded scenarios use the existing 200-fact inventory approach;
	// each direct/prepared query checks exact values and bag multiplicities.
	const batch = 200
	for start := 0; start < len(facts); start += batch {
		end := min(start+batch, len(facts))
		t.Run(fmt.Sprintf("%04d-%04d", start, end-1), func(t *testing.T) {
			p := fixture.NewScenario(t)
			step := fixture.Select("SELECT "+pgProcInventoryProjection+" FROM pg_catalog.pg_proc WHERE oid IN ("+pgProcInventoryIDs(facts[start:end])+")",
				pgProcInventoryColumns, pgProcInventoryTypes, facts[start:end])
			step.Bag = true
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(step))
				})
			}
		})
	}
}
