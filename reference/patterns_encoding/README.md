# reference/patterns_encoding

Collations and text matching: collation objects, providers, versions and the
collation management functions; expression collation derivation; encoding,
locale and float-presentation settings; `format`, `concat`/`concat_ws` and
`quote_*`; LIKE/ILIKE, SIMILAR TO, POSIX regular expressions and the `regexp_*`
functions. Other string functions live in `reference/strings`; BYTEA
substring/position in `reference/binary_json_arrays`; JSONPath `like_regex` in
`reference/binary_json_arrays`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- **Helpers.** `collation_legacy_helpers_test.go`, `format_helpers_test.go`,
  `posix_helpers_test.go`, `regexp_helpers_test.go`, `similar_helpers_test.go`,
  `string_variadic_oracle_test.go`, the shared regexp row/set/timeout helpers in
  `regexp_{cancel_rows,held_sets,namespace,set_walks,stored_overloads,timeout_rows}_test.go`,
  `like_metadata_test.go`, and `likeReferenceCancelEntered` in
  `like_cancellation_test.go`.
- **Collation transactions.** Reuse `fixture.CollationScenario`,
  `SequenceCoreConn` and `HeldStatement` for owner/peer, SQL BEGIN and Go BeginTx
  routes. Keep actual collation behavior coupled to identity/provider/version
  metadata, conditional notices and full/savepoint rollback. Conflict scenarios
  use the shared deterministic lock observers and join every worker; native
  controls use unwrapped pools and replay the actual committed-only Dump.
- **Cancellation admission** uses one of two patterns; both reject early
  completion, join workers under bounded cleanup and check nil outputs, the
  sentinel and same-owner/held recovery:
  - *Native stack observer*: LIKE uses `fixture.BinaryConversionActiveCancel`,
    whose context holds an actual provider cancellation poll until the observer
    sees that provider and its `activeProviderCancel` worker in the same live
    stack and cancels it. Finite normalization cannot escape between samples.
    POSIX and regexp retain `likeReferenceCancelEntered`, matching the provider
    and `likeReferenceCancelEntered.func1` in one live stack, with creation
    metadata excluded. Their workloads must keep the provider busy between
    samples. PostgreSQL observes the server command.
  - *Relation-lock admission* (SIMILAR, `regexp_substr`): the regex-bearing UPDATE
    waits behind `LOCK TABLE ... IN ACCESS EXCLUSIVE MODE` before cancellation.
    This proves admitted SQL cancellation, not provider entry; matcher-level
    cancellation is covered by internal units (`TestSimilarActualMatcherCancellation`,
    `TestRegexpSubstrCancellation`).
- **Regexp statement timeout.** All six scalar/set-returning commands, direct
  and held, use the finite row `s='a',p=''` behind a durable ACCESS EXCLUSIVE
  relation blocker with the unchanged 10 ms timer. A separate timeout-zero
  witness strictly observes those same commands queued, releases the blocker,
  drains/closes bounded finite results and checks full transactional state and
  same-physical-owner recovery. This proves SQL admission, not matcher entry;
  `TestReferenceRegexpActualCancellation` independently retains real provider
  work/cancellation and PostgreSQL streaming coverage.
- **Frame names matched by tests.** `sqltype.matchLike`,
  `sqltype.LikeEscapePlan.Evaluate`, `sqltype.(*PGRegexProgram)` methods and the
  test workers `activeProviderCancel` and `likeReferenceCancelEntered.func1`.
  Update the probes when renaming them.
- **Resource limits.** Regex work budgets are tested only by internal units with
  injected limits; reference tests never assume a portable complexity cutoff.
- **Replay errors.** Expected errors during PostgreSQL emitted-SQL replay run
  inside savepoints and roll back to them, so later assertions run in a healthy
  transaction.

Add `REFERENCE=1` for the oracle.

- Collation objects, providers, versions, TEMP and native collisions: `RUN='^TestReferenceCollation'`
- Collation management functions: `RUN='^TestReferenceCollationFunctions'`
- Expression collation derivation and demand: `RUN='^TestReferenceExprCollation'`
- Encoding, locale and float-presentation settings: `RUN='^TestReferenceFormatEncoding'`
- `format` (also selects `FormatEncoding`): `RUN='^TestReferenceFormat'`
- `concat`, `concat_ws`, `quote_ident`/`quote_literal`/`quote_nullable`: `RUN='^TestReferenceStringVariadic'`
- LIKE and ILIKE: `RUN='^TestReferenceLike'`
- SIMILAR TO and SQL-regex `substring`: `RUN='^TestReferenceSimilar'`
- POSIX operators and `substring`: `RUN='^TestReferencePosix'`
- `regexp_*` functions: `RUN='^TestReferenceRegexp'`
- `regexp_substr`: `RUN='^TestReferenceRegexpSubstr'`
