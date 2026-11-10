# Immutable FTS scalar values and algebra

`fts` is a stateless leaf below `internal/sqltype`, despite its engine directory.
It uses only standard/scalar diagnostic facilities, never catalog, sessions,
dictionaries or engine authority. Core owns configured normalization/acquisition.

## Parsing and rendering

Parsers check cancellation and consume all input. Vector/query payloads are
private immutable values; canonical text is presentation, not execution storage.
Document tokens borrow exact immutable input substrings; parser frames, lookahead,
normalization counters and output construction are invocation-private.

Borrowed normalizer callbacks supply lexical results without granting catalog
lookup or callback lifetime authority. Typed continuation metadata stays separate
from SQL array presentation and logical word positions.

## Limits

Released word, position, storage and grammar limits are compatibility contracts.
Long scans, sorting, reduction and tree construction poll cancellation throughout,
not only at entry. Failed construction publishes no partial immutable value.

## Algebra

Operations traverse immutable vector entries/query arenas directly, without SQL
text round trips. Builders own new backing and may share unchanged immutable
strings/positions. Evaluation/ranking/rewrite scratch is private to each call;
matching never stores mutable state in a shared value or recursive runtime host.

### Ranking and headlines

Ranking consumes retained positions/query structure. Headline construction uses
original token/position associations from the configured walk rather than
retokenizing or renormalizing display text. Returned results own their storage.

## Query inspection and rewriting

Inspection and rewrite helpers consume the same typed immutable representation.
Changed results construct new values; no operation retains an evaluation context,
dictionary capability or catalog reference.

## Error authority

Input producers mark only their own recoverable lexer/grammar/limit errors with
`sqlstate` soft-input origin. Cancellation and unrelated runtime errors remain
hard errors; consumers must not infer recoverability from SQLSTATE alone.

## Testing

Pure grammar, immutable algebra, limits and cancellation units live here.
Configured dictionary/runtime ownership belongs to core/tsearch tests; public
SQL lives in the text-search reference packages.
