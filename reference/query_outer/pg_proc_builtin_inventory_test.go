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
// access-method, BRIN, restricted-statistics-view, generic-file, control-data, runtime-information, session-activity, session-endpoint, trigger-depth, trigger-builtin, event-trigger rewrite and settings/options descriptor owners. The existing
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
// Statistics definition inquiry OID3415 uses the same one-OID strict/STABLE/
// parallel-safe TEXT contract and unnamed input formal.
// The two publication inquiries retain released variadic/output formals,
// strict/stable/parallel-safe metadata and fixed scalar/set-returning types.
// The four XML predicate identities 2614/3051/3052/3053 retain strict scalar BOOL
// results, parallel-safe defaults and internal symbols. Only the xmloption-aware
// unsuffixed validator is STABLE; the other three are IMMUTABLE.
// The physical compression inquiry retains its released one-any strict/STABLE/
// parallel-safe TEXT declaration, cost 1 and internal implementation symbol.
// The nine replication-slot lifecycle declarations retain strict/volatile/
// parallel-unsafe metadata, exact OUT formals, defaults and overload symbols.
// Logical-message OIDs3577/3578 and sync OID6344 retain strict/volatile/
// parallel-unsafe scalar declarations, actual message input names and emit defaults.
// XML aggregate OID2901 retains its non-strict IMMUTABLE/parallel-safe XML
// signature and aggregate_dummy symbol; xmlconcat2 remains lookup-only.
// Relation/attribute statistics restore/clear OIDs6362/6397/6363/6398 add
// non-strict VOLATILE/parallel-unsafe signatures and heterogeneous ANY variadics.
// The ordinary XML construction routines 2895/3813 retain strict immutable XML
// results, parallel-safe defaults and their released internal symbols.
// The three server-signal declarations retain strict VOLATILE/parallel-safe
// scalar BOOL metadata, unnamed inputs and their released internal symbols.
// The fourteen XML mapping identities retain explicit cost 100 and strict XML
// results with named all-IN formals: table/schema/database functions are STABLE/
// parallel-restricted, query and cursor functions VOLATILE/parallel-unsafe.
// The six WAL-position declarations retain strict VOLATILE/parallel-safe scalar
// PG_LSN results, unnamed formals, no defaults and released internal symbols.
// Pure WAL-name identities2850/2851/6213 retain strict, parallel-safe scalar
// declarations: immutable PG_LSN filename helpers and stable TEXT split, with
// complete ordered OUT metadata and a genuinely unnamed filename-only input.
// The four large-object descriptor declarations retain strict VOLATILE/
// parallel-unsafe scalar metadata, unnamed inputs and released be_lo* symbols.
// The session-signal identities 2096/2171 retain strict VOLATILE/parallel-safe
// BOOL declarations; only termination names pid/timeout and defaults timeout.
// The two statistics-snapshot lifecycle declarations retain their released
// zero-argument TIMESTAMPTZ/VOID signatures and parallel-restricted metadata.
// Current-backend block inquiries OIDs3044/3045 retain unnamed strict OID-to-INT8
// scalar signatures, VOLATILE/parallel-restricted metadata and matching symbols.
// Replication statistics reset OIDs6170/6232 retain unnamed TEXT/OID inputs,
// non-strict VOLATILE/parallel-safe VOID results and matching internal symbols.
// Shared/SLRU/backend reset OIDs3775/2307/6387 retain VOLATILE/parallel-safe
// scalar VOID declarations. Shared/SLRU name target with one TEXT NULL default;
// backend alone is strict, with one unnamed INT4 PID and no default.
// Database/object reset OIDs2274/3776/3777 retain volatile parallel-safe VOID
// declarations, no defaults/names, and strictness only for the unary OID calls.
// Exported transaction snapshots add strict VOLATILE/parallel-unsafe OID3809,
// with no inputs and a scalar TEXT result; strictness is pg_proc.h's default.
// Callable registry lookups OIDs3476/3479/3492/3494 retain one unnamed TEXT
// input, the corresponding alias result, strict STABLE/parallel-safe scalar
// metadata, cost 1 and an internal source matching the SQL name.
// Relation/namespace/type/typmod lookups OIDs3495/4086/3493/6317 retain the
// same one-TEXT contract, returning their exact alias types or INT4 typmod.
// GIN pending-list cleanup OID3789 retains its strict VOLATILE/parallel-unsafe
// one-regclass INT8 declaration, cost 1 and released internal symbol.
// Array-vector construction and current-config identity add strict scalar,
// parallel-safe declarations3327/3759 with immutable/stable volatility.
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
	// declarations, two publication inquiries, four match routines,
	// two genuine ispell helpers, four executable XML predicate identities,
	// the physical compression inquiry, nine replication-slot declarations,
	// XML aggregate OID2901, three relation-file location declarations,
	// four relation/attribute statistics administration declarations,
	// ordinary XML construction identities xmlcomment/xmltext, five
	// strict/volatile/parallel-safe recovery-control declarations,
	// three strict/volatile/parallel-unsafe logical-message/sync declarations,
	// three strict/volatile/parallel-safe server-signal declarations, eight
	// table/query/cursor XML row/schema mapping identities, five
	// origin identity/progress routines, six strict/volatile/parallel-safe
	// WAL-position declarations, and four strict/volatile/parallel-unsafe
	// large-object descriptor declarations, and two strict/volatile/parallel-safe
	// session-signal declarations, and two statistics-snapshot lifecycle
	// declarations, and the metadata-only AIO, build-configuration and file-settings
	// view backing declarations. pg_config retains its released two OUT TEXT fields,
	// strict STABLE/parallel-restricted metadata and row estimate 23. File settings
	// retains seven OUT fields, strict VOLATILE/parallel-safe metadata, rows 1000,
	// and the internal source show_all_file_settings rather than its SQL name.
	// Identity-file mappings adds its metadata-only backing declaration, OID6250:
	// seven named OUT fields, strict VOLATILE/parallel-safe SETOF record, rows
	// 1000 and internal source pg_ident_file_mappings (REL_18_6 pg_proc.dat).
	// Twelve comparison estimator declarations add their genuine strict/STABLE/
	// parallel-safe INTERNAL metadata, without any scalar execution provider.
	// Exported snapshots add the released zero-argument TEXT declaration OID3809.
	// Four XPath declarations add the two internal implementations and two
	// released SQL wrapper bodies.
	// Statistics definition introspection adds the one-OID TEXT declaration3415.
	// The two truncate declarations add strict VOLATILE/parallel-unsafe
	// INT4 results and their released signed descriptor/length formals.
	// Seek/tell add four strict VOLATILE/parallel-unsafe declarations with
	// signed INT4 descriptor/whence and width-selected offsets/results.
	// Nullable registry lookups add released one-TEXT scalar declarations
	// OIDs4093/4195, callable identities3476/3479/3492/3494 and relation/type
	// identities3495/4086/3493/6317, with strict/STABLE/parallel-safe metadata
	// and unnamed inputs.
	// GIN adds its genuine handler333 and ten internal extraction/consistency
	// support declarations, with no scalar C ABI execution.
	// Selected btree_gin classes add genuine bitcmp/varbitcmp declarations:
	// both are leakproof and strict IMMUTABLE/parallel-safe INT4 comparators;
	// their released internal source is bitcmp, with cost 1 and unnamed inputs.
	// The selected network GIN family adds genuine macaddr8_cmp OID4119.
	// Pending-list cleanup adds strict VOLATILE/parallel-unsafe
	// REGCLASS-to-INT8 declaration gin_clean_pending_list, OID3789.
	// Replication statistics reset adds the released non-strict VOLATILE/
	// parallel-safe TEXT/OID->VOID declarations6170/6232 with unnamed inputs.
	// Array-vector construction and current configuration add the strict,
	// parallel-safe scalar declarations3327/3759 and unnamed input formals.
	// Shared, SLRU and backend reset add three released scalar VOID identities.
	// Schema/database XML mapping adds six strict/STABLE/restricted XML records.
	// Eight frequency/cover-density rank and four text headline declarations
	// add strict parallel-safe scalar REAL/TEXT signatures (REL_18_6).
	// Current-backend memory adds one callable ten-OUT-field record SRF.
	// Current-backend fetched/hit inquiries add two strict VOLATILE/restricted
	// unnamed OID-to-INT8 declarations3044/3045.
	// Archiver adds its metadata-only non-strict STABLE/restricted scalar
	// RECORD declaration3195, seven named OUT fields and default PUBLIC EXECUTE.
	// SLRU adds its metadata-only non-strict STABLE/restricted SETOF RECORD
	// declaration2306, nine named OUT fields, rows100 and default PUBLIC EXECUTE.
	// Bgwriter adds four metadata-only strict STABLE/restricted zero-input
	// scalar declarations2772/2773/2859/3075: INT8 counters and TIMESTAMPTZ reset,
	// no OUT fields/defaults and ordinary implicit PUBLIC EXECUTE authority.
	// Subscription monitoring adds non-strict STABLE/restricted SETOF RECORD
	// declaration6118, one named IN OID, ten OUT fields and rows10.
	// Checkpointer adds eleven metadata-only strict STABLE/restricted zero-input
	// scalar declarations: eight INT8 counters, two FLOAT8 times and TIMESTAMPTZ
	// reset, with no OUT fields/defaults and default PUBLIC EXECUTE authority.
	// Xact function view authority adds three strict VOLATILE/restricted
	// unnamed OID-input scalar declarations3046/3047/3048, with INT8/FLOAT8
	// results, no OUT/default/set metadata and default PUBLIC EXECUTE.
	if len(facts) != 2274 {
		t.Fatalf("released represented overloads: got %d, want 2274", len(facts))
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
