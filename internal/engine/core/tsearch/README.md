# internal/engine/core/tsearch

The text-search runtime: tsvector and tsquery conversion (text and JSON
documents), `ts_debug`, `ts_lexize`, `unaccent`, parser resolution for
`ts_parse`/`ts_token_type`, the `ts_rewrite` and `ts_stat` nested SQL, and
dictionary and thesaurus acquisition. It is a leaf below core (ENGINE-SPLIT
§3, L14.5); the immutable codecs, parsers and vector builders are
[`engine/fts`](../../fts/README.md).

## Layering

- Imports only packages below core: `access`, `authorization`, `catalog`,
  `ddl`, `exec`, `extension`, `fts`, `ids`, `regobj`, `session`, `stored`,
  `parser`, `sqlstate`, `sqltype`, `util`, `workadmission`. It never imports
  core, a family or the root.
- Core reaches it from `textSearchHost`, whose `exec.TextSearchRuntime`
  methods call the entry functions with the statement host, and from the
  tsvector trigger (`trigger_builtin.go`), which walks a `Capture`. The
  session's thesaurus initializations are a `ThesaurusMemos` in core's
  `sessionPayload`.

## Contract

- `Convert` (to_tsvector, to_tsquery and its plain, phrase and websearch
  forms), `ConvertJSON`, `Debug`, `Stat`, `Rewrite`, `Lexize`, `Unaccent` and
  `ResolveParser` are the bodies of `exec.TextSearchRuntime`.
- `CaptureConfiguration` captures one configuration and its parser from an
  immutable catalog read; `Capture.Walk` tokenizes and lexizes a text,
  acquiring each consulted dictionary on demand; `Capture.Accept` is the final
  currentness and cancellation fence that publishes new thesaurus receipts.
  `Captured` reports whether a configuration is held.
- `ThesaurusMemos` maps a dictionary to one successful thesaurus
  initialization of the session; its entries stay opaque to core.

## Host and authority

- `Host[A]` is the statement a call runs in; core implements it on
  `statementHost` (the shared leaf method set). It gives the operation
  (context, settings, notices), the I/O environments (the captured regconfig
  input), work admission, the Engine.Mu pair, the pending catalog journal and
  the private catalog over it, the operation's authorization, the namespace
  authorization's catalog and regconfig/regdictionary resolver, the
  thesaurus compiler's dictionary host, the session's `ThesaurusMemos`, and
  the `exec.NestedSQL` capability.
- `Authority[A]` is that authorization, held by value: a `ddl.Authority` for
  `extension.CaptureThesaurusInput`, `Access` for the current role, and
  `Recaptured` over another catalog read. The package is generic over it, as
  `analyze.PathInput` is, so capturing and recapturing it never allocates.
  Core's `AuthorizationView` implements it.

## Locks

- Calls are entered with no engine lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it; the host methods
  named …Locked, `tsAcquisition.currentLocked` and the thesaurus checks run
  inside those windows. `ThesaurusMemos` belongs to `Engine.Mu`.
- Parser, dictionary and resource compilation, lexization, notices and nested
  SQL run with nothing held. Engine.Mu precedes the effective settings mutex.

## Behaviour

Text-search conversion captures an immutable catalog generation and configuration
identity under `Engine.Mu`, then releases it before runtime acquisition,
tokenization, resource compilation, lexization or notice callbacks. Capture does
not initialize mapped dictionaries. Omitted configuration resolves the current
`default_text_search_config` name from operation-effective settings at execution,
with Engine-before-settings lock order and semantic missing-schema/configuration
errors. Mapping changes publish through ordinary schema stages and definition locks.
JSON conversion validates its filter first, then resolves an omitted default
identity before traversal, even if no item is selected. Explicit configurations
retain lazy identity demand. Both forms acquire parser/dictionary runtime only
at the first selected item, including an empty string. All selected items share
one immutable catalog generation and acquisition, but reset parser/phrase state
per item. One cumulative position counter includes stopwords; only an item
emitting lexemes adds an artificial gap. No selected item yields a typed empty
vector without dictionary receipts. Traversal, lexical work and final acceptance
remain detached and cancellation-fenced; no partial vector or acquisition escapes
a failure.
Debug borrows the same coherent capture/default resolver and acquisition fence,
but independently lexizes every mapped dictionary against each original token.
Its complete ordered mapping arrays and recognized-empty/unknown distinctions
never inherit conversion's filtering, phrase or token-suppression walk.
Debug rows and acquired dictionary runtimes belong to one invocation; nothing
is published before currentness and cancellation are checked.
Provider failures inside debug acquire the actual SQL-standard function's
statement context through the shared immutable SQL routine diagnostic helper;
caller argument-input failures remain outside that body frame.

`textSearchSQL` owns the shared rewrite/statistics nested cursor scope over the
`exec.NestedSQL` capability: ordinary settings-aware parse, complete
preparation, ACL/planning and read-only cursor admission use the caller's
private catalog/data with no outer parameters.
Stat enables canonical executable-binding `readOnlySPI` classification before
description: derived/stored row marks are rejected, while unused analysis-only
locking CTEs are not executable marks. Non-cursor command-kind precedence remains
separate. Rewrite keeps its existing syntax-gate admission rather than inheriting
stat's stronger SPI demand; lexical restoration includes both demand flags.
The consumer closes rows before lexical restoration; only prepare/open failures
gain SPI diagnostic enrichment. `nestedCursor` is all-row demand, not cached SQL.
Runtime acquisition occurs only when the actual ordered token chain consults a
dictionary; invocation-local reuse avoids repeated acquisition. Empty input,
unused categories and dictionaries after recognizing nonfilters (including
stopwords) create no initialization receipts. Phrase continuation tests mapping
membership without acquiring that category's other dictionaries. Detached demand
and final result acceptance fence the captured configuration, parser and consulted
executable inputs; mismatch returns ordinary `ErrCatalogReplaced`, without retrying
callbacks or notices. Newly initialized receipts remain invocation-private until
the complete result passes currentness and cancellation checks, so rejected
conversions publish neither a vector/query prefix nor a prefix of new receipts.
Thesaurus initialization belongs to the physical session's `sessionPayload`, with
at most one successful immutable runtime per current dictionary definition.
Namespace USAGE authorizes initialization/reinitialization, not each unchanged
invocation: ACL-only revocation leaves that session's runtime usable, while new
sessions independently authorize initialization. Receipts check exact executable
inputs, catalog domain/epoch, effective role/path and logical source identity.
Changed executable inputs require new resolution/authorization and detached
compilation followed by a current-input recheck; failed refresh cannot return
stale state. Errors are never installed or transferred between sessions.
The conversion chain owns head/continuation cursors and private phrase matchers,
saved longest completion, dictionary resume positions and suffix replay. Unmapped
categories are skipped; a mapped incompatible category discards saved completion.
Continuation searches the whole next mapping and bypasses its earlier filters.
Oversized parser tokens emit their notice on first encounter, including phrase
lookahead. A per-walk token-index high-water mark prevents duplicate notices
when a saved completion replays a suffix; skipped tokens demand no dictionary.
Unsuccessful multiword continuation resumes the remaining head chain with the
original token; immediate nonrecognition retains preceding filter transformations.

`fts_query_runtime.go` executes SQL rewrite rules inside the caller's admitted
operation, transaction and statement snapshot. Lexical scratch and zero inner
parameters are scoped stackwise; private staged writes remain visible. The
ordinary parser, analyzer, admission and query producers supply exact typed rule
rows, never a driver connection or a second SQL executor. The consumer fetches
100 rows before checking result shape and retains one bounded typed rule batch.
Deletion stops rule application, but the next batch is still fetched before
testing emptiness again. Original fetch errors remain distinct from prepare/open
errors. The cursor closes before restoring caller scratch; outer publication
remains eager.

Dictionary execution captures and validates the current private descriptor under
`Engine.Mu`, then runs immutable lexization after releasing it. Installed
`unaccent(text)` resolves the dictionary named `unaccent` in the function's current
schema; mutable member names are not a replacement for that lookup.

## Testing

- The runtime's engine tests stay in core until their census owners move
  them (`RUN='^Test(FTS|TS|Thesaurus|Dictionary|Ispell|TriggerBuiltins)'` over
  `./internal/engine/core`); census-owned text-search tests are in
  `core/enginetests/textsearch`. Public scenarios are
  `RUN='^TestReference(FTS|Dictionary|Thesaurus|TextSearch|TriggerBuiltins)'`.
- `thesaurus_demand_test.go` (core) reads the session's receipts by the field
  names `thesaurusMemos`, `runtime` and `normalizer`; renaming them changes it.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/tsearch/enginetests`.
