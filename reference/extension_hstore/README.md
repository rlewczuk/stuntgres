# reference/extension_hstore

Dual-mode tests for the `hstore` extension type: scalar codecs, constructors,
lookups, key predicates, updates, set-returning extraction, JSON/JSONB
conversions, records, arrays and comparisons, with their native
Dump/Reset/Restore controls. Other extension types are in
[reference/extension_types](../extension_types/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. The lib/pq client quirks and catalog-probe rules of
[reference/extension_types](../extension_types/README.md#testing) apply here too.

- `hstore_helpers_test.go` owns scalar text/raw metadata and complete diagnostic
  assertions. Canonical pairs distinguish empty, NULL-valued and quoted-NULL
  values from SQL NULL. Conflicting duplicate keys have one allowed original
  survivor, not a backend-choice parity golden. Native controls preserve the
  chosen stored value and retained metadata; emitted SQL is replayed directly
  on the oracle. Held literal assignments retain their canonical values across
  rename/schema moves; retired installations and unrelated TEXT destinations
  bind the original SQL afresh, never stale hstore values. Conversion fixtures
  use ordinary `_TEXT` transport for owned flat/rectangular arrays and invoke
  both named functions and prefix operators after restoration.
  `hstore_json_helpers_test.go` checks JSON result metadata, detached scans and
  SQL NULL separately from JSON member null. Strict/loose expectations preserve
  numeric spelling and distinguish whole JSON numbers from string boundaries;
  stored views/defaults execute after restore and current-name dump replay.
  EXECUTE checks use current callers, distinguishing folded literal NULL from
  demanded row-dependent NULL calls.
  `hstore_jsonb_helpers_test.go` applies the JSONB metadata/transport contract;
  independent strict/cast strings and loose exact-numeric normalization drive
  projection, equality predicates, privileges and extension-owned cast lifecycle.
  `hstore_compare_helpers_test.go` shares typed comparison expectations for
  equality, real B-tree support operators and comparator/hash routines. The
  comparison scenarios cover canonical payload boundaries and NULL bits,
  ordered/grouped consumers, both index methods, complete diagnostics and
  stored expressions through native controls and actual emitted-SQL replay.
  The reusable family selector is `RUN='^TestReferenceHstore'`.
  Constructor and lookup fixtures preserve literal escaping, array shape/bounds
  and missing versus NULL-valued keys; exact body diagnostics remain separate
  from overload errors. `_TEXT` transport covers array lookups. Native controls
  carry constructors, named backers and operators with the shared exact
  sixty-five-owner validator, omitting only the ownerless cast. Nonstrict
  constructors preserve present NULL values under current caller admission.
  Held installed-hstore result signatures reject true Reset/Restore replacement
  with the cached-plan result-type diagnostic; builtin TEXT signatures rebind
  actual constructor/lookup expressions with current inputs. Fresh hstore result
  metadata and detached old readers remain independently asserted.
  Key predicates cover NULL-valued presence, ignored NULL array elements and
  shape-independent requests; containment distinguishes NULL values from text
  NULL. Key/value array fixtures preserve paired positions without an insertion
  order promise. Selected backers, selectivity and commutator links, live caller
  admission and stored consumers survive native controls and emitted-SQL replay.
  Genuine rename/schema moves of all seven constructor/lookup/predicate
  procedures retain original SQL names and exact overload signatures in actual
  Dump output. The exact rename-then-move witness starts at the namespace of the
  one genuine released CREATE EXTENSION, not an assumed final `public` namespace,
  and requires final identities before stored consumer creation. Executable C
  symbols are forbidden as SQL identities in every namespace. Displaced native
  Restore and replay of those identical emitted SQL bytes on PostgreSQL exercise
  both stored consumers and current names.
  The same replay witness preserves escaped and whole-NULL inputs, multi-key and
  matrix construction, direct `_TEXT` metadata and nondefault request bounds,
  duplicates and NULL keys, plus destination-local procedure/arrow relationships.
  Record fixtures additionally assert declared RECORD/ANYELEMENT identities
  independently of effective tuples, destination typmods and descriptor-order
  failures, whole-NULL versus all-NULL tuples, scalar FROM metadata and genuine
  current-authority calls. Stored constructor/function/operator consumers retain
  old readers through replacement and replay actual emitted SQL after renames.
  Set-returning key/value/pair extraction uses `hstore_rows_helpers_test.go`:
  occurrence bags retain duplicate values and present SQL NULLs, independent
  of hstore traversal order. Fixed OUT names, named `each` input, zero-row
  metadata, held parameter recovery and caller admission share the same public
  scenarios, including denied literal-NULL SRFs. Renamed stored consumers and
  retained outputs survive native replacement; held builtin TEXT parameters
  rebind through `::text::hstore`, while genuinely hstore-typed parameters retain
  the catalog-lifetime 0A000 fence. The actual emitted SQL is replayed on the oracle.
  Update scenarios independently specify right bias, exact pair deletion and
  shape-independent key deletion/slice, including NULL-valued retained pairs.
  All nine procedure/operator roles survive stored replay and genuine schema
  changes. Held builtin TEXT signatures reanalyze across replacement; fresh raw
  hstore results check destination metadata without weakening catalog fences.
