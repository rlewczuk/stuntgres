# reference/pgstatstatements

Public statement-statistics scenarios share the ordinary dual-mode fixture and
one package-owned oracle. `TestMain` opts into `WithPgStatStatements()`; the
existing pinned PostgreSQL container genuinely preloads the module. Each
scenario explicitly installs version 1.12 in `pgss_part`. Tests use only public
SQL, `database/sql` and `reference/fixture`, never internal engine APIs.

The observer's pinned session disables collection. A separate worker supplies
measured statements. New resolved objects and distinct statement shapes isolate
streams; each counter assertion establishes its matching baseline before work.
Cluster history is not assumed empty. Reset scenarios explicitly assert each
destructive setup/result; other families isolate shapes without using SQL reset
as an unasserted setup helper. All borrowers close before extension/schema/role
teardown. PostgreSQL scenarios run serially because their oracle is shared.

Declaration expectations come from the released 1.12 SQL, independently of
backend metadata. Logical identity/calls/rows and representative text are exact;
generated query IDs are checked through identity relationships, not allocator
values. PostgreSQL physical fields have nonnegative independent expectations;
native columns 19–50 have a typed deterministic-zero boundary owned by the
physical-counter projection part. Collector timestamps are real, not physical
approximations. PostgreSQL's preload/shared-memory ABI and statistics files are
oracle process mechanisms, not native SQL installation or logical collection.

## Testing

`pgstatstatements_fixture_test.go` owns the 52-column contract, isolated
observer/worker sessions and direct/prepared counter readers. The installation
family checks atomic failures, exact diagnostics/notices, owner/member
relationships and public execution authority. Metadata covers empty direct and
prepared descriptions, STRICT NULL, showtext and the physical-field boundary.
Normalization covers literal and parameter routes, first text, UTF8, typed NULL,
constant lists and the released PG18 selective jumble. Effective FROM aliases
separate groups; an identical alias can deliberately merge different resolved
relations. Without aliases, different unqualified relation names separate.
Distinct sentinels independently prove which real relation was read. Calls/rows
cover SELECT, INSERT SELECT, UPDATE, RETURNING, DELETE, zero-row completion and
completed work retained after rollback.

The permanent suffix inventory under `TestReferencePgStatStatements` is:

| Suffix | Contract |
| --- | --- |
| Installation, Metadata | Installed declarations, sealed authority independent of SQL/C entry spelling, all 52 columns, direct/prepared NULL/showtext and named physical boundary |
| Normalization, JSONIdentity, XMLIdentity, XMLSerializeIdentity, CallsRows, Timing | Released structural identity, resolved JSON/XML selected fields and implicit constants, exact completion counts and real numerical timings |
| Planning | Unchanged PostgreSQL generic/custom counts, exact actual native accepted-build counts, SQL PREPARE/EXECUTE and independent real planning/execution phases |
| Failures | Runtime and late multi-row atomicity, admitted direct/prepared cancellation and completed children of failed parents |
| Settings | Module metadata, complete setter diagnostics and effective session/transaction/savepoint restoration |
| TerminalIdentity, TerminalIdentityRepresentative, TerminalIdentityRestoration, TerminalSettings, TerminalInvocation, TerminalUtilityEnable, TerminalNesting | Genuine utility-hook principal and first-text ownership, redaction, transaction versus in-utility restoration, current terminal nesting/policy and separate planning/execution keys, invocation-local restoration/session-SET escape and separately captured utility enablement |
| Utility, UtilityIdentity, AnalyzedUtilityIdentity, CallIdentity | Disabled/enabled utilities, selected transaction kinds/names, typed utility children, authenticated renamed procedures, prepared wrappers, FETCH and genuine table/query streaming COPY with distinct wrapper/independently analyzed child accounting |
| RawUtilityOptionOrder, RoleKindIdentity, NullableUtilityIdentity, RawRangeFunctionIdentity | Ordered raw declaration options, distinct ROLE/USER/GROUP introducers, nullable SQL/MED fields and raw function-FROM versus explicit ROWS FROM provenance, independently of equivalent catalog effects or view values |
| DatabaseIdentity | ALTER DATABASE rename/source/target, ordered options and literal kinds, equivalent TABLESPACE spellings, distinct refresh identity, exact UTF8 batch representative and direct/fresh/held execution counts; Prepare and failures do not publish successful calls |
| Nested, NestedSource | SQL/PL routines, triggers, top/all/none, effective SECURITY DEFINER owner, invocation-setting restoration and exact UTF8 source spans for PERFORM, INTO, loops, dynamic SQL and non-simple scalar subqueries |
| Cluster | Shared cross-database collection without peer installation, pool-close retention and independent Driver isolation |
| Visibility | Own/foreign redaction, inherited statistics-reader authority and immediate revoke on held prepared readers |
| Eviction | 5100 distinct FROM-alias shapes, untied hot survivor, bounded capacity and event—not victim—deallocation accounting |
| InFlightEviction | Real suspended executor survives max5000 pressure; normal partial Close recreates only its actual successful phase without draining or replaying evicted history |
| ResetDeclaration, ResetArguments, ResetDiagnostics, ResetFilters, ResetMinmax, ResetInfo, ResetPrivileges | Genuine defaulted scalar declaration, named/coerced/strict NULL calls, complete diagnostics, cross-role/database filters, all-field cumulative preservation, exact same-server reset timestamps/global outcomes and live ACL admission |
| ResetCompletion, ResetInFlight, ResetControls, ResetFeatureSmoke | Enabled caller1/1 only at actual terminal, old eligible executor completion after SQL reset versus native Clear fences, genuine stored identities/current deparse and native-control lifetimes |
| Controls | Persistent provider/member/owner identities, stored consumers/data, failed replacement atomicity, clean local history and intact peer history |
| ConsumerLifecycle | Ordinary scripted views retain changed definitions, moved identities, comments, ownership/ACLs, actual membership or absence through native replacement; unrelated canonical-name tables and detached historical function cohorts remain independent |
| Protocol | Actual public server/pgconn simple and extended messages, Parse/Describe, volatile portal suspension/resume and explicit close-time accounting, COPY and CopyFail atomicity |
| CursorLifetime, CursorArgumentSource | PL cursor planning, partial/unused closure, failed-executor rejection, completed children retained after parent failure and declaration-ordered named argument source provenance |
| FeatureSmoke | Literal grouping, held execution, real timings, failed write, foreign visibility, STRICT metadata and native persistent control roundtrip |

DatabaseIdentity executes each ALTER directly, freshly prepared and twice through
one held statement. Catalog identity/flags/limits, rename effects, same-space
metadata and refresh notices are asserted after each execution. The pinned
PostgreSQL 18.6 collector permanently zeros a retained raw utility plan's query
ID: direct/fresh/first-held contribute three calls, while the second held command
still executes but contributes none. The narrowly named
`heldRawUtilityCollectedCalls` expectation records that oracle defect; native's
invocation-owned collector must retain all four calls. Neither execution is
skipped or retried, and Prepare/failures still contribute none. See
[the released source and observed proof](../../docs/POSTGRES_REGRESSION.md#held-raw-utility-plans-lose-their-statement-statistics-identity).

Controls and FeatureSmoke keep native host replacement phases explicit in the
shared definitions. They exercise public Dump/Reset/Restore rather than SQL reset
overloads. Held rows remain owned across replacement; private SQL state and
collector history do not become dumped data. Protocol uses the existing public
server fixture and real frontend messages, not internal collector calls.
Reset cancellation observes genuine queued argument-relation admission before
the body, then joins cancellation. Its Admin monitoring legitimately adds cluster
history, so only these monitored cases compare both seeded A/B rows in all52
fields plus full info; all other reset failure cases compare complete history.
Utility SET, named DEALLOCATE and FETCH check exact representatives through
independently expected per-execution call/row deltas, including history retained
across installation replacement. Named deallocation is collected with its name
normalized, while SQL PREPARE/EXECUTE charge the prepared body. Protocol separates
each Execute completion tag from terminal ExecutorEnd accounting at portal
disposal. Normal close of a suspended portal records only actually processed
rows and does not drain volatile effects. Closing an initialized but unused
SELECT executor records one successful call with zero rows and no volatile
evaluation; Bind itself records only its successfully accepted planning work.
COPY(query) preserves its whole COPY representative for both genuine frames:
under `track=all`, outer true and inner false each record one call and three
processed rows, with distinct nonzero query IDs. `top` admits only the wrapper;
utility off with `all` admits only the child, and utility off with `top` neither.
Failed query execution adds no successful calls or rows to either frame.

Terminal identity follows the actual successful hook, not the later response-time
session snapshot: SET/RESET ROLE, session authorization and DISCARD can change
the utility owner, whereas transaction-control hooks precede command-end role
restoration. Fresh terminal keys receive cleaned raw source; existing keys keep
their first representative. Executor completion checks terminal tracking policy;
utility collection retains its separate early enable decision. Function-local
set_config restores the caller, while session-level SET can escape function
configuration. Successful planning remains independent of suppressed execution.
Current terminal nesting governs both execution eligibility and its top-level
key. SQL CLOSE is nested inside its utility hook even with utility collection
off; command-end COMMIT cleanup is outside that hook. A retained cursor can
therefore keep its nested planning entry and add a separate top-level execution
entry with the same query ID, without duplicating calls or draining extra rows.

Native has no cost-based generic/custom chooser: each real successful native
build contributes a plan, while accepted portal continuation contributes none.
The held table-variable witness therefore measures native plans2/calls2 then
plans3/calls2 after a true runtime failure, under either stored force mode;
PostgreSQL keeps its independent generic1/customN expectations. Plain native
constant arithmetic retains its existing row-owned error demand after planning.
`Protocol/failed_static_bind` instead exercises a genuine static planning error:
syntax-only Parse/Describe, exact Bind22012, zero successful phases, Sync recovery
and no usable portal. No error is moved between phases for a statistic.

NestedSource retains exact per-executor representative text. Its loop-body
literal numbering is independently specified for the genuine bindings:
PostgreSQL's `r.k` is external datum parameter 5, so its constant is `$6`;
native's local composite-field expression has no external parameter, so its
constant is `$1`. This changes neither source spelling outside that placeholder
nor logical identity assertions, calls, rows, result values or metadata.


Use the family selector in both modes:

```sh
make check RUN='^TestReferencePgStatStatements'
make check REFERENCE=1 RUN='^TestReferencePgStatStatements'
```

Follow [docs/TESTING.md](../../docs/TESTING.md). The shared backend loop is
unfiltered; missing native behavior cannot prevent the sibling PostgreSQL
subtest from observing the independently specified contract. No recorder learns
expectations; reset scenarios exercise genuine public SQL, never collector mocks.
