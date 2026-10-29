# Immutable FTS scalar values and algebra

`fts` is a stateless leaf **below `internal/sqltype`**, despite its directory
under `internal/engine`. It imports only the standard library and `sqlstate`.
It owns immutable decoded vector lexemes/positions and query operator arenas,
FTS quoting, grammar, canonical text and released byte/count checks, and the
stateless default document tokenizer. It does not
own SQL descriptors, NULL, source positions, portal context, notices, catalogs,
sessions, dictionaries, ranking or indexes.

## Parsing and rendering

Parse functions check cancellation and consume all input. Vector/query payload
fields remain private. Their `String` returns cached canonical text without
decoding/allocation; `Equal` compares structure, not SQL comparison semantics.
Query association survives even when canonical text erases redundant AND/OR
parentheses. Query parsing/rendering/comparison are iterative. Each parenthesis
frame retains the released 32 pending-operator limit; there is no 32768-node or
Go/C-stack-depth limit.

`InputText` reuses the same cancellation-aware renderer with every operator node
parenthesized, preserving association for logical serialization. Unary frames
let generated NOT chains exceed 32 nodes without exceeding the per-frame parser
limit on replay. `String` stays canonical. Lossless text is computed only when
needed by SQLLiteral, never for SELECT, with no extra cache.

The composed normalization owner in core streams recognized lexical results
through a caller-owned position counter. Recognized empty stopwords advance it,
including trailing stops; unrecognized tokens do not. Scalar document consumers
initialize zero, while multi-source trigger consumers retain one counter across
separate parses, without joining source buffers or reparsing rendered vectors.

`ConvertQuery` reuses strict operand grammar with a borrowed document-normalizer
callback, or builds plain, phrase and forgiving web modes directly into the same
owned arena. Each normalized operand drops stopped edge padding, retains internal
position gaps and contributes one placeholder if entirely stopped. Grammatical
stopword nodes survive complete syntax validation; iterative cleanup propagates
independent left/right phrase widths before rendering once. Constructor limits
apply to emitted lexemes/storage, while raw `ParseQuery` bounds its input operands.
Web empty quoted spans remain operands, including for notice classification;
contextual OR uses input-cursor boundaries and lookahead rather than token equality.
`LexicalResult`/`LexicalPosition` are catalog-free data: recognition, ordered
lexemes, prefix and variant grouping. Query expansion combines source and
dictionary prefixes, ANDs one variant and ORs alternatives. No dictionary
registration, synonym resource or configuration authority belongs here.
Recognized-token position assignment and dictionary-chain continuation belong to
the composed conversion owner in core. This leaf consumes rich lexical positions
without selecting dictionaries, initializing runtimes or emitting notices.

### Document tokenization

`ParseDocument(ctx, text)` returns ordered `DocumentToken{ID, Text}` values.
Tokens share exact immutable input substrings; whole URLs and hyphenated words
intentionally overlap their subsequently emitted components. The ordered action
tables and stack interpreter own tentative recognition, fallback and replay,
including XML/script/style handling. UTF8 classification follows the C locale:
all non-ASCII characters are alphabetic, not Unicode space/digit classes.

Parser state, tentative frames, lookahead and output are invocation-private.
Lookahead uses the same interpreter and shared cancellation budget; nesting is
bounded by the host/path state graph, not input depth. Cancellation checks occur
within long tokens and across backtracking/replay, with no partial result on
error. Document tokens have no vector/query lexeme-size limit. Category SQL
descriptors and parser-name resolution belong to callers, not this leaf.
`DocumentTypes` owns the same token-ID ordered alias/description inventory used
by parser metadata and configuration validation. `VectorBuilder` transfers
invocation-owned lexeme/position entries directly into canonical immutable
vectors; it never renders and reparses vector input to construct a result.
`ParserRuntime` copies checked callback identities without retaining a catalog.
Parsing admits only the actual stock START/GETTOKEN/END combination; descriptor
enumeration independently admits the stock LEXTYPES identity. Unused HEADLINE
does not gate either operation. A different signature-compatible identity fails
the affected native operation explicitly rather than producing default/empty
output. Catalog owners certify SQL signatures before constructing this value.


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

Vector utilities transform the private canonical entries without reparsing text.
Exact selector lookup shares immutable strings and unchanged position slices;
only changed positions and the output entry buffer are owned anew. Weight parsing
consumes an already converted internal byte and emits hard operation errors.
Selector scans, transformations and extraction check cancellation at bounded
intervals; no partial values or execution context escape an operation.

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

Package units cover canonicalization, grammar, limits, algebra and document
tokenization, including literal released regression rows and boundary/fallback
cases. FTS typed values, operators and dump live in `sqltype`, `functions` and
`dump` units. Cancellation units use deterministic interior boundaries in sorting,
duplicate reduction, long document tokens, lookahead and replay, never elapsed time.
Builder kernel units live in `enginetests/` and use deterministic context
boundaries while observing immutable results through the public leaf API. Public SQL
scenarios live in [reference/ranges_text_search](../../../reference/ranges_text_search/README.md#testing).

```sh
make check RUN='^TestFTS'            # FTS units across packages; add RACE=1 for cancellation
make check RUN='^TestReferenceFTS'   # public scenarios; add REFERENCE=1 for the oracle
```
