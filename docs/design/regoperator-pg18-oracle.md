# PostgreSQL 18 operator identifier expectations

## User-operator supersession

PG18-FUNC-OPERATOR-USER supersedes historical native CREATE OPERATOR42601 and
PostgreSQL-only ExternalBoundary statements below. The original builtin alias
inventory and numeric unknown/fixed-identity contracts remain; known allocated
custom aliases now replay by exact qualified signature, including nested mixed
containers. Shared reference definitions cover actual providers, owned shells,
schema identity changes, dependency closure and emitted-script replay. Earlier
numbered evidence remains historical, not proof of this production cutover.


## Authority and construction

`regoperator-pg18-inventory.tsv` is an independent expectation artifact, constructed before any production operator inventory or generator. Its 799 data rows are all records in released [REL_18_0 pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_operator.dat), sorted by numeric OID. Operand symbolic names resolve against released [pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat), including its `array_type_oid` companion identities. Zero is the absent operand, not a type or SQL NULL. The catalog defaults namespace to pg_catalog (11); see [pg_operator.h](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_operator.h).

Fields are raw tab-separated values: oid, namespace, name, left_oid, right_oid, input_signature, regoper_text, regoperator_text. Signatures preserve ordered operands. Input signatures qualify both operator and operand identities with pg_catalog; arrays spell their element name followed by brackets; the internal single-byte character type is quoted `"char"`. Output canonical type names follow [format_type.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/format_type.c), not native codecs. Bare NONE represents operand zero. Every name with more than one released signature formats regoper as `pg_catalog.<symbol>`; unique names omit qualification on the ordinary default path. Regoperator always contains the canonical exact two-slot signature. Operator symbols themselves are not quoted.

No local development inventory, native output, executable provider list, or production table supplied these expectations. They deliberately include geometry, network, full-text, array and polymorphic operand identities without asserting that native execution/storage codecs exist for those operands.

**Evidence status:** root ran the initial selector against the genuinely started pinned PostgreSQL 18.0 container; the command exited 2. Complete evidence is in `.cswd/parts/PG18-TYPE-REG-OPERATOR/tests-1.md` (including the oracle output). Root parsed the full inventory failure: 799 observed rows and 799 expected rows, with zero semantic differences after numeric text normalization. This grounds the entire released identity bag (OID, namespace, name, ordered operand OIDs), not yet every canonical alias text or exact input signature: those assertions were blocked by the OID expectation representation.

Observed pq alias transport is owned `[]byte`, with SQL NULL as nil; both aliases and OID report `ScanType interface{}`, `Nullable false/false`, `Length 0/false`, and `DecimalSize 0,0/false`. The general fixture normalizes pq OID bytes to decimal strings, while native OIDs remain int64. Expectations now preserve those raw transports using the existing family-local OID convention; the general fixture is unchanged.

The complete observed operand diagnostics are encoded independently: quoted NONE and unknown types 42704, missing operand schema 3F000, invalid numeric precision 22023, and numeric operand spelling 42601 with `invalid type name "123"` Context. Literal Position is 8; all observed inner fields are absent. Boolean/numeric casts are 42846 at Positions 12/20, and negative bigint casts are 22003 `OID out of range` without Position. Private-domain denial is 42501; the filtered unqualified operand is 42704, both with actual inferred-parameter portal Context and no Position. No native output supplied these expectations.

Root's second pinned oracle run exited 2; complete evidence is in `.cswd/parts/PG18-TYPE-REG-OPERATOR/tests-2.md`. Seven inventory outputs (OIDs1797 and1804–1809) use `bit varying`, not `varbit`; the TSV now encodes those canonical output strings while retaining the valid qualified `pg_catalog.varbit` input identities. The other inventory alias/signature assertions produced no failures in that run.

Run2 observed typed-nil `[]byte` bound to held regoper as SQL NULL in both alias and OID cells. This is now an explicit shared NULL expectation. The subsequent regoperator Prepare failed with pq's unexpected Parse response because the preceding discovery cursor had read only one row and deferred Close until the whole scenario ended. The regoperator typed-nil result was therefore **unobserved**. Prepared tests now scope statement ownership per alias, consume/close cursors before statement reuse or the next Prepare, check iteration/close errors, and assert successful reuse after typed-nil binds. Only regoperator's unknown typed-nil result remains a temporary discovery observation.

The real custom DROP record is encoded in full: ERROR/ERROR, SQLSTATE2BP01, primary `cannot drop operator #=#(integer,integer) because other objects depend on it`, Detail `view identity_view depends on operator #=#(integer,integer)`, Hint `Use DROP ... CASCADE to drop the dependent objects too.`, and all remaining logical diagnostic fields absent. The shared fixture compares the complete record, not a SQLSTATE-only or substring assertion.

## Shared definitions and first discovery

The definitions in `reference/type_resolution/regoperator_*_test.go` reuse the existing public fixture/driver/control APIs. Nine required top-level entrypoints exist. Root completed pinned discovery in tests-4: every selected definition passed, including regoperator typed-nil byte input as SQL NULL. The temporary PostgreSQL-only filter and observation logging were removed before tests-5 recorded the failing native-before-production regression (unsupported alias column type).

```sh
make check REFERENCE=1 RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|NamespaceAuthorization|Inventory|ExternalBoundary|PreparedLifetime)$'
```

Root applies an external 900-second deadline and retains complete output. Known diagnostics assert full records independently for literal Query, actual literal Prepare, inferred direct/held alias parameters and runtime text casts. Resolved metadata, diagnostics, typed-nil values for both aliases and custom DROP expectations are permanent assertions.

The oracle is the existing pinned PostgreSQL18.0 UTF8/C image; no fixture configuration is changed. A real catalog projection phase asserts every released namespace11 row's name and ordered operand OIDs. Shared parameterized alias SQL exercises every numeric identity's two formatters and every qualified exact input signature, using this independent TSV rather than a production catalog.

Private-schema ownership transfer proves authorized missing42883 versus denied42501, including explicit operator namespaces and private-domain operands, without requiring native operator DDL or unsupported schema GRANT. ExternalBoundary creates genuine PostgreSQL custom operators using existing builtin implementation functions, checks quoted-schema output, signature masking and cross-namespace overload ambiguity, and observes real stored-constant DROP dependency behavior. Its eventual native branch is a failure-only unsupported DDL witness, not a simulated custom provider.

## Subsequent gates

Production implementation was authorized after the tests-5 regression. The native CREATE OPERATOR failure was independently observed as ERROR/ERROR, SQLSTATE42601, primary `syntax error at or near \"OPERATOR\"`, Position8, with all other logical fields empty. ExternalBoundary now asserts that full record and unchanged sentinel data. Production verification remains root-owned and is not implied by implementation readiness.

Persistence and FeatureSmoke are present but excluded from initial discovery: they need actual native emitted Dump bytes before PostgreSQL replay. They define scalar data/default/CHECK/domain/view persistence, overloaded regoper551's ambiguity trap, retained symbolic strings/metadata across replacement, stable redump, fresh-engine Restore, bad staged Restore atomicity and ordinary cancellation. Numeric typed dump is the selected lossless production behavior; these tests do not implement it.

Fixture route corrections preserve these SQL expectations: actual `current_database()` values are identifier-quoted with doubled embedded quotes before becoming alias-name components, so mixed-case native database identities are not folded. Persistence UPDATE RETURNING, view and default-expression reads explicitly assert native string versus PostgreSQL byte-slice alias transport with `RegtypeRaw`; NULL-only alias reads assert SQL nil through the same helper. Direct stored-domain reads (including empty results) retain base `REGOPERATOR` metadata and raw transport assertions alongside the existing explicit base-cast reads. A typed domain NULL is also checked directly and prepared. These are explicit family-local assertions, not generic fixture normalization changes; their subsequent dual-mode verification remains root-owned.

Final selected native and real commands add `Persistence|FeatureSmoke`; feature smoke selects `^TestReferenceRegOperatorFeatureSmoke$` in each mode. Internal descriptor units for genuine mixed-case schema output and catalog completeness belong to the later authorized production round. Public custom operator DDL/catalog projection/executable provider support remains explicitly outside native scope.

## Review-remedy parser and prepared provenance

The independent parser order follows released
[REL_18_0 regproc.c, parseNameAndArgTypes](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/regproc.c):
split qualified-name syntax, closing delimiter, then scan and resolve each current
operand before advancing. Parentheses and brackets use the released combined depth
counter. FUNC_MAX_ARGS is checked after resolving the over-limit operand. The
[released scanner_isspace](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/scansup.c)
accepts only space, tab, LF, CR, VT and FF; actual NBSP stays in the type spelling.

Root's tests-13/14 ground the new complete records before production remediation.
In particular, `+(,no_such_regop_type)` reports `42601: invalid type name ""`,
whereas a trailing comma after a valid operand reports `22P02: expected a type name`.
`a..+(integer` reports invalid-name syntax before a missing right delimiter, and
`+(no_such_regop_type,)` reports the first missing type before the empty tail.
NBSP before/after NONE or integer and an NBSP-only body report missing types, not
an accepted builtin or an empty argument list. `ParserEdges` asserts full records
with the existing source and parameter annotations on all six routes.

The existing shared REGTYPE cell now carries ordered resolved operand ObjectRefs
and revisions for named REGOPERATOR literals, also feeding the ordinary binder
dependency manifest. Direct shared-cell validation is independently exercised by
`TestRegOperatorPreparedOperandManifest`; NONE/numeric/name-only dependency
boundaries are explicit units. Real internal typed descriptors establish revision,
replacement, authority, reuse and group-publication invariants; these are bounded
native algorithm proofs, not PostgreSQL custom-DDL parity. Public
`PreparedAnalysis` remains identical in native and pinned modes.

Required fresh selected proof, owned by root with 900-second external deadlines:

```sh
make check RUN='^TestRegOperator(Signature|Prepared)'
make check RUN='^TestReferenceRegOperator(ParserEdges|PreparedAnalysis)$'
make check REFERENCE=1 RUN='^TestReferenceRegOperator(ParserEdges|PreparedAnalysis)$'
make check RACE=1 RUN='^Test(RegOperatorPrepared|ReferenceRegOperator(PreparedLifetime|PreparedAnalysis|Persistence))'
```

These definitions and production changes are not passing evidence until root
records fresh verification; whole-project quickcheck and the affected existing
REGTYPE/REGCLASS selected gates remain required by the living plan.

## Operator definition options versus utility signatures

CREATE option strings are single literal name/type components, not REGTYPE or
REGOPERATOR input text: dots, empty strings, quotes and decorations are not
reparsed. Only the utility-signature helper interprets an unquoted NONE operand
as an absent prefix slot. CREATE's supplied NONE option is an actual type/name;
ALTER's bare/unquoted NONE option has command-specific NULL semantics, while
quoted/literal none remains a name. Operand identity resolution and type USAGE
are separate CREATE checkpoints, preserving utility signature authorization.
The positioned AST/extraction contract and new root-owned dual-mode selectors
are documented in [the operator design](pg-operator-pg18-oracle.md) and
[selected reference proof](../../reference/type_resolution/README.md#user-defined-operators).
This does not broaden alias-array, general type-input or routine capabilities.
