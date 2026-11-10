# reference/patterns_encoding

Collations and text matching: collation objects, providers, versions and the
collation management functions; expression collation derivation; encoding,
locale and float-presentation settings; `format`, `concat`/`concat_ws` and
`quote_*`; LIKE/ILIKE, SIMILAR TO, POSIX regular expressions and the `regexp_*`
functions. Other string functions live in `reference/strings`; BYTEA
substring/position in [temporal_types](../temporal_types/README.md#testing);
JSONPath `like_regex` in [datetime_formatting](../datetime_formatting/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- **Helpers.** `collation_legacy_helpers_test.go`, `format_helpers_test.go`,
  `posix_helpers_test.go`, `regexp_helpers_test.go`, `similar_helpers_test.go`,
  `string_variadic_oracle_test.go`, the shared regexp row/set/timeout helpers in
  `regexp_{cancel_rows,held_sets,namespace,set_walks,stored_overloads,timeout_rows}_test.go`,
  `like_metadata_test.go`, and `likeReferenceCancelEntered` in
  `like_cancellation_test.go`.
- Transactional helpers reuse `fixture.CollationScenario`, `SequenceCoreConn`
  and `HeldStatement`; native controls use the unwrapped pool.
- Cancellation uses actual provider admission or observed relation-lock admission.
  These prove different boundaries. Reject premature completion, cancel/join
  under bounded cleanup and retain same-owner/held recovery checks.
- Timeout lock witnesses first observe the same finite commands at timeout zero.
  Prepare held statements before arming timers. Relation-lock admission proves
  SQL cancellation, not entry inside a matcher; keep actual provider work separate.
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
