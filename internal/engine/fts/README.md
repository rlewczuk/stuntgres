# Immutable FTS scalar values and algebra

`fts` is a stateless leaf **below `internal/sqltype`**, despite its directory
under `internal/engine`. It imports only the standard library and `sqlstate`.
It owns immutable decoded vector lexemes/positions and query operator arenas,
FTS quoting, grammar, canonical text and released byte/count checks. It does not
own SQL descriptors, NULL, source positions, portal context, notices, catalogs,
sessions, dictionaries, ranking or indexes.

## Parsing and rendering

Parse functions check cancellation and consume all input. Payload fields remain
private. `String` returns cached canonical text without decoding/allocation;
`Equal` compares structure with cancellation, not SQL comparison semantics.
Query association survives even when canonical text erases redundant AND/OR
parentheses. Query parsing/rendering/comparison are iterative. Each parenthesis
frame retains the released 32 pending-operator limit; there is no 32768-node or
Go/C-stack-depth limit.

`InputText` reuses the same cancellation-aware renderer with every operator node
parenthesized, preserving association for logical serialization. Unary frames
let generated NOT chains exceed 32 nodes without exceeding the per-frame parser
limit on replay. `String` stays canonical. Lossless text is computed only when
needed by SQLLiteral, never for SELECT, with no extra cache.

## Limits

Vector positions model the pinned 64-bit release's decimal conversion: signed64
saturation, signed32 narrowing, upper clamp and fourteen-bit masking. They do
not use Go integer-overflow rejection. Positions are sorted/deduplicated, with
highest weights except the released early cutoff at the 256th distinct position.
Vector raw token starts and final aligned storage allow 1048575 bytes; query
text-input operand starts (counting each preceding operand's NUL) must be below
1048575. Decoded words allow 2046 bytes. Allocation-size arithmetic uses the
released 12-byte query item/8-byte header and MaxAllocSize, not Go object layout.

Vector canonicalization checks cancellation during lexeme/position sorting and
duplicate-position reduction, not only lexing. Stable ordering preserves the
released first-weight cutoff behavior. Sorts use bounded 128-item library runs
and cancellation-aware merges, with checks at most 256 copied/scanned items
apart. Small or already ordered inputs need no scratch allocation; larger
unordered inputs share one half-sized merge buffer per sort. Repeated positions
have no input-count cap.

## Algebra

Query AND/OR/NOT/phrase operations copy and relocate immutable postorder arenas,
share word strings and retain association/repetition without text re-input.
Generated operands serialize right-first and permit a start of exactly 1048575,
rejecting larger starts independently of input limits. Generated trees do not
inherit the text parser's pending-operator bound. Phrase validates 0–16384 before
empty identities. Contains uses an operation-local exact lexeme set, ignoring
Boolean structure, flags and multiplicity; it owns no SQL NULL semantics.

Vector Concat scans the global left position maximum, counts the exact merged
lexeme capacity and performs a linear merge. Immutable unmodified strings/position
slices are shared; shifted/merged positions have independent exact ownership.
Left positions win, then shifted right positions append until 256/16383, retaining
the first saturated weight rather than input max-weight merging. Final aligned
storage is checked before rendering. Operation scans/copies/rendering check
cancellation at bounded intervals, return no partial result and never mutate inputs.

## Query inspection and rewriting

`NodeCount` reads the immutable arena length. `IndexableTree` reduces NOT
subtrees to TRUE without Boolean simplification or rewrite ordering.
`RewriteWorkspace` owns transient indexed nodes and right-first child spans;
AND/OR flattening, signed legacy-CRC ordering, multiset matching and binary
reconstruction are iterative and cancellation-aware. Direct rewriting protects
inserted roots from the same rule. SQL consumers use `ApplyRule` to clear that
protection and normalize between rows. `Empty` reads the current root in O(1)
without mutation or reconstruction; `Finish` emits one immutable arena and
canonical text. Empty substitutes delete; SQL NULL remains outside this package.
No transformation reparses rendered text.


## Error authority

Owned input lexer/position/operand errors and released text-input word/storage
limits carry the shared `sqlstate` soft-input origin, allowing COPY/JSONB
conversion seams to recover without class-wide 42601/54000 rules. Cancellation
and the defensive `XX000 tsquery stack too small` path retain hard authority. The
parser does not catch arbitrary errors or own command-level recovery; CHECK
expression execution is made hard by the scoped domain adapter. The shared
vector-size diagnostic is unmarked; only the two parser size checks attach soft
origin. Algebra's Concat size failure retains its own hard operation authority.

## Testing

Package units cover canonicalization, grammar, limits and algebra; FTS typed
values, operators and dump live in `sqltype`, `functions` and `dump` units. The
cancellation units cancel deterministically inside sorting and duplicate
reduction and check actual partial progress, never elapsed time. Public SQL
scenarios live in [reference/types](../../../reference/types/README.md#testing).

```sh
make check RUN='^TestFTS'            # FTS units across packages; add RACE=1 for cancellation
make check RUN='^TestReferenceFTS'   # public scenarios; add REFERENCE=1 for the oracle
```
