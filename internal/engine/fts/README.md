# Immutable FTS scalar values and algebra

`fts` is a stateless leaf **below `internal/sqltype`**, despite its directory
under `internal/engine`. It imports only the standard library and `sqlstate`.
It owns immutable decoded vector lexemes/positions and query operator arenas,
FTS quoting, grammar, canonical text and released byte/count checks. It does not
own SQL descriptors, NULL, source positions, portal context, notices, catalogs,
sessions, normalization, matching, dictionaries, ranking or indexes.

Parse functions check cancellation and consume all input. Payload fields remain
private. `String` returns cached canonical text without decoding/allocation;
`Equal` compares structure with cancellation, not SQL comparison semantics.
Query association survives even when canonical text erases redundant AND/OR
parentheses. Query parsing/rendering/comparison are iterative. Each parenthesis frame
retains the released 32 pending-operator limit; there is no invented 32768-node
or Go/C-stack-depth limit.
`InputText` reuses the same cancellation-aware renderer with every operator node
parenthesized, preserving association for logical serialization. Unary frames
also let generated NOT chains exceed32 nodes without exceeding the unchanged
per-frame parser limit on replay. Canonical `String` is unchanged. Lossless text
is computed only when needed by SQLLiteral, never for SELECT, with no extra cache.


Vector positions model the pinned 64-bit release's decimal conversion: signed64
saturation, signed32 narrowing, upper clamp and fourteen-bit masking. They do
not use Go integer-overflow rejection. Positions are sorted/deduplicated, with
highest weights except the released early cutoff at the 256th distinct position.
Vector raw token starts and final aligned storage allow 1048575 bytes; query
text-input operand starts (counting each preceding operand's NUL) must be below1048575.
Decoded words allow 2046 bytes. Allocation-size arithmetic uses the released
12-byte query item/8-byte header and MaxAllocSize, not Go object layout.
Vector canonicalization checks cancellation during lexeme/position sorting and
duplicate-position reduction, not only lexing. Stable ordering preserves the
released first-weight cutoff behavior. Sorts use bounded128-item library runs
and cancellation-aware merges, with checks at most256 copied/scanned items
apart. Small or already ordered inputs need no scratch allocation; larger
unordered inputs share one half-sized merge buffer per sort. Repeated positions
have no new input-count cap.

Query AND/OR/NOT/phrase operations copy and relocate immutable postorder arenas,
share word strings and retain association/repetition without text re-input.
Generated operands serialize right-first and permit a start exactly1048575,
rejecting larger starts independently of input limits. Generated trees do not
inherit the text parser's pending-operator bound. Phrase validates0–16384 before
empty identities. Contains uses an operation-local exact lexeme set, ignoring
Boolean structure, flags and multiplicity; it owns no SQL NULL semantics.

Vector Concat scans the global left position maximum, counts the exact merged
lexeme capacity and performs a linear merge. Immutable unmodified strings/position
slices are shared; shifted/merged positions have independent exact ownership.
Left positions win, then shifted right positions append until256/16383, retaining
the first saturated weight rather than input max-weight merging. Final aligned
storage is checked before rendering. Operation scans/copies/rendering check
cancellation at bounded intervals, return no partial result and never mutate inputs.


## Testing

Root runs selected units (external 900-second deadline):

```sh
make check RUN='^TestFTS(VectorCanonical|VectorCanonicalCancellation|VectorLimits|QueryGrammar|QueryStructure|QueryLimits|CodecCancellation)$'
make check RUN='^TestFTSTyped(Value|Metadata|Scope)$'
make check RUN='^Test(PGTypeCaptureOwnership|PGTypeInstalledDomainAuthority|AnyValueProviderDatums|OrderingUsage)$'
make check RUN='^TestFTSOps(QueryAlgebra|PhraseBounds|Containment|VectorConcat|Limits|Cancellation|TypedPlan|ProviderLifecycle|RoutineDeclarations)$'
make check RUN='^TestFTSOpsCancellation$' RACE=1
```
The ownership inventory admits both scalar and metadata-only companion
identities. Generic ANY_VALUE units exercise real FTS payload carriers, including
empty values and distinct query associations; ordering units independently
require FTS to remain non-sortable. These are package units, not SQL providers.
`TestFTSVectorCanonicalCancellation` enters post-lexing canonicalization directly
and deterministically cancels during lexeme sorting, position sorting and a long
duplicate-position scan. It checks actual partial sorting/reduction progress,
not elapsed time or cancellation consumed by the lexer. Vector canonical cases
also retain a late weight after4096 duplicate positions; existing cutoff units
retain the independently specified first weight at the256th distinct position.


The public shared-body scenarios live in `reference/types`, not here. Feature
smoke, in both modes, is:

```sh
make check RUN='^TestReferenceFTSTypesFeatureSmoke$'
make check RUN='^TestReferenceFTSTypesFeatureSmoke$' REFERENCE=1
```

Expected: canonical weighted/positioned vector and compound query rows, held
string/byte/NULL/empty binds, exact NOTICE and malformed/NOT NULL diagnostics,
statement atomicity, native Dump/Reset/Restore/redump and retained-cursor output.
Real mode also executes the actual native dump on pinned PostgreSQL. See that
package's README for the complete selected inventory and explicit physical-stack,
runtime-folding and oversized-pq-error distinctions. Selected checks do not
replace root's whole-project quickcheck, review or post-integration finalcheck.
