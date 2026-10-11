# SQL dialect

Supported SQL surface, native limits and intentional PostgreSQL differences. See
[README](../README.md) for getting started, the [SQL reference](SQL.md) for detailed
feature contracts, and the [development guide](DEVELOPMENT.md) for contributor instructions.

## Supported surface and limits

### Table and column grants — change note

Autocommit GRANT and plain REVOKE support SELECT, INSERT, UPDATE, DELETE,
TRUNCATE, REFERENCES, TRIGGER and MAINTAIN on tables/views/materialized views;
column lists support SELECT, INSERT, UPDATE and REFERENCES. Optional TABLE,
multiple targets/grantees, ALL [PRIVILEGES], PUBLIC, WITH GRANT OPTION,
GRANTED BY and current-only ALL TABLES IN SCHEMA use the existing immutable ACLs.
Column grants remain independent of table grants; ownership and implicit owner
options are not ordinary revocable use rights. Delegation records one inherited
direct source rather than a union of option sources. PostgreSQL18.0 requires
explicit GRANTED BY to name current_user. Circularity and RESTRICT are checked
within each ACL, not an invented table-column dependency graph.

Prepare is effect/name-resolution free; Execute publishes all reached ACL changes
atomically or none, retaining reached warnings. Existing consumers enforce exact
columns, including empty/filtered sources. MAINTAIN authorizes actual REINDEX;
TRUNCATE has a LOCK consumer. REFERENCES/TRIGGER do not add absent commands.
Table grants never confer sequence privileges, including SERIAL defaults.
Sequence/other-object grants, CASCADE/GRANT OPTION FOR, defaults and native
explicit-transaction object utilities remain excluded. Native explicit-ACL Dump
rejects before output; Restore's whitelist is unchanged.
REFERENCES retains its canonical reserved-word category and existing excluded
foreign-key constraint path; grant support does not add foreign keys.
Excluded object-grant forms return native `0A000` / `statement is not supported`.
See [authorization](AUTHORIZATION.md#table-and-column-grants--change-note) for
syntax, owner/PUBLIC/INHERIT behavior, grantor provenance and warning contracts.
Feature smoke (900 seconds each):
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.

### SQL prepared statements — change note

`PREPARE name [(type, ...)] AS statement` supports currently executable
SELECT/VALUES, INSERT (including supported query sources), UPDATE, DELETE and
MERGE bodies. It adds no underlying DML capability. Utility bodies (including
CREATE, SET, SHOW, BEGIN and nested PREPARE) are syntax42601; SELECT INTO is not
an ordinary preparable SELECT. `EXECUTE name [(expression, ...)]` returns the
underlying rows, names/types and command count, including currently supported
RETURNING. Existing MERGE native versus lib/pq command-count differences remain.
`DEALLOCATE [PREPARE] name` removes one definition; ALL clears only this session,
including an already empty registry. Empty declaration/actual parentheses are
syntax errors, not empty lists. CREATE TABLE AS EXECUTE remains unsupported.

Declarations resolve through current catalog/type authority and seed the one
binder's formal input contract. Unknown declarations remain inferable; concrete
unused declarations still count as required arguments. Referenced later slots
can infer types beyond the declared list; gaps and unresolved unused unknown
slots report42P18 without allocating intermediate sparse slots. Declaration
typmods do not constrain inputs (`varchar(2)` can receive `long`); actual column
typmods still enforce assignment. Concrete formal source types precede
destination contexts, allowing one declared int parameter to feed int and bigint
destinations through ordinary coercion. Unknown occurrences preserve existing
target-before-filter fallback/inconsistent-type42P08 behavior.

Actuals use the shared scalar evaluator and assignment coercion, with a fresh
typed frame per EXECUTE and no table namespace. Current arithmetic, comparisons,
casts, CASE/COALESCE and scalar functions compose; each demanded volatile actual
is evaluated once, never at driver description. For nonzero formal counts,
subqueries are0A000 `cannot use subquery in EXECUTE parameter`, aggregates are42803
and windows42P20 with EXECUTE-specific English diagnostics/positions.
Current comment/deparse/index-property and visibility functions in actuals use
their existing statement-scope catalog/role/path captures before evaluation;
primitive and known-NULL STRICT actuals do not demand those captures.

Pinned PostgreSQL18.0 independently confirms the zero-formal exception:
`PREPARE z AS SELECT 1; EXECUTE z(1/0)` returns1, as do extra subqueries,
aggregates/windows and volatile calls, without analyzing/evaluating those extras.
For nonzero formal counts, missing/extra actuals instead report42601 with
`Expected N parameters but got M.` Detail before actual-expression analysis.
Known incompatible actual types report42804 with the rewrite/cast Hint;
unknown literals use formal type input diagnostics (for example22P02).
Missing-name26000 lookup precedes SQL argument analysis. PREPARE analyzes types
and its body before duplicate-name42P05 lookup; a failed duplicate preserves the
old callable definition. Diagnostics retain absolute full-source character
positions, not positions rebased to a reparsed body.

See [session and adapter lifetimes](DRIVER.md#sql-prepared-statements--change-note).
Independent direct/Go-prepared references use `^TestReferenceSQLPrepared` in both
quick and pinned modes, with 900-second deadlines per invocation.

### Transaction and idle timeouts — change note

`transaction_timeout`, `idle_in_transaction_session_timeout` and
`idle_session_timeout` join the existing statement/lock settings as real typed
millisecond runtime limits, default0. Existing SET/LOCAL, SHOW, current_setting,
set_config, DEFAULT and RESET routes share assignment validation and transaction/
savepoint restoration. No SQL grammar, pg_sleep or public scheduler API is added.
Transaction budgets cover prebirth parsing, semantic Prepare, execution and lock
waits; successful detach is timer-free, CHAIN gets a fresh restored budget and
ERROR/savepoint restoration does not rearm stopped timers.

Idle applies only at ready boundaries to the appropriate physical owner category;
active real work is not idle, while pooled owners and retained eager Rows can be.
Current transaction/idle expiry performs canonical rollback/resource release and
reports exact FATAL25P04/25P03/57P05. Accepted native commands never return
ErrBadConn for replay; later dead pre-admission pool use can safely be discarded.
This is not libpq wire-phase emulation: pinned pq terminal sentinels and independent
real-server log evidence remain distinct from native structured diagnostics.
See [timeout rules](SETTINGS.md#timeout-behavior--change-note).

### Timezone lookup views — change note

`pg_catalog.pg_timezone_names` exposes `name TEXT, abbrev TEXT,
utc_offset INTERVAL, is_dst BOOL`; `pg_catalog.pg_timezone_abbrevs` exposes
`abbrev TEXT, utc_offset INTERVAL, is_dst BOOL`. Both grant PUBLIC SELECT and
support ordinary direct/prepared lookup queries and stored user-view consumers.
Offsets are seconds east of Greenwich, encoded as an interval with zero months
and days. Emitted values are nonnull; declared view columns remain nullable.
Names include canonical installed aliases independently, with no promised order.

Names use the transaction-start instant captured once by existing execution
authority, including repeated source occurrences. Prepared execution captures
fresh settings/instant; Prepare never freezes view rows. Abbreviations expose
selected stock entries plus actually used alphabetic uppercase session-zone
labels; session labels shadow stock entries. Numeric zone labels remain valid
in names but are excluded from abbreviations. Fixed daylight entries such as EDT
are present in winter too. Dynamic/historical matching preserves the matched
label's daylight flag, not a guess based on current season.

Runtime enumerates the existing embedded installed IANA2025b Zone/Link archive,
not the host timezone database or PostgreSQL's complete filesystem tree.
Accepted POSIX zone expressions do not become names rows. Customized files,
leap-second/right/posix trees and unrelated SQL timezone SRFs are not added.
Aliases, stock table entries, historical meanings and DST rules are version-sensitive;
see [provider provenance](../internal/sqltype/tzdata/README.md).

`pg_class` identifies these sources as views with four/three attributes.
Native does not add pg_attribute, pg_views definitions, pg_rewrite rules or
pg_get_viewdef for them: these are typed virtual sources, not stored SQL rewrites.
Semantically valid INSERT/UPDATE/DELETE reject with55000 and operation-specific
English diagnostics; abbrevs reports its UNION non-updatability, names its
nonsingle-table source. Existing supported target/expression/predicate/assignment
and UPDATE/DELETE RETURNING analysis, including parameter finalization, precedes
that rejection. Invalid supported writes retain their normal42703/42804/42883
diagnostics without evaluating expressions, requiring physical storage or writing.
Existing scalar VALUES column references remain unsupported natively (0A000
`column references are unsupported in scalar inputs`), unlike PostgreSQL's
missing-column42703; this part does not add general scalar column references.
INSERT RETURNING remains outside the existing grammar. Other catalog mutation,
DDL and row-lock restrictions remain unchanged.
Native Dump skips builtins and serializes user-view references symbolically;
Reset/Restore recreate builtins without storing timezone facts or session settings.
Admitted native results retain prior values, schema and presentation after replacement.

### MERGE insertion — change note

`MERGE INTO table USING joined_table ON expression` supports ordered conditional
`WHEN NOT MATCHED [BY TARGET] THEN INSERT` arms, with one VALUES tuple or DEFAULT
VALUES. Current table/view/derived/VALUES/join source capabilities are reused.
Source occurrences match a frozen target; first TRUE unmatched arm inserts via
ordinary INSERT mappings/defaults/identity/generated/constraints, atomically.
Command-only Query has no metadata/rows; native affected count is the inserted
count, while pinned lib/pq exports0 and shared tests independently inspect rows.
The observed integer/unknown-string nonboolean ON behavior is MERGE-local;
ordinary JOIN/WHERE/WHEN boolean rules remain unchanged.

MATCHED actions, BY SOURCE, DO NOTHING, RETURNING, action INSERT SELECT/multiple
tuples and writable target views remain unsupported, never successful no-ops.
Physical source non-tableoid system columns are prohibited in WHEN (42P10).
Permitted positive source system references still use the existing unavailable
native query-column provider boundary (42703); PostgreSQL supplies real identities.
No native MVCC/OID provider, fake NULL carrier, generic GRANT implementation or
external durability/network/pg_dump/native-ABI promise is added.
See [matching and lifecycle semantics](QUERIES.md#merge-insertion--change-note).

### RANGE table partitions — change note

`CREATE TABLE ... PARTITION BY RANGE (...)` and `CREATE TABLE ... PARTITION OF ...`
with typed `FOR VALUES FROM (...) TO (...)` or DEFAULT support nested keys and real
baseline INSERT routing/SELECT demand. Tuple ordering is lexicographic and half-open;
MINVALUE/MAXVALUE are sentinels, while SQL NULL keys require DEFAULT. Existing scalar
types, coercion and ordering providers define admission; no new type or operator-class
family is implied. See [partition definitions](TABLES.md#range-partition-definitions--change-note)
for inherited constraints, authorization, catalog metadata and persistent controls.
Explicit and effective key collations retain their existing catalog-backed
authority. Provider RESTRICT/CASCADE includes the complete hierarchy and owned
sequences; native Restore remaps explicit identity and comparison together before
preserving exact live TEMP overlays. Persistent keys depending on a TEMP collation
cannot be dumped as portable state.


Both bound arities preflight before demand. Lower then upper tuples transform
left-to-right: each finite datum binds, coerces, evaluates and rejects NULL before
the next datum is transformed. Each tuple's sentinel suffix is checked only after
its datums transform, before the next tuple begins. Expression/provider evaluation
runs outside Engine.mu; a later failure cannot rewind an already reached sequence
draw, but cannot publish a child or auxiliary definition.
Empty-range Position follows the deciding component (the last component for full
finite equality, or the terminating infinity component). Overlap diagnostics use
typed ordered endpoints and the final binary-search comparison, not sibling
creation order; an equal lower endpoint retains its first-component position.
Ancestor errors retain the actual named INSERT target; physical NN/CHECK errors
retain the actual failing leaf's diagnostic identity. Both use the original named
INSERT target's disclosure policy for VALUES and INSERT SELECT: table SELECT permits
the whole row; otherwise supplied and named-column SELECT-authorized values are
labelled in physical order, without revealing inaccessible omitted defaults.
Existing `ALL TABLES IN SCHEMA` GRANT/REVOKE includes virtual roots and nested
members, without granting future relations or sequences. Missing-partition-key
expression Detail uses PostgreSQL deparse only with named-target table SELECT;
column-only SELECT exposes simple authorized keys, not expression keys.
Partition-child Execute checks destination CREATE rights before parent ownership,
including positioned qualified-schema failures; Prepare remains effect-free.

Native hierarchy consumers intentionally reject UPDATE, DELETE, row marks, unsafe
ALTER and explicit hierarchy LOCK with ERROR/0A000 and respectively
`updating partition hierarchies is unsupported`, `deleting from partition hierarchies is unsupported`,
`row locking partition hierarchies is unsupported`, `altering partition hierarchies is unsupported`
and `locking partition hierarchies is unsupported`. LOCK first requires a genuine
transaction; ordinary25P01 outside-transaction admission is unchanged.
The newly integrated ON CONFLICT and MERGE mutation forms reject virtual and
physical hierarchy targets after ordinary analysis/rights and before row demand:
native0A000 `ON CONFLICT on partition hierarchies is unsupported` and
`merging partition hierarchies is unsupported`, with all optional fields empty.
Ordinary nonpartitioned conflict admission/revalidation/counts are preserved;
cursor SELECT continues through the ordinary complete hierarchy provider.
Virtual CREATE INDEX/REINDEX report `partitioned indexes are unsupported`;
excluded parsed constraint combinations report
`partitioned table constraint combination is unsupported`. These diagnostics have
empty optional fields. Earlier actual malformed key/bound/type/owner diagnostics
remain authoritative. Integrated PRIMARY KEY syntax parses normally; a virtual
PRIMARY KEY declaration reaches the same execution-time constraint-combination
0A000, not a parser/Prepare rejection. Physical leaves retain ordinary local
UNIQUE/PRIMARY KEY/EXCLUDE/index construction and enforcement through existing producers.
Manual sequence OWNED BY logical root/intermediate columns retains ordinary
owner/schema/live-attribute authority, AUTO dependencies and symbolic controls;
this does not widen virtual serial/identity creation combinations.

Partition definitions nested in CREATE SCHEMA retain an explicit adjacent boundary:
native utility execution reports0A000 `partition definitions in CREATE SCHEMA are unsupported`,
Position at the first nested PARTITION token, before any element is staged or bound
expression demanded. Utility Prepare remains metadata-only; PostgreSQL can execute
this combination. Ordinary CREATE SCHEMA elements are unchanged.
TEMP partition definitions are an adjacent native boundary: utility execution
reports0A000 `temporary partition definitions are unsupported` at the first
PARTITION token, before hierarchy staging. This also applies when qualified
pg_temp destinations select temporary persistence. Ordinary TEMP tables and
ON COMMIT semantics are unchanged; PostgreSQL supports same-session TEMP trees.
Permanent hierarchy CREATE inside an explicit native transaction retains the
existing0A000 utility boundary. Native physical provider retirement still removes
dependent persistent hierarchies and their owned closure atomically, preserving
peer TEMP state and retained cursor prefixes. Internal COMMIT staging uses its real
scopeless lifecycle authority, not a fabricated SQL statement scope.



Utility statements retain their existing zero-parameter contract. Genuine Prepare
with a supplied argument reports `sql: expected 0 arguments, got 1`; native direct
admission reports `stuntgres: got 1 arguments, want 0`. A no-argument utility containing
`$1` retains semantic42P02 instead of acquiring an invented bind slot.
Private Restore exceptions are scoped to actual importer provenance, not a SQL role
or authenticated superuser generally. PostgreSQL SQL control witnesses explicitly
provide importer rights while replaying the unmodified native stream.

### Datetime truncation — change note

`date_trunc(text,timestamp)`, `date_trunc(text,timestamptz)`,
`date_trunc(text,timestamptz,text)` and `date_trunc(text,interval)` return
unrestricted results of the selected temporal type. Fields microseconds,
milliseconds, second, minute, hour, day, week, month, quarter, year, decade,
century and millennium support PostgreSQL aliases and first10-byte matching.
Lower fields become zero; calendar day/month become one. Weeks start Monday;
BC decade/century/millennium boundaries use PostgreSQL's no-display-year-zero
calendar. INTERVAL retains independent months, days and elapsed microseconds;
negative fields truncate toward zero, without 30-day or24-hour justification.
INTERVAL week reports0A000 with Detail `Months usually have fractional weeks.`

TIMESTAMPTZ uses Execute-time session TimeZone or the explicit third argument.
Day and larger boundaries recompute the offset; hour and smaller retain the
source instant offset, preserving distinct fall-fold occurrences and half-hour
DST transitions. Explicit names, captured configured abbreviation sets and POSIX
zones reuse the existing provider. Session-zone abbreviations have precedence
over the selected table; dynamic abbreviations use their owning full-zone rules
without forcing the source abbreviation's offset.
Explicit results still render in the captured session zone. Invalid zones are
resolved before invalid fields/infinity; NULL in any active argument bypasses
both lookups. Wrong signatures still fail during binding.

DATE selects the preferred TIMESTAMPTZ overload and session-local midnight.
TIME selects INTERVAL through a call-local conversion only;24:00 has zero days
and24elapsed hours. This does not add a general TIME-to-INTERVAL cast.
Native intentionally rejects three-argument TIMESTAMP/DATE inputs with0A000
`date_trunc with time zone requires timestamp with time zone`, at the function
position. Pinned PostgreSQL18.0 instead implicitly converts these inputs to
TIMESTAMPTZ in the session zone; shared tests assert that distinct behavior
independently. Three-argument INTERVAL remains a genuine missing signature.

TIMESTAMP/INTERVAL two-argument forms and explicit-zone TIMESTAMPTZ are immutable;
session-zone TIMESTAMPTZ is stable, including implicit DATE coercion. Surviving
child volatility remains authoritative for generated/index admission. Strict
constant NULL may simplify before admission. Defaults, checks, views, grouping,
writes and native symbolic Dump/Restore share the ordinary expression path;
TIME children remain symbolic rather than becoming a new global cast.

Feature smoke: `make check RUN='^TestReferenceDatetimeTruncFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeTruncFeatureSmoke$'`.

### Datetime binning — change note

`date_bin(interval,timestamp,timestamp)` and
`date_bin(interval,timestamptz,timestamptz)` return the start of the bin
containing the source, aligned to the third argument's origin. Alignment floors
negative offsets; an exact negative multiple stays on its own boundary.
Arbitrary positive fixed strides preserve microseconds. INTERVAL days are
24elapsed hours, independent of DST;30days and365days are legal. Any nonzero
stored month field is forbidden, including `1 mon -30 days`. Mixed day/time
signs are permitted when the checked elapsed total is positive.

Both intrinsic signatures are immutable. Mixed TIMESTAMP/TIMESTAMPTZ inputs
select TIMESTAMPTZ, using session TimeZone for civil-to-instant child coercion.
DATE with TIMESTAMP selects TIMESTAMP; DATE with TIMESTAMPTZ selects TIMESTAMPTZ.
DATE/DATE and all-unknown temporal slots select preferred TIMESTAMPTZ; a known
exact TIMESTAMP slot with an unknown peer selects TIMESTAMP. Inferred parameters
are INTERVAL plus the selected pair of temporal types, independent of Go input
kinds. Execution captures session coercions, not a plan-retained zone. Stable
coercions remain stable through immutable wrappers and are rejected by generated
columns/expression indexes. Same-type zoned binning remains immutable.

All three arguments are strict: NULL returns typed NULL after ordinary child
evaluation and parameter import. NULL does not conceal malformed typed literals
or parameter codec errors. A nonnull infinite source returns itself before
origin/stride validation. Otherwise infinite origin fails22008 `origin out of
range`, infinite interval fails22008 `timestamps cannot be binned into infinite
intervals`, and stored months fail0A000 `timestamps cannot be binned into
intervals containing months or years`. Checked day multiplication precedes time
addition; either overflow, or source-origin subtraction overflow, fails22008
`interval out of range`. A nonpositive total fails22008 `stride must be greater
than zero`. Negative-boundary correction outside timestamp range fails22008
`timestamp out of range`. Runtime bin errors have no SQL Position or context.
Truly incompatible origins (INTEGER/TEXT/TIME) use ordinary42883 signature
diagnostics; compatible mixed temporal variants are not rejected.

Computed metadata is unrestricted TIMESTAMP/TIMESTAMPTZ, name `date_bin` unless
aliased, ScanType time.Time, without inherited input precision or column identity.
Finite raw values use time.Time, NULL uses nil, and infinities use owned bytes.
Queries, predicates, ordering/grouping/aggregate children, DEFAULT/CHECK/views,
generated/index expressions and symbolic `pg_catalog.date_bin` Dump/Restore
reuse existing publication, remapping and descriptor ownership.
Feature smoke: `make check RUN='^TestReferenceDatetimeBinFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeBinFeatureSmoke$'`.

### UUID generation — change note

`gen_random_uuid()` and `uuidv4()` generate cryptographic version4 UUIDs.
`uuidv7()` and `uuidv7(interval shift)` generate version7 UUIDs from current UNIX
time with submillisecond precision and cryptographic random tails; all four
signatures are volatile and return UUID, not TEXT. The interval overload is
strict (`uuidv7(NULL)` returns typed NULL), accepts contextual interval strings
and parameters, and permits the selected named `shift=>`/`shift:=` spelling.
Known TEXT/integer arguments require an explicit interval conversion.

One physical session shares an ascending v7 clock across both overloads,
prepared executions, transaction rollback and native Reset/Restore. Unshifted
and zero-shift calls on that session strictly increase; there is no ordering
promise across sessions or different shifts. Prepare, metadata and Dump do not
generate values. Months, days and elapsed microseconds are applied in that order,
using Execute-time TimeZone (month-end clamping and DST are not fixed durations).
Native accepts shifted UNIX microseconds0 through281474976710655000 inclusive,
rejects infinite shifts and out-of-range timestamps with exact22008 diagnostics,
and preserves ordinary timestamp-arithmetic overflow errors.

The manual18.6 guard contract differs from the unchanged pinned18.0 oracle:
18.0 unchecked infinite shifts succeed with valid version/variant bits but have
no portable timestamp expectation because sentinel arithmetic overflows;
finite outside-48-bit shifts encode wrapped timestamps. Shared reference tests
assert these known release differences explicitly, not weakened parity.
Closed selected UUID defaults generate per row; stored CHECK/views/DML and
native symbolic Dump/Restore use the existing typed expression/control paths.
Generated columns and expression indexes reject surviving volatile generators,
but strict `uuidv7(NULL::interval)` reduces to typed UUID NULL before immutability
validation and is admitted. Simplified constant-NULL CASE/COALESCE arguments obey
the same rule without demanding clock or entropy. Stored definitions remain
symbolic across Dump/Restore; genuine unshifted/non-NULL generation stays volatile.
No UUID extraction functions, binary wire API or external provider is added.
See [UUID generation](TYPES.md#uuid-generation).
Feature smoke: `make check RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`.

### Binary conversion — change note

| Strict signature | Result | Intrinsic volatility |
|---|---|---|
| `convert(bytea,name,name)` | BYTEA | STABLE |
| `convert_from(bytea,name)` | TEXT | STABLE |
| `convert_to(text,name)` | BYTEA | STABLE |
| `encode(bytea,text)` | TEXT | IMMUTABLE |
| `decode(text,text)` | BYTEA | IMMUTABLE |

The database encoding is UTF8. All42 PostgreSQL18 encoding identities and all128
documented directed default pairs are implemented, including MULE_INTERNAL and
the direct Japanese, BIG5/CNS, Cyrillic and LATIN2/WIN1250 algorithms. Names use
the existing release aliases after NAME's63-byte complete-UTF8 clipping. Resolve
source then destination, validate the entire source (including NUL), then apply
empty/equal-encoding/SQL_ASCII shortcuts or exactly one direct pair. Missing
nonempty pairs report42883 even for ASCII; empty input succeeds after name
resolution. No arbitrary pathfinding or implicit UTF8 pivot is provided.

Format names are TEXT, matched case-insensitively without whitespace trimming or
alias normalization. Only hex/base64/escape are accepted. Hex emits lowercase
digits without a BYTEA prefix, accepts either case, and ignores SP/TAB/LF/CR only
between complete pairs. Base64 uses the RFC2045 alphabet, with LF after each
complete76-character line; a padded final quartet does not itself add LF. Its
decoder uses PostgreSQL18's padding state machine, not Go's stricter decoder.
Escape encodes NUL/high bytes as three-digit octal and doubles backslashes;
other ASCII bytes, including controls and DEL, remain literal. This intentionally
differs from `bytea_output=escape`. Decode accepts doubled backslashes and valid
three-digit octal; ordinary UTF8 bytes are copied.

Character inputs retain the existing implicit TEXT/NAME coercions (CHAR loses
trailing spaces); raw BYTEA binds remain octets. NULL skips runtime conversion,
not child analysis/input errors. Runtime codec errors have no invented SQL
Position. Outputs and retained metadata preserve BYTEA/TEXT ownership.
Query/write/CHECK/view consumers and native Dump/Reset/Restore use the existing
Binary carrier and staged publication. Generated columns/expression indexes
accept immutable formats and reject stable convert functions, including under
immutable wrappers. Existing binary-function DEFAULT exclusion remains native
0A000; no integer↔BYTEA casts or CREATE CONVERSION are added. The separate
bounded client_encoding/SET NAMES settings do not change these conversion pairs.

One inherited direct-call transport/admission difference remains outside these
providers: with no supplied bindings, PostgreSQL's simple-query
`SELECT convert($1,$1,'UTF8')` rejects the first parameter with42P02,
`there is no parameter $1`, Position16. Native direct execution analyzes before
its supplied-argument count check and reports42P08,
`inconsistent types deduced for parameter $1`, Detail `bytea versus name`,
Position19. Genuine Prepare on both backends reports that same42P08 formal-role
conflict. This does not relax selected format/name/source-data diagnostics or
change generic parameter admission, adapter contracts or prepared inference.

### Bit strings — change note

Scalar `BIT`, `BIT(n)`, `BIT VARYING[(n)]` and `VARBIT[(n)]` support stored
columns, direct/prepared INSERT/SELECT, defaults and NULL. Bare keyword BIT
defaults to one bit; quoted/qualified BIT and unbounded varying forms do not.
Modifiers range from1 through83886080. Assignment never pads or truncates:
fixed mismatches fail22026, varying overflow22001, malformed digits22P02.
Parentheses around a simple modifier retain the constant; arithmetic modifiers
are rejected uniformly with42601 at the type start during CREATE execution,
not evaluated as bit-width arithmetic.
Adjacent raw `B'…'`/`X'…'` literals and textual parameters produce canonical
binary string values, preserving leading zeroes and distinguishing empty from
NULL. BIT↔VARBIT assignment checks destination width. Metadata and native
Dump/Reset/Restore preserve type identity and bit width. Explicit casts,
bit operators/functions/comparisons/index classes and bit-array storage remain outside
this surface; mixed BIT/VARBIT parameter destinations retain native0A000.
Separated `b '…'`/`x '…'` prefixes remain outside adjacent bit-literal syntax:
the inherited native generic typed-literal guard returns0A000
`typed literals are unsupported`, whereas PostgreSQL reports42704 missing type.
The bit feature does not expand that generic typed-literal surface.
Existing stored `format` defaults/views consume known B/X literals without
introducing explicit BIT casts in Dump; restored defaults remain expressions.
See [bit strings](TYPES.md#bit-strings--change-note) for input, scanner and
driver details.

### String codepoints, encoding and Unicode — change note

The fixed UTF8 database supports these immutable, strict functions:

| Signature | Result | Behavior |
| --- | --- | --- |
| `ascii(text)` | INT4 | First UTF8 codepoint; empty text returns0, not an error. |
| `chr(integer)` | TEXT | One Unicode scalar; unassigned scalars and noncharacters are legal output. |
| `to_ascii(text)` / `to_ascii(text,integer)` / `to_ascii(text,name)` | TEXT | Byte transliteration from an explicitly selected supported legacy encoding. |
| `unistr(text)` | TEXT | Runtime Unicode escape decoding and UTF16 surrogate pairing. |
| `unicode_assigned(text)` | BOOL | Every codepoint has Unicode16 General_Category other than Cn; empty text is true. |

Character arguments use the existing type-owned TEXT conversion, including
trailing-space removal for CHARACTER but not VARCHAR. `chr` accepts implicit
SMALLINT widening and contextual unknown input, not known BIGINT/NUMERIC/TEXT.
The encoding argument chooses INT4 for compatible known numeric inputs and NAME
for character inputs or unknown literals/parameters. NAME retains its existing
63-byte complete-UTF8 clipping before encoding-name resolution.

`to_ascii` is not general encoding conversion or Unicode accent removal. It maps
raw source bytes through PostgreSQL18's LATIN1/LATIN2/LATIN9/WIN1250 tables
(IDs8/9/16/29), preserving byte count and ASCII bytes. For example
`to_ascii('é','LATIN1')` returns `AC`, not `e`. Omitted encoding is UTF8, so even
empty/all-ASCII sources report0A000. Release18 encoding aliases normalize ASCII
alphanumerics case-insensitively: `ISO-8859-1` and `windows-1250` are valid;
`CP1250` is not. Valid-but-unsupported encodings, including SQL_ASCII, UTF8,
MULE_INTERNAL (ID7) and SJIS, report0A000 using canonical encoding names.
Unknown names/codes report42704. This does not add server-encoding settings
or a general conversion registry.

`unistr` recognizes exactly `\XXXX`, `\+XXXXXX`, `\uXXXX`, `\UXXXXXXXX` and
`\\` (one literal backslash); hex digits accept either case and widths are fixed.
Use dollar literals or parameters when supplying these raw runtime escapes:
`unistr($$\D83D\uDE42$$)` returns `🙂`. Ordinary Unicode is unchanged.
NUL or a value above10FFFF reports22023 with uppercase hexadecimal codepoint;
malformed escapes report42601 with the four-form Hint. Invalid surrogate pairing
reports42601 without that Hint. A malformed/out-of-range second escape is diagnosed
before pending-surrogate pairing. These are runtime function diagnostics, distinct
from lexical E-string/U& errors, and carry no invented SQL source Position.
`chr(-1)` reports22023; zero, surrogate values and values above10FFFF report54000.

Assignment uses generated ranges from the vendored Unicode16 UnicodeData, including
CJK/Hangul and private-use First/Last ranges. Controls, combining marks and private
use are assigned; noncharacters and unassigned characters are not. SQL text input
still validates the entire UTF8/NUL boundary before any function, even `ascii`.
NULL returns a typed NULL and skips runtime conversion availability/name checks,
not analysis or demanded child-input diagnostics.

DEFAULT/CHECK, views, generated columns and expression indexes reuse the existing
expression ownership/volatility contracts. Native Dump/Restore preserves overloads
and replays actual emitted SQL against PostgreSQL; failed staged Restore or writes
do not publish partial state. Cancellation uses bounded provider scans; native
controls retain their ordinary precanceled context-error contract.
Complete dual-mode family: `RUN='^TestReferenceStringEncoding'`; feature smoke:
`RUN='^TestReferenceStringEncodingFeatureSmoke$'` under both check modes.

### Forward-only SQL cursors — change note

Nonholdable `DECLARE [NO SCROLL] CURSOR [WITHOUT HOLD] FOR query`, forward
constant-count/ALL FETCH and CLOSE/name/ALL reuse the typed query pipeline and one
physical-session registry. Each FETCH is eager and detached while the private
query retains position and DECLARE visibility. First-demand identity/time data
is distinct from each FETCH's current execution authority. Blocking query demand
is preserved; ordinary SELECT eagerness is unchanged. Empty and zero-width batches
retain complete schema/cardinality. SQL/driver transaction end, full abort,
PREPARE detach and session end close named execution; savepoint rollback removes
later declarations without rewinding older movement or undoing CLOSE/failure.
Native held FETCH returns the live schema, not pq's stale prepared description.
Successful final cursor preflight is authoritative: Finish cannot retract a
completed DECLARE/FETCH/CLOSE on late cancellation, while mid-demand failure
returns no partial batch and fails the portal. Only these stage-free cursor
utilities bypass the explicit private-stage merge; noncursor publication is unchanged.
Runtime REGTYPE parser notices and modifier warnings use the current FETCH's
operation/`client_min_messages`, not DECLARE's. Quiet DECLARE/before-first FETCH0
and EOF do not emit runtime warnings; bounded FETCH projects no extra output warning.
SCROLL, WITH HOLD, backward repositioning and ABSOLUTE/RELATIVE movement remain
excluded. See [driver ownership and transport distinctions](DRIVER.md#forward-only-sql-cursors--change-note).
Dual-mode family: `RUN='^TestReferenceSQLCursor'`; feature smoke:
`RUN='^TestReferenceSQLCursorFeatureSmoke$'`.

### Prepared transaction SQL — change note

`PREPARE TRANSACTION STRING`, `COMMIT PREPARED STRING` and
`ROLLBACK PREPARED STRING` are complete-input utility controls. Prepare is
metadata-only for driver statements; execution captures current actor/state.
Private writes and original relation/row/unique locks survive preparation and
physical origin exit, then publish once on commit or discard on rollback.
Exact principal ownership, GID byte limits/uniqueness, full-failure rollback,
SESSION/LOCAL finishing, sticky TEMP sequence access and retained SSI dependencies
share existing transaction seams. `pg_prepared_xacts` exposes five exact types,
including a narrow distinct XID scalar, without enabling XID column syntax,
casts/functions/ordering or arrays.

Native state is always-enabled and process-local per Engine, not crash durable
or cluster-wide. Reset/Restore/ReplaceCatalog refuse while any entry remains;
Dump records committed state only. Executed owner-TEMP table/sequence access
prohibits preparation. SQL WITH HOLD cursors, notifications and exported snapshots
retain their preexisting exclusions.
Ordinary driver Rows and prepared statements do not prohibit preparation.
See [transaction lifecycle and diagnostics](TRANSACTIONS.md#prepared-transactions).

### Query-created tables — change note

Ordinary `CREATE TABLE [IF NOT EXISTS] name [(columns)] AS query
[WITH [NO] DATA]` and top-level `SELECT ... INTO [TABLE] name` create independent
tables through the existing SELECT/query-VALUES producer. Populated commands
return stored-row counts with no public columns; NO DATA, empty producers and
existing-target skips return0. Source analysis/parameter import stays active for
NO DATA and skips, but planning and runtime evaluation do not run.
See [storage shape/publication](TABLES.md#query-derived-tables--change-note)
and [consumer phases](QUERIES.md#query-created-table-consumers).

Native writable explicit transactions reject these utilities with0A000
`statement is unsupported in an explicit transaction` before source acquisition,
including semantic Prepare. Execute read-only25006 rejection precedes initial
snapshot acquisition and source lookup/ACLs/locks, with command tags CREATE TABLE AS
and SELECT INTO. Native explicit read-only Prepare retains its ordinary0A000
utility exclusion, including the sticky error from Conn-prepared→Tx.StmtContext
repreparation. These rejected utilities never enter a deferrable safe-snapshot
wait. PostgreSQL can instead encounter source-analysis errors or, with an unresolved
serializable writer,57014 `canceling statement due to statement timeout` before its
utility gate, including Prepare and the held route. Owner recovery requires the
ordinary rollback after an admitted failure. Normal SELECT and admitted standalone
query creation retain their existing snapshot acquisition.

Temporary/TEMPORARY/unlogged creation, temporary namespaces, access methods,
storage parameters, tablespaces, ON COMMIT, AS EXECUTE and AS TABLE remain explicit
capability boundaries. Deferred option rejection still consumes the complete
input before effects. Native pg_temp namespace rejection retains destination
character Position; other CTAS destination schema errors are unpositioned.
This CTAS change itself adds no new set/query providers, SQL GRANT
surface, native pg_type/pg_attribute source or broader Restore whitelist.

Full shared definitions: `make check RUN='^TestReferenceQueryTableCreate'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryTableCreate'`.
Feature smoke uses the same commands with `RUN='^TestReferenceQueryTableCreateFeatureSmoke'`.


### SQL current date and time — change note

`CURRENT_DATE` returns DATE; `CURRENT_TIME[(p)]` returns TIMETZ;
`CURRENT_TIMESTAMP[(p)]` returns TIMESTAMPTZ; `LOCALTIME[(p)]` returns TIME;
`LOCALTIMESTAMP[(p)]` returns TIMESTAMP. Each reads the microsecond-quantized
top-level transaction start, not statement time or Prepare time. Explicit
transactions preserve that instant across statements/savepoints. COMMIT/ROLLBACK,
including AND CHAIN, and implicit statements obtain fresh transaction clocks;
physical peer sessions remain independent.

TimeZone is read for each statement: changes within a transaction alter civil
date/clock/local timestamp and CURRENT_TIME's fixed offset without changing the
CURRENT_TIMESTAMP instant. DateStyle controls normal date/timestamp text output.
DATE is unrounded; civil time/local timestamp are derived before precision
rounding, while TIMESTAMPTZ rounds the instant. Rounded TIME/TIMETZ may reach24:00;
timestamp rounding carries into the next date.

Only the four time/timestamp forms accept an unsigned integer precision literal.
Omission preserves full microsecond precision;0–6 rounds fractional seconds.
Higher precision succeeds at6 with WARNING22023 at analysis, once for Prepare
and never repeated by SAME held Execute. Signed/empty/expression/parameter
precision syntax is42601; CURRENT_DATE has no parentheses. Quoted or qualified
ordinary function names do not become these keyword forms. Quoted current-family
table and column identifiers remain case-preserving ordinary names, including
before CREATE/INSERT column-list parentheses.

Direct/prepared projections, existing casts/comparisons and scalar table/DML
consumers preserve distinct descriptors and default keyword labels. Defaults,
CHECK and views retain symbolic expressions through deparse/native replay.
The producers are STABLE, not immutable generated/index expression providers.
Native DATE/TIME/TIMETZ use strings and TIMESTAMP/TIMESTAMPTZ use time.Time;
pq uses its existing temporal time.Time codecs. Nullable metadata remains unknown
for computed producers; native stored/view columns retain declared metadata.
Quote keyword-labelled view columns (for example `"current_time"`) to read them;
bare `current_time` is a new SQL value function, not a column reference.

These current-derived scalar expressions, including
`normalize(CURRENT_DATE::TEXT)` and `CURRENT_DATE::TEXT IS NORMALIZED`,
use the ordinary query pipeline and its no-FROM local input. This does not add
datetime arithmetic or additional catalogs.
Full shared scenarios: `make check RUN='^TestReferenceDatetimeCurrent'`
and `make check REFERENCE=1 RUN='^TestReferenceDatetimeCurrent'`.
Feature smoke uses the same commands with `RUN='^TestReferenceDatetimeCurrentFeatureSmoke$'`.

### Transaction and statement time — change note

`now()`, `transaction_timestamp()`, `statement_timestamp()` and
`clock_timestamp()` return TIMESTAMPTZ; `timeofday()` returns TEXT. These five
ordinary functions take exactly zero arguments (no precision overload). Lowercase,
unquoted folded names, exact quoted lowercase and `pg_catalog` qualification
resolve through the existing builtin namespace rules.

`now()` and `transaction_timestamp()` read the same top-level transaction start,
also witnessed by CURRENT_TIMESTAMP and TIMESTAMPTZ 'now'. Savepoint recovery
does not restart that clock. Statement time is fixed across demanded occurrences
of one command and refreshes at the next command. The first three calls are
STABLE; the wall-clock calls are VOLATILE and sample at actual demand, not
Prepare or stored-definition creation. Adjacent wall samples may be equal;
strict monotonicity is not promised. All samples use microsecond precision.

Native direct and held-prepared execution each own one public-command sample.
A fresh implicit transaction uses that same sample for its birth and statement
start. PostgreSQL simple-query execution agrees; pq extended-protocol Bind can
start a transaction before Execute refreshes statement time. Thus the shared
prepared/parameterized contract is now <= statement_timestamp, while native
single-command execution additionally gives equality. No artificial wire phase
or delay is introduced. Explicit BEGIN/BeginTx and AND CHAIN capture fresh births;
physical peer sessions remain independent.

TimeZone controls typed export and casts at execution without changing instants.
timeofday instead retains its English `Mon Jan DD HH:MM:SS.ffffff YYYY zone`
layout regardless of DateStyle: two-digit day, six fractional digits and the
session provider's abbreviation. Numeric `SET TIME ZONE 5.5` gives east +05:30
and suffix `+05:30`; textual `'+05:30'` uses POSIX west-offset interpretation
and an empty abbreviation, retaining the final space. TEXT exports/ScanType are
string with known unbounded Length; four temporal exports/ScanTypes are time.Time.
Computed nullability and temporal length/precision remain unknown.

Existing queries, DML, CHECK and views compose these producers; deparse and
Dump/Restore retain symbolic call names, including the two transaction aliases.
Closed selected clock/timeofday default trees are admitted without allowing
unrelated volatile defaults. Surviving selected calls remain nonimmutable for
generated columns and expression indexes. Selected aggregate/window decorations
are failure-only syntax, never successful aggregate/window functionality for
these five providers. A call permits at most one FILTER followed by at most one
OVER clause; repeated or misordered clauses are syntax errors (42601). A
syntactically valid FILTER-OVER call fails as a nonaggregate (42809), after
argument and FILTER child analysis. Named argument structure is checked after
that child analysis and the 100-argument guard: duplicate labels and positional
arguments after named ones fail with 42601 at the offending label/expression,
before missing-signature resolution.

Full shared selector: `^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)`.
Feature smoke uses `RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'` in both
`make check` and
`make check REFERENCE=1`, each under 900 seconds.


### Composites — change note

`CREATE TYPE [schema.]name AS (member type [COLLATE collation], ...)`, including
an empty member list, publishes one named type and a reciprocal nonstorage
backing relation. Supported user table/view rows share the concrete codec.
Unknown literals/inferred parameters, explicit TEXT IO, nested supported scalar/
plain-domain/composite/scalar-array children, stable source shape generations,
dependency cascades and mixed native Dump/Restore are supported.
Table CHECK/NOT NULL constraints remain physical-source-only. Constrained domains,
composite arrays, builtin/catalog row execution, ROW constructors, field operations,
comparison/hash/key operations and new ALTER/DROP TYPE forms remain excluded.
See [complete type/transport/shape contract](TYPES.md#composites--change-note).

### Array expressions and partial assignments

`ARRAY[...]` supports scalar common-type resolution and rectangular nested
constructors. An explicitly cast empty constructor obtains its element type
from that cast. Constructors are not assignment-context typed; existing scalar
coercion rules apply elementwise without adding new scalar cast routes.
All child expressions are evaluated before constructor shape checks. The first
nonempty child's added outer rank is checked against six before sibling shape
or mixed-empty errors; all-empty/NULL children retain canonical empty.

Array access supports adjacent subscripts, parenthesized expression bases and
parameters. Element reads respect actual lower bounds; NULL, missing and
wrong-rank reads yield typed NULL. A colon makes the entire access a slice:
plain subscripts become `1:upper`, omitted bounds use the actual dimensions,
and clipped result bounds start at one. Slice misses yield typed empty arrays.

INSERT and UPDATE allow element and slice targets, including repeated partial
targets in SQL order. INSERT partial targets start from typed NULL rather than
the column default. UPDATE RHS and bounds read the old row while partial writes
compose privately. NULL assignment bounds are errors; NULL slice RHS is a
no-op. One-dimensional writes may enlarge arrays with typed NULL gaps.
Multi-dimensional writes preserve shape, and slice assignment consumes enough
flattened RHS elements while ignoring excess. MERGE partial targets remain
unsupported.
Nonempty slice writes validate destination dimensions in order and final shape
before RHS size. Empty/NULL originals construct explicit spans: zero yields
canonical empty, negative/overflow yields SQLSTATE `54000`, and omitted boundaries
remain errors. Empty originals check source sufficiency before final bounds,
including final bounds when another span is zero. NULL slice RHS bypasses span
checks after bound NULL validation.

Bound analysis coerces lower then upper within each lexical dimension before
rejecting a group longer than six. This lexical analysis order is distinct from
runtime demand; the executor's rank-six storage limit is unchanged.
Execution demands the base, all upper bounds, then all lower bounds. Reads of
a whole-NULL array skip bounds; assignments do not. Assignment NULL-bound
validation precedes its replacement RHS. Existing source-first INSERT query
and identity behavior is preserved.

Native Restore applies its central parameter rejection to stored CREATE column
defaults, generated expressions and column/table CHECK expressions, including
parameters inside array members or bounds. Stored expression admission stays
with the existing binder; INSERT retains its existing Restore whitelist.

### Domains — change note

`CREATE DOMAIN [schema.]name [AS] base_type [COLLATE [schema.]collation] [NULL]`
supports the existing stored scalar families, domain chains and supported arrays.
The target schema requires CREATE; a domain base requires its represented USAGE
authority. Domains have distinct catalog IDs, owners, namespaces and immediate
bases. Their nullable values use the ultimate base codec/typmod/collation.
Known same-domain CASE/COALESCE/VALUES inputs retain logical identity; mixed
base/distinct-domain inputs flatten through existing common-type rules. Supported
operators/functions, ordering, grouping, COUNT and BOOL_AND/BOOL_OR use
base signatures; assigning or explicitly casting back restores the domain tag.

Duplicate names fail42710 before resolving the proposed base. Missing types fail
42704; void/unknown/record are invalid domain bases (42804). Domain use-site
modifiers fail42601. Literal input preserves source character positions and base
SQLSTATE/messages; parameter input retains existing adapter diagnostics.
Contextual literals read the unmodified physical base first: lexical errors stay
eager, while assignment typmod enforcement and explicit truncation use the normal
execution coercion. Qualified modifier syntax resolves the actual catalog type,
including a domain whose name is also a builtin spelling.
The default is typed NULL. Domain DEFAULT/CHECK/NOT NULL, ALTER DOMAIN,
array operators/functions, GRANT/REVOKE TYPE/DOMAIN and
explicit-transaction domain utilities remain unsupported, not successful no-ops.
The migrated visibility boundary retains its exact CREATE DOMAIN d AS integer
Query, empty utility result and typed7/true INT4/BOOL witness. DROP followed by
quiet Prepare leaves the type absent until SAME held Execute; six other native
DDL exclusions and the default-function negative remain. Original source and
recorded Context consumers are separate phases, not new dialect support.


`DROP DOMAIN [IF EXISTS] name [, ...] [RESTRICT | CASCADE]` requires ownership.
RESTRICT retains all objects/data and reports actual dependent types, columns and
stored expressions. CASCADE removes the transitive closure, preserving unrelated
columns/tables, stable surviving attributes and row identities; schema removal
includes external dependents. Missing IF EXISTS emits a normal NOTICE.
Native logical Dump/Restore orders domains before dependent persistent objects
and reowns logical/base pointers, owners, payloads and dependencies atomically.
It is not PostgreSQL wire storage or external pg_dump support. Native pg_type
projections, pg_typeof and general array operators are not added.
See [type/driver contracts](TYPES.md#domains--change-note) and the domain feature
smokes in [README](../README.md).

`information_schema.time_stamp` is the selected executable predefined domain:
its base is `timestamp(2) with time zone`, preserving supplied instants and
rounding fractions to hundredths through existing domain/base coercion.
Database/sql results expose TIMESTAMPTZ base metadata while the declared column
keeps the qualified domain identity. Native Dump/Reset/Restore retains it without
recreating a user domain. Sibling information-schema domains and `_time_stamp`
array storage remain unsupported, as do general information_schema/pg_type views.
PostgreSQL's predefined `DEFAULT current_timestamp(2)` is an explicit boundary:
native does not inherit type defaults; omitted/DEFAULT values use an explicit
column default or typed NULL. Explicit NULL/bound nil remain NULL in both modes.



### Constraint/expression introspection — change note

The native catalog sources add exactly `pg_catalog.pg_constraint`
(`oid OID, conname NAME, contype CHAR, conrelid OID, conbin PG_NODE_TREE`) and
`pg_catalog.pg_attrdef` (`oid OID, adrelid OID, adnum INT2, adbin PG_NODE_TREE`).
They project existing CHECK (`c`), NOT NULL (`n`), UNIQUE (`u`) and PRIMARY KEY
(`p`) identities, plus explicit nontrivial defaults from real immutable descriptors.
NOT NULL and index constraints have NULL conbin. Attribute numbers are stable,
not dense evaluation slots. These are not general constraint/catalog rollout:
foreign keys and additional metadata catalogs remain absent. STORED generated
definitions share existing pg_attrdef/pg_get_expr; no pg_attribute or
information_schema rollout is implied.

`pg_node_tree` is distinct type194 with PG_NODE_TREE metadata and native string
ScanType. Non-NULL values are internal immutable structural carriers. Output uses
native deterministic node tags, concrete type/typmod, attribute/provider identities,
constant payloads and recursive children. It is neither original/replay SQL nor
an opaque lookup token, and is **not** PostgreSQL nodeToString physical interchange.
External non-NULL input always fails0A000, including empty/invalid text; typed NULL
is ordinary. The four `pg_get_constraintdef`/`pg_get_expr` signatures return TEXT,
with structural rendering, strict NULL, current names and supplied-relation lookup.
Both modes preserve grouped predicate operands and current provider visibility.
Standard/postfix same-type scalar annotations and existing directional numeric
edges consume returned definitions; function casts and known-TEXT input conversion
boundaries are unchanged. Literal NULL may carry `::unknown` syntax provenance,
but UNKNOWN is not an input/DDL type.
Synthetic comparison casts are unsized base-type annotations, not destination
column assignments; actual explicit NUMERIC precision/scale remains enforced.
Reconstruction does not add array equality/order or plain TIME AT inputs.
current_schemas behavior uses strict BOOL NULL and public changing-path output;
AT/LOCAL consumer fixtures use typed TIMESTAMP/TIMESTAMPTZ/TIMETZ columns.
Persisted identity leaves preserve keyword versus ordinary-call syntax and stable
NAME/TEXT typing; neither SQL nor structural output evaluates the identity.
Native SYSTEM_USER remains typed NULL, but its nontrivial DEFAULT stays stored.
These consumers do not change identity providers, authority or namespace resolution.
See [query contracts](QUERIES.md#constraint-and-expression-deparsing).

### Safe legacy user view — change note

`pg_user` is a public nine-column LOGIN-only role projection: NAME usename,
OID usesysid, four direct BOOL privilege flags, constant TEXT passwd `********`,
typed TIMESTAMPTZ expiry and typed NULL TEXT[] useconfig. Ordinary readers see
unrelated LOGIN roles; NOLOGIN is the sole role filter. Expired passwords and
connection limits do not hide users, and membership does not confer projected
flags. Credentials are never read or exported by the provider.

Role-specific settings remain excluded; NULL useconfig does not promise
ALTER ROLE SET/RESET or an empty settings array. Exact C950 collation for NAME
and useconfig follows the pinned oracle without changing generic TEXT[] defaults.
Saved statement-wide role roots cover stored/derived/join consumers; fresh held
execution observes changes while already-open native rows and metadata survive
mutation and Reset/Restore. Timezone rendering is captured per execution.

Normal semantic errors precede readonly rejection. Writes modifying passwd fail
0A000 regardless of its assigned value; other supported writes fail55000 naming
pg_shadow with complete PostgreSQL Detail/Hint. pg_shadow is diagnostic/summary
text only, not an executable catalog. No role settings, locking or utility
grammar expansion is implied. See [the exact contract](SQL.md#pg_user-safe-user-view--change-note).

### Safe role view — change note

`pg_roles` is the public thirteen-column role summary, including unrelated,
NOLOGIN, privileged, expired and zero-limit roles. Flags and limits are direct
stored attributes, not membership-derived privileges. PUBLIC is not a role row.
The password is always TEXT `********`, never a credential-dependent value;
reachable configuration is typed NULL TEXT[], not empty or session-derived.
Role settings/ALTER ROLE SET/RESET and pg_authid/pg_shadow remain excluded.

Typed TIMESTAMPTZ expiry uses execution-captured timezone. One immutable saved
role root serves repeated and nested source openings; held Prepare refreshes on
each execution while old native Rows/metadata survive mutation and Reset/Restore.
Native controls preserve role identities and membership, omitting role DDL,
credentials and virtual rows from Dump. Generated view relation identities need
not match an independent server's allocator; existing row/array types link to
the actual view. NAME and rolconfig use exact C950 collation, mask TEXT default100.

Valid writes fail55000 naming pg_roles, including rolpassword/WHERE FALSE, with
the join-view Detail and operation-specific INSTEAD OF Hint; earlier ordinary
semantic diagnostics retain precedence. Native catalog row locking remains0A000.
`pg_views` includes the release reconstruction; `pg_tables` excludes the view.
See [the exact schema/contract](SQL.md#pg_roles-safe-role-view--change-note).
Feature smoke: `make check RUN='^TestReferencePGRolesFeatureSmoke$'` in both
native and `REFERENCE=1` modes, each with an external900-second deadline.

### Table and view summaries — change note

Readonly `pg_tables`/`pg_views` use their documented PostgreSQL18 eight/four
fields and exact unfiltered underlying-object disclosure, independent of actor
and search_path. They summarize supported ordinary tables and stored views plus
the explicitly represented nine system tables/seven system views, including
pg_matviews, pg_user and pg_roles; this does not
promise a full PostgreSQL catalog inventory or change `pg_class` kinds.
Tables retain sticky index-publication history after DROP. Native admitted user
tables have default NULL tablespace and false rule/trigger/RLS states; no success
stub is added for unsupported DDL. Released builtin flags/tablespaces and complete
system-view SELECT definitions are sourced compatibility metadata, not invented
native storage/indexes or executable definitions of missing catalogs/functions.
User definitions remain executable stored structural reconstruction, not original
SQL, pretty-print equality or view evaluation.

No persistence/access-method/physical-size columns or physical approximations are
exposed. Native logical Dump/Restore serializes real owning objects and normalizes
formerly-indexed/no-current-index history to false; current/UNIQUE indexes restore
true. Immutable actual catalog replacement preserves history-only true and
normalizes false to true for represented indexes without mutating source roots. Captured
native rows/descriptors remain stable while held executions refresh. Supported
own transaction overlays participate; native's latest committed catalog frontier
still differs from PostgreSQL repeatable-read MVCC. TEMP tables/views, partitioning,
tablespace DDL, rules, triggers, RLS and VACUUM remain outside this change.
Native ALTER TABLE OWNER and TRUNCATE retain their existing0A000 boundaries
without changing summary state; supported ALTER VIEW OWNER refreshes view ownership.
Table owners come from real authenticated creation, control replay or internal
immutable descriptor replacement, not a new table-owner SQL command.
See [exact schemas and provenance](SQL.md#table-and-view-summaries--change-note).

Index/view introspection has seven strict STABLE TEXT signatures:
`pg_get_indexdef(oid)`, `pg_get_indexdef(oid, integer, boolean)`,
`pg_get_viewdef(text)`, `pg_get_viewdef(oid)`,
`pg_get_viewdef(text, boolean)`, `pg_get_viewdef(oid, boolean)` and
`pg_get_viewdef(oid, integer)`. There are no optional single index-column
arguments or text/width overloads. Unknown arguments resolve before strict NULL;
ambiguous calls report42725, missing signatures42883. Names use normal captured
relation lookup; missing OIDs/nonmatching kinds yield NULL, missing text names
error. Invalid nonzero index columns yield nonnull empty TEXT. Pretty/width
rendering is structural and executable, not original SQL or view evaluation.
Ordinary and materialized views and simple/expression/partial/INCLUDE/UNIQUE backing
indexes use the current-name renderers.

### Materialized-view metadata — change note

Permanent materializations expose `pg_class.relkind='m'`, their real owner,
namespace, column count and `_RETURN` rule. `relhasrules` is true;
`relispopulated` distinguishes populated empty results from WITH NO DATA and is
true for other represented relation kinds. `pg_get_viewdef` renders the frozen
definition without evaluating it.

`pg_catalog.pg_matviews` exposes exactly seven columns: `schemaname NAME`,
`matviewname NAME`, `matviewowner NAME`, `tablespace NAME`, `hasindexes BOOL`,
`ispopulated BOOL`, `definition TEXT`. Default tablespace is NULL and indexes
are false within this scope. Like pg_views, membership discloses represented
objects independently of privileges and search_path. The materialized definition
uses the existing PostgreSQL presentation renderer and captured search_path for
qualification. Dump uses that renderer with fully qualified identities so native
replay preserves the definition's presentation without synthetic source aliases.
MVs do not appear as
ordinary pg_tables/pg_views rows. No full catalogs, physical statistics, public
MV indexes, ALTER or REFRESH are added. Native persistence preserves exact stale
rows privately; PostgreSQL dump-script replay deliberately restores only
unpopulated definitions. See [native controls](TABLES.md#materialized-views--change-note).
The bounded surface does not add pg_namespace or pg_attribute query
providers: exact schema/owner names come from pg_matviews, while existing
pg_class/pg_rewrite identities expose namespace/owner relationships and rule
links without promising allocator-specific OIDs. Frozen REGTYPE constants use
the existing typed identity output/remapping authority when deparsed, including
associated row types whose identities are reminted during native replacement.

### Owned sequence introspection — change note

`pg_get_serial_sequence(table text, column text) -> text` is the sole signature.
This strict STABLE inquiry finds actual column-owned sequences: AUTO ownership
from SMALLSERIAL/SERIAL/BIGSERIAL or manual OWNED BY, and INTERNAL ownership from
the supported identity columns. It does not infer an association from a default
or `_seq` spelling. DROP DEFAULT or a different nextval provider leaves ownership
intact; OWNED BY NONE removes the association even if the default survives.
An ordinary unowned column returns typed TEXT NULL.
The existing `ctid`, `xmin`, `cmin`, `xmax`, `cmax` and `tableoid` system
attributes on supported non-view relations also have no owned sequence and return
NULL. Views lack those implicit attributes and retain42703 for absent names;
a declared view column with the same spelling follows ordinary declared lookup.
This existence inquiry adds no system-column value projection or DDL capability.

The first argument is SQL-qualified identifier text: unquoted components fold,
quoted components preserve case/dots/embedded quotes, and identifiers truncate
at a complete UTF8 boundary of63 bytes. Numeric text names a relation, not an OID.
A matching current-database prefix is accepted; cross-database references are
unsupported. The second argument is an exact literal column name, with no
folding, unquoting or whitespace trimming beyond ordinary character coercion.
Missing relations/schemas/columns and malformed input retain PostgreSQL
diagnostics; wrong signatures fail binding before strict NULL execution.
NULL arguments suppress catalog lookup after demanded children are evaluated.

Output always qualifies and safely quotes the actual sequence namespace/name,
so it can feed nextval/currval. Inquiries consume no generator values and use the
current statement's immutable, transaction-visible catalog. Held statements
observe later ownership/search-path changes at execution. TEXT metadata remains
known for success, NULL and empty results. Views, closed STABLE defaults and CHECK
retain symbolic inquiries; native Dump/Restore preserves existing ownership and
generator state without new serialization. No serial/identity DDL boundary is
widened. `pg_get_owned_sequence` is explanatory prose, not an alias.
Feature smoke:
`make check RUN='^TestReferenceSerialIdentityFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceSerialIdentityFeatureSmoke$'`.




### Integer range columns — change note

CREATE TABLE additionally admits `int4range` and `int8range`, including qualified
`pg_catalog` names and NULL/NOT NULL. Unknown literal text and text/byte binds
use the column's codec; `[1,4]` canonicalizes to `[1,5)`. Nonnull `empty`,
nonnull unbounded `(,)` and SQL NULL remain distinct. Range grammar, endpoint
overflow, canonical successor overflow and reversed bounds retain PostgreSQL
SQLSTATE/English diagnostics and literal/parameter provenance.
Metadata reports INT4RANGE/INT8RANGE without length or precision; native exports
canonical strings. Ordinary native Dump/Restore replays destination-context
quoted literals and preserves retained readers and atomic replacement.
Already-admitted range-backed domain columns replay canonical base text and NULL
through their declared destinations, without adding explicit range/domain casts
or changing stored-expression identity serialization.
This admission does not add range constructors/operators/functions, comparisons,
ordering/index providers, casts, typed range literals, arrays or multiranges.
See [integer range types](TYPES.md#integer-range-columns--change-note).
Feature smoke: `make check RUN='^TestReferenceIntegerRangeFeatureSmoke$'`;
pinned counterpart:
`make check RUN='^TestReferenceIntegerRangeFeatureSmoke$' REFERENCE=1`.

CREATE TABLE supports unqualified or schema-qualified names with SMALLINT/INT2, INTEGER/INT/INT4, BIGINT/INT8, NAME, TEXT, BOOLEAN/BOOL, NUMERIC/DECIMAL/DEC[(p[,s])], REAL/FLOAT4, DOUBLE PRECISION/FLOAT8, FLOAT[(p)] (1–24 real, 25–53 double; omitted p double), DATE, TIME[(0–6)] [WITH | WITHOUT TIME ZONE], TIMESTAMP[(0–6)] [WITH | WITHOUT TIME ZONE], TIMETZ[(p)], TIMESTAMPTZ[(p)], UUID, BYTEA, BIT[(n)], BIT VARYING[(n)], VARBIT[(n)] and optional NULL/NOT NULL; INSERT VALUES supports optional target columns/multiple rows; SELECT supports mixed stars or scalar literal/query-column/parameter targets with FROM, scalar output labels, binary scalar comparison leaves and Boolean AND/OR/NOT with grouping in targets and WHERE, bare scalar WHERE in contextual BOOL, and expression/output-label/ordinal ORDER BY with multiple keys and per-key ASC/DESC/NULLS FIRST/LAST. FROM remains required outside identity/settings special forms and the selected null-count-only form. Values include exact decimal fractions/exponents, integer/radix literals inferred INT4/INT8/NUMERIC by signed range, quoted strings, raw B/X bit literals, TRUE/FALSE/NULL, 1-based `$n` and explicitly typed date/time/timestamp literals. Float4 stores binary32, Float8 binary64; both export float64, and all NaNs compare equal above non-NaNs. Temporal input/output uses captured TimeZone/DateStyle, BC/infinity and existing precision sizing; prepared constants freeze while parameters reread execution start. Finite public timestamps are time.Time, infinities owned bytes; time.Time parameters import civil fields for TIMESTAMP and preserve instants for TIMESTAMPTZ. Quoted identifiers, nested comments, doubled quotes/apostrophes and newline string concatenation follow README's C-locale UTF8 subset. Unresolved output parameter occurrences fall back to TEXT after filter analysis, retaining their initially known type: a target-first unknown occurrence conflicts with a later non-TEXT constraint (`42P08`), while TEXT target/filter reuse works. Same-context repeats work, unused gaps fail, mixed assignment destinations remain unsupported. Text/parameter inputs coerce through type input functions; INSERT assignment casts and comparison operator checks differ. WHERE keeps only TRUE. Defaults remain ASC NULLS LAST/DESC NULLS FIRST; explicit NULL placement overrides direction. See [expression ordering](QUERIES.md#expression-ordering--change-note-changelog).

### PRIMARY KEY constraints — change note

CREATE TABLE admits named/unnamed scalar column and ordered composite table
PRIMARY KEY declarations. Each retained key has kind `p`, separate implicit
NOT NULL identities, and an actual unique B-tree attachment. Duplicate/NULL
keys fail23505/23502 with current constraint/column identity; failed writes and
TYPE builds expose no partial rows or roots. Identical explicit UNIQUE keys can
merge into an explicit primary declaration; independent LIKE copies do not.
Existing DROP/rename/schema/REINDEX/LIKE/ON CONSTRAINT and native Dump/Restore
use the same group. Dropping the primary key leaves NOT NULL; dropping a live
key's NOT NULL fails42P16 (identity-column ALTER COLUMN retains earlier42601).
Named NOT NULL DROP reaches the primary-key guard first even on an identity key.
Actual key TYPE remints affected p/index/n identities.
ALTER ADD PK (including ADD COLUMN/USING INDEX), deferrability,
WITHOUT OVERLAPS and primary index parameters remain explicit capabilities
outside the native surface. Full-input syntax errors precede those exclusions.
Foreign keys remain external. See
[the complete contract](TABLES.md#primary-key-constraints--change-note).
Smoke: `make check RUN='^TestReferencePrimaryKeyFeatureSmoke$'`;
use identical RUN with `REFERENCE=1` for the pinned PostgreSQL18.0 oracle.

### INSERT conflict omission — change note

The existing INSERT producers admit `ON CONFLICT DO NOTHING`, optionally using
column/expression inference with COLLATE/class and a partial inference predicate,
or `ON CONSTRAINT`. All matching installed unique providers participate, with
typed/provider equality and NULLS DISTINCT/NOT DISTINCT semantics. Omitted
proposals do not suppress defaults, identity, generated, NOT NULL or CHECK
failures. Selected unique or supported nondeferrable exclusion collisions are suppressible.
Partial inference preserves nested SQL three-valued values and follows released
conservative proof rules, not every mathematical Boolean implication. For example,
`active IS TRUE` does not infer an index whose predicate is bare `active`.
Stable-isolation selected collisions use checked immutable storage positions;
ordinary enforcement and read-committed omissions do not scan for row identity.
See [INSERT conflict omission](QUERIES.md#insert-conflict-omission--change-note).
DO UPDATE and INSERT RETURNING retain positioned0A000 suffix boundaries.
Native deferrable UNIQUE DDL remains0A000. Supported equality exclusion constraints
participate through omitted/named targets, but not unique inference. Selected
deferrable exclusion arbiters fail55000 only for demanded proposals; zero-row
INSERT succeeds. Conflict-bearing Restore input and generic-plan cache policy
remain unchanged.

### Exclusion constraints — change note

CREATE TABLE and existing ALTER TABLE ADD CONSTRAINT support real equality
exclusions over admitted B-tree/HASH scalar families. Original operand types,
commutativity and family membership are checked independently of class input
compatibility. Immutable key expressions and optional TRUE-only predicates reuse
the installed index binder; FALSE/NULL predicates skip key evaluation and any
NULL key prevents a conflict. Constraint-local HASH WHERE does not admit standalone
partial HASH CREATE INDEX. HASH's existing one-key/no-INCLUDE limits remain.
DEFERRABLE and initial modes use selective actual row/version recheck obligations.
SET CONSTRAINTS forces only due events; transaction completion, savepoints and
prepared transfer reuse existing owners and completion/deadlock/cancellation
resources. READ DEFERRABLE is unrelated. Uncontested deferred admission does not
invent an obligation for later peers. Ordinary query MVCC visibility is unchanged.
Native metadata adds contype x and symbolic pg_get_constraintdef to existing
projections, not SQL pg_index or new pg_constraint columns. Native process-local
Dump/Reset/Restore preserves the real definition/index closure; physical/WAL,
extension, GiST/SP-GiST/range and server pg_dump support is not implied.
See [the full contract](TABLES.md#exclusion-constraints--change-note) and
[design](design/PG18-DDL-EXCLUSION.md).
Feature smoke:
`make check RUN='^TestReferenceExclusionFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceExclusionFeatureSmoke$'`.


### JSON textual IO — change note

JSON columns, direct/prepared insertion, bound text/bytes and typed literals
retain exact whitespace, duplicate keys, order, numeric and escape spelling.
SQL NULL differs from nonnull JSON `null`. Syntax-only validation accepts valid
zero/lone-surrogate escapes; malformed escapes/syntax report22P02 with JSON
Context and original SQL literal Position. Raw invalid UTF8/NUL is encoding22021.
Physical EOF in a Unicode escape is an invalid token; an actual nonhex character
retains the four-hexadecimal-digits Detail.
Standard/postfix casts support JSON identity, explicit TEXT↔JSON and assignment/explicit JSON↔JSONB IO, through
the shared expression binder. JSON typed literals compose in table and ordinary
no-FROM SELECT without a syntax-only per-target witness. The local no-FROM unit
does not broaden any type's cast or operator providers.
Generic JSON[] and domains reuse existing associations. JSON has no ordinary
equality/order/group/hash or B-tree provider. GROUP BY denial retains the key's
original SQL source Position. Ordinary SELECT DISTINCT is supported, but JSON
keys, including JSON domain/array keys, fail equality analysis with 42883.
pg_type projections remain outside the catalog surface.
Missing default B-tree classes report42704 with the existing Hint for both direct
and expression keys, including atomic ALTER TYPE reconstruction. Explicit
incompatible classes retain42804. JSON INCLUDE payloads require no comparator
and support row maintenance and native dump replay under a supported ordered key.
JSON operators/functions and SQL/JSON `json(...)` construction
are excluded. See [JSON storage and metadata](TYPES.md#json-textual-storage--change-note).
Native Dump/Restore and stored defaults retain exact text/type identity.
Smoke: `make check RUN='^TestReferenceJSONFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceJSONFeatureSmoke$'`.

### JSONB normalized IO — change note

JSONB columns and typed literals normalize whitespace and decoded object keys,
with duplicate-last semantics, exact decimal output and Unicode decoding.
Numeric range, U+0000 and invalid surrogate pairs are rejected without changing
source-preserving JSON behavior. SQL NULL remains distinct from JSON null.
Ordinary comparison, grouping, DISTINCT, UNIQUE and generic B-tree keys use
structural equality (numeric scale ignored) and the released `jsonb_ops` class.
Explicit TEXT↔JSONB and assignment/explicit JSON↔JSONB IO casts, array/domain
associations, metadata and logical native persistence reuse central providers.
JSON operators/functions, GIN and scalar constructors remain excluded.
See [JSONB storage](TYPES.md#jsonb-normalized-storage--change-note).
Smoke: `make check RUN='^TestReferenceJSONBFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceJSONBFeatureSmoke$'`.


### JSONPath typed IO — change note

JSONPATH columns, typed and pg_catalog-qualified literals, standard/postfix casts
and destination-inferred binds validate the full PostgreSQL18 grammar and retain
an immutable reusable syntax tree. Canonical output quotes keys/variables,
preserves strict mode and exact numeric scale, and normalizes parentheses,
comments, escapes and regex flags. The private input-time ARE/ERE/BRE validator
admits released regex syntax, not Go's RE2 subset; it has no matching API.
Scanner/grammar errors, Unicode failures, numeric overflow and2201B regex errors
use central English diagnostics with original SQL literal Position or existing
redacted portal Context for binds. Owned bytes, NULL, arrays/domains, defaults,
views and atomic native persistence use existing providers. Explicit character
IO casts size canonical output with ordinary character modifiers; typed character
assignment and incompatible SQL casts stay rejected. Native JSONPATH/string
metadata is independently asserted despite lib/pq's blank name/interface{} metadata.
No typmod, collation, comparison/group/hash/B-tree provider, path evaluator,
JSON query function/operator, SQL/JSON query form or GIN is introduced.
Unsupported SQL comparison, ordering and grouping reject through the existing
capability guards; canonical datum identity is only for stored representations.
Array/domain composition does not grant these SQL capabilities, and diagnostics
retain the logical type name and source position.
See [JSONPath contract](TYPES.md#jsonpath-typed-input--change-note).
Smoke: `make check RUN='^TestReferenceJSONPathFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'`.

### Interval values — change note

The supported type surface also includes INTERVAL with every permitted field
range and precision declaration, verbose/SQL/ISO8601 input, NULL/prepared binds,
comparison/grouping and native round trips. Month/day/time representations stay
distinct even where SQL30-day-month/24-hour-day comparison considers them equal.
IntervalStyle is real per-session input/output state; prepared constants capture
input at Prepare and cursor output at Execute. See [types](TYPES.md#interval--change-note)
and [settings](SETTINGS.md#intervalstyle--change-note).
Malformed/range/precision errors use originating PostgreSQL logical diagnostics
and atomic publication. [Date/time arithmetic](#date-and-time-arithmetic--change-note)
adds selected operators separately; wire protocol and physical storage remain
excluded. Selected constructors, justification and extraction are documented below.
Smoke: `make check RUN='^TestIntervalSmoke$'`.

### Interval justification — change note

The strict IMMUTABLE signatures `justify_days(interval)`,
`justify_hours(interval)` and `justify_interval(interval)` return unrestricted
base INTERVAL (OID1186, typmod-1), including for restricted or domain inputs.
UNKNOWN literals, bare NULL and positional parameters infer INTERVAL; NULL
propagates. These functions accept one unnamed argument.

`justify_days` carries each 30 days into months and repairs opposing month/day
signs, leaving the clock unchanged. `justify_hours` carries each 24 hours into
days and repairs opposing day/clock signs, leaving months unchanged. Combined
justification normalizes both and aligns nonzero signs: `1 mon -1 hour` becomes
`29 days 23:00:00`. Neither interval equality nor ordinary arithmetic implicitly
justifies stored representations.

Combined normalization checks the release-prescribed intermediate month carries
and first reduces same-sign days/clock to avoid a spurious day overflow:
`2147483647 days 24 hours` becomes `5965232 years 4 mons 8 days`, while
`justify_hours` on that value fails. Finite normalization overflow is
`22008 interval out of range`; input syntax/range errors remain22007/22015.
Both infinities pass through unchanged. Signature failures retain42883 with
the English explicit-cast Hint and character location. PostgreSQL's implicit
TIME-to-INTERVAL route is an explicit native0A000 capability boundary;
no new cast or named-function infrastructure is introduced.

Queries, grouping, joins, writes, defaults/views, generated columns and expression
indexes share the unary bound expression. Native Dump/Restore remints descriptor
ownership and retains statement atomicity and old cursors. Rendering uses the
existing Execute-captured IntervalStyle, not scalar arithmetic settings.
Public output is string/nil natively and byte-slice/nil through pq, with fixed
INTERVAL metadata even for empty/all-NULL results.

Smoke: `make check RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'`.
The complete shared family selector is `^TestReferenceDatetimeJustify`.

### Period overlap — change note

`OVERLAPS` retains its type/function keyword category: ordinary calls,
explicit `AS overlaps` labels and qualified identifiers such as
`public.overlaps` or `t.overlaps` are accepted. It does not become an unquoted
bare relation/column name or bare output label. Qualification followed by `(`
does not turn a CREATE TABLE relation name into a function call.

`(start,end) OVERLAPS (start,end)` accepts two-field implicit or explicit ROW
pairs. Each complete pair is ordered independently; nonzero periods are
half-open `[start,end)`, so touching boundaries do not overlap. Equal endpoints
denote a single instant: it overlaps a period at its start or interior, but not
at its end. Reversed duration/endpoints are ordinary normalized periods.

For each of TIME, TIMESTAMP and TIMESTAMPTZ, the four-argument overload accepts
endpoints, a duration in the second slot, a duration in the fourth slot, or both
durations. TIMETZ supports endpoints only: thirteen executable signatures in
total, also callable as `pg_catalog.overlaps(a,b,c,d)` or quoted lowercase
`"overlaps"`. DATE has no exact overload; existing implicit conversions and
PostgreSQL's preferred TIMESTAMPTZ selection apply before duration arithmetic.
All-DATE periods therefore depend on the Execute-time zone; a known TIMESTAMP
can select the plain timestamp signature. Zoned calendar days and elapsed hours
retain existing DST behavior. TIMETZ ordering includes its displacement tie-break.

OVERLAPS is non-strict. A partial pair has one known endpoint; against a complete
period it proves TRUE only when that endpoint is strictly interior, otherwise
the result is NULL. Two partial pairs or a both-NULL pair yield NULL. Duration
addition remains strict: a NULL start yields no effective known endpoint;
a known start plus NULL duration retains that start. Reached sibling errors are
not suppressed by a NULL endpoint.

Resolution is one four-slot function decision, not independent pair promotion.
All-UNKNOWN inputs are ambiguous (42725). Known TIMESTAMP inputs can resolve
UNKNOWN endpoints through the final same-known fallback; known TIME starts with
UNKNOWN second slots remain ambiguous because clocks also implicitly convert
to durations. OVERLAPS alone supports local TIME-to-INTERVAL conversion in
selected duration slots, preserving the raw 24:00 clock. No general TIME cast,
age/isfinite/justify boundary or arithmetic signature is expanded. Clock
addition wraps modulo one day and ignores interval calendar fields; the resulting
endpoints are normalized, not interpreted as a cyclic overnight period.

The result is BOOL (OID16, typmod-1), normally labelled `overlaps`, including
empty/prepared results. Queries, grouping, joins, writes, DEFAULT/CHECK, views,
immutable generated/index expressions and native Dump/Reset/Restore share the
same bound expression. Zoned duration/coercion expressions retain STABLE
admission rules. Public deparse preserves SQL versus ordinary-call provenance;
SQL-form DATE/clock pairs hide their implicit casts, while native logical
replay retains executable original clock arguments. Wrapper and inlined
arithmetic errors preserve their observed PostgreSQL Context fields; child
input/evaluation failures keep their own diagnostics.

Smoke: `make check RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'`.
The complete shared family selector is `^TestReferenceDatetimeOverlaps`.

### Age and finite predicates — change note

Selected strict signatures are `age(timestamp) -> interval`,
`age(timestamp,timestamp) -> interval`, and
`isfinite(date|timestamp|interval) -> boolean`. Binary age subtracts civil
fields, borrowing a partial month's days from the original earlier timestamp's
month. Thus June1 minus April30 is `1 mon 1 day`, not an elapsed-day interval
or `1 mon 2 days`. Months, days and microseconds remain separate; reverse age
negates each component. BC uses astronomical year0 for1BC, so1BC to1AD is1year.
This age/finite family adds no new temporal casts or excluded executable overloads.

Unary age subtracts from transaction-current local DATE at midnight. It is
STABLE and responds to Execute-time TimeZone and transaction start; binary
plain-timestamp age and all selected isfinite overloads are IMMUTABLE.
Finite predicates reject both infinity signs, not extreme finite components.
Age propagates signed infinity; subtracting equal-sign infinities reports
22008 `interval out of range`. Strict typed NULL returns a typed NULL; child
analysis/input and reached sibling errors retain their existing precedence.
IntervalStyle is captured by the output codec, not the arithmetic.

Calls compose through existing queries, writes, DEFAULT/CHECK, aggregates,
views and native Dump/Reset/Restore. Immutable selected calls may appear in
generated columns and expression indexes; unary STABLE age may not.
Ordinary, quoted lowercase and `pg_catalog` names retain namespace visibility.
UNKNOWN unary arguments remain PostgreSQL ambiguities (42725), even though
native implements fewer signatures. Binary age with an exact TIMESTAMP argument
accepts UNKNOWN/DATE via existing inference/coercion. All-UNKNOWN binary age,
DATE-only age and mixed TIMESTAMP/TIMESTAMPTZ resolve PostgreSQL's excluded
timezone overload and report native0A000 `age with time zone is unsupported`.
Explicit TIMESTAMPTZ isfinite reports0A000 `isfinite with time zone is unsupported`.
PG's TIME-to-INTERVAL implicit route retains native0A000
`time to interval conversion is unsupported`; TIMETZ is a wrong signature.
Explicit xid casts retain the existing native type-admission boundary:
0A000 `unsupported column type` at the type token, before function selection.
The diagnostic-only xid candidate
still participates in unary UNKNOWN age ambiguity. No raw-syntax bypass or
fake type/function catalog registration is provided. Wrong types/arity preserve
42883 English diagnostics, hints and character-based function positions.

Smoke: `make check RUN='^TestReferenceDatetimeAgeFiniteFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDatetimeAgeFiniteFeatureSmoke$'`.

### Datetime constructors — change note

The selected signatures return exact unsized values:

| Function | Parameters in declared order | Result |
|---|---|---|
| `make_date` | `year int4, month int4, day int4` | DATE |
| `make_time` | `hour int4, min int4, sec float8` | TIME |
| `make_timestamp` | `year int4, month int4, mday int4, hour int4, min int4, sec float8` | TIMESTAMP |
| `make_timestamptz` | the six timestamp parameters, optionally `timezone text` | TIMESTAMPTZ |
| `make_interval` | `years, months, weeks, days, hours, mins int4`, `secs float8` | INTERVAL |

Every interval parameter independently defaults to zero. Positional arguments
must precede named arguments; both `=>` and `:=` are admitted, with ordinary
identifier folding and exact quoted labels. Timestamp's third label is `mday`,
not `day`. Ordinary and `pg_catalog`-qualified calls use existing namespace
resolution and implicit coercion: int2 widens to int4, but int8/numeric does not
implicitly narrow. Seconds accept the existing numeric-to-float8 edges.

Years below zero mean BC; year zero is invalid. Calendar fields do not normalize.
Seconds round to nearest/even microseconds before clock checks; sec60 can carry,
TIME keeps24:00, and timestamp24:00 advances the civil date. DATE keeps its
wider civil range; constructors produce only finite timestamps strictly below
the TIMESTAMP END bound, independent of text-input typmod rounding. Interval
months/days/time stay distinct; checked years/weeks intermediate overflow cannot
be rescued by a later component. Huge finite seconds multiplication overflow
reports22003 separately from interval22008.

Omitted timestamptz zone uses Execute-time session TimeZone; explicit zones use
the same embedded IANA/abbreviation/POSIX provider with constructor numeric-zone
admission and ISO displacement sign. Rounded/carry-normalized civil fields choose
the DST gap/fold offset. Explicit NULL timezone returns NULL, not the session
zone. Presentation still uses captured output settings.
Numeric-zone diagnostics preserve embedded quotes and backslashes literally.

All constructors are strict. NULL does not suppress signature/input errors or
constant-child preflight errors (for example `make_date(NULL,1,1/0)`); runtime
nullable operands do not suppress reached failing siblings. Date/time/plain
timestamp/interval are IMMUTABLE; both timestamptz overloads are STABLE, even
with explicitUTC. Immutable constructors work in generated columns/expression
indexes; timestamptz defaults/views remain dynamic. Existing query/DML/CHECK,
group/join, stored remapping and native control consumers share the same bound
nodes. Stored definitions preserve supplied named order and omitted defaults;
deparse normalizes named separators to `=>`. No general named-function provider,
new temporal arithmetic, host timezone fallback or wire service
is added.

Smoke: `make check RUN='^TestReferenceDatetimeMakeFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDatetimeMakeFeatureSmoke$'`.

### Datetime extraction — change note

`EXTRACT(field FROM source)` returns unrestricted NUMERIC (OID1700).
`date_part(text, source)` returns FLOAT8 (OID701), with possible floating-point
precision loss. Both admit DATE, TIME, TIMETZ, TIMESTAMP, TIMESTAMPTZ and INTERVAL;
both are strict, but NULL does not hide malformed child input, parameter/domain
import failures or demanded child arithmetic errors. EXTRACT's field is an
identifier, quoted identifier, temporal keyword or string literal; date_part's
field is an arbitrary text expression, including held parameters and columns.
Unknown temporal sources remain ambiguous rather than defaulting to TIMESTAMP.

Calendar fields are century, day, decade, dow, doy, isodow, isoyear, julian,
millennium, month, quarter, week and year. Clock fields are hour, microseconds,
milliseconds, minute and second; zone fields are timezone, timezone_hour and
timezone_minute. Finite applicability is:

| Source/API | Fields |
|---|---|
| EXTRACT DATE | Calendar + epoch |
| date_part DATE | Calendar + clock + epoch, through midnight TIMESTAMP |
| TIME | Clock + epoch |
| TIMETZ | Clock + zone + epoch |
| TIMESTAMP | Calendar + clock + epoch |
| TIMESTAMPTZ | Calendar + clock + zone + epoch |
| INTERVAL | century, day, decade, epoch, hour, microseconds, millennium, milliseconds, minute, month, quarter, second, week, year |

DATE date_part errors identify timestamp without time zone, and dates outside
the timestamp range fail22008 even though EXTRACT can inspect them. The retained
literal/parameter SQL wrapper contributes `SQL function "date_part" statement 1`
Context when both arguments are immutable and not row/stage-dependent. A column,
STABLE or VOLATILE expression in either argument permits inlining and omits that
Context; parameter dependence alone does not. Wrapper annotation never replaces
child input/import/analysis diagnostics. Unsupported units report0A000; unknown units
report22023, retaining normalized field spelling. ASCII case folding and
multibyte-safe63-byte truncation do not trim whitespace or fold Unicode. Release
aliases include m/mm=minute, mon=month and first10-byte microsecon/millisecon/
timezone_h/timezone_m matches, including longer names sharing those prefixes.

Sunday is dow0/isodow7; ISO week/year can differ from civil year at New Year.
BC has no output year0. Julian is civil Julian day plus local clock fraction.
Intervals retain independent months/days/time: month is months%12, hour is not
mod24, minute/second use signed mod60 and week is days/7, ignoring clock hours.
Interval epoch counts whole years as365.25days and remaining months as30days.
Quarter preserves release int32 negation/remainder behavior, including quarter1
at minimum int32 months. It does not justify or normalize the interval.

Integral fields/microseconds have NUMERIC scale0, milliseconds scale3 and second
scale6. Epoch has scale6 except EXTRACT DATE's integral epoch. Julian TIMESTAMP
uses PostgreSQL numeric division display scale (midnight28, noon20). Large
timestamp epoch uses release default numeric division rounding followed by
scale6, not a float intermediate or an idealized exact-microsecond replacement;
the maximum timestamp therefore yields `9224318016000.000000`.

DATE/TIMESTAMP/TIMESTAMPTZ infinity preserves its sign for epoch, julian, year,
isoyear, decade, century and millennium; oscillating fields return typed NULL.
DATE EXTRACT still rejects unsupported clock/zone units; infinite TIMESTAMP
zone fields return NULL despite finite TIMESTAMP rejecting them. Infinite
INTERVAL preserves its sign for epoch, hour, day, year, decade, century and
millennium; its supported remaining fields return NULL and unsupported ones
still fail. Results are genuine typed NUMERIC/FLOAT8 infinities, not strings
under false metadata.

TIMESTAMPTZ local fields use the Execute-time session zone at the stored instant;
epoch remains UTC. TIMETZ keeps its fixed offset across session changes, returns
seconds east of UTC and signed hour/minute components, and does not wrap epoch
at midnight. TSTZ overloads are STABLE; others IMMUTABLE absent stronger children.
Immutable calls work in generated columns/indexes; stable defaults/views stay
dynamic. Grouping, aggregate arguments, writes/RETURNING, table CHECK and
unconstrained domain sources, stored renames/remapping and native controls use
the same provider-owned plans. Constrained domains remain an existing native
capability boundary; extraction does not implement their checks/defaults.
The shared references separately assert native rejection and real PostgreSQL
domain failures, while selected extraction uses plain domains and table CHECK.
Existing scalar-function admission boundaries also remain: the references keep
the current_setting field and random comparison CASE native boundaries alongside
their independently observed PostgreSQL extraction errors. A supported
CURRENT_DATE-derived field exercises STABLE wrapper inlining; the actual bound
random provider supplements VOLATILE propagation without widening SQL admission.
Ordinary and partial B-tree extraction expression indexes are shared supported
consumers. Partial predicates exclude NULL/FALSE rows; references retain direct/
prepared creation, filtered results, current definitions and PostgreSQL descriptors.
COUNT(EXTRACT(...)) exercises the supported aggregate child path with independent
INT8 counts. SUM(EXTRACT(...)) is a shared direct/prepared supported consumer:
February2 plus January1 returns exact NUMERIC `"3"` through numeric SUM.
Catalog definition tests use existing same-OID pg_attrdef/pg_class joins, not
new regclass-to-OID casts.
Later-row extraction NULLs into NOT NULL targets preserve statement atomicity
and full23502 diagnostics. Extraction runtime errors in explicit transactions
produce normal aborted25P02 behavior; rollback restores staged writes and permits
successful extraction on the same connection/held statement. Native raw result
frames retain captured NUMERIC/FLOAT8 values and metadata across subsequent zone
changes and physical source-session teardown, independently of fresh executions.
PG catalog rendering preserves SQL EXTRACT field text verbatim, including unusual
fields that PG itself does not render as replayable SQL. Native logical Dump
separately quotes fields safely and rebinds every descriptor/cast during Restore.
No date_trunc/date_bin/justify, new cast family or general function framework is added.

Smoke: `make check RUN='^TestReferenceDatetimeExtractFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDatetimeExtractFeatureSmoke$'`.

### Date and time arithmetic — change note

PostgreSQL Table 9.32 operators use SQL-selected operand/result types:

| Family | Operators and results |
| --- | --- |
| DATE | date ± integer → DATE; date − date → INTEGER; date ± interval → TIMESTAMP |
| DATE with clocks | date + time → TIMESTAMP; date + timetz → TIMESTAMPTZ |
| TIMESTAMP / TIMESTAMPTZ | timestamp ± interval → same timestamp family; same-family subtraction → INTERVAL |
| TIME / TIMETZ | clock ± interval → same clock family; time − time → INTERVAL |
| INTERVAL | +, −, unary −, interval × double precision, double precision × interval, interval ÷ double precision → INTERVAL |

Addition commutes where documented, including interval + date/timestamp/clock.
There is no direct date − time, timetz − timetz, interval − timestamp or
scalar ÷ interval operator, and no unary + interval. Existing implicit casts
may select an overload: mixed timestamp/timestamptz subtraction promotes to
timestamptz. PostgreSQL date − time succeeds through its TIME→INTERVAL cast;
native retains the existing unsupported-cast boundary (`42883`) on that same SQL.

Calendar arithmetic adds months once, clamps to the destination month end,
then adds days, then microseconds, checking each finite stage. BC/AD civil
dates have no SQL year zero. Date's wider domain still admits day arithmetic,
date subtraction and comparison, but cannot always promote to timestamp.
TIMESTAMPTZ calendar months/days resolve each session-local destination through
embedded zone rules before proceeding to the next stage; microseconds are
elapsed instant time. Thus Denver `2005-04-02 12:00-07` + one day becomes noon −06,
while +24 hours becomes 13:00 −06. New York `2018-02-11 02:30-05` + one month
and one day becomes `2018-03-12 03:30-04`: the month stage first resolves the
March11 gap to03:30, and the day stage retains that resolved clock.
A zoned day stage may temporarily reach local Julian−1 if resolving the
destination zone produces a valid instant at or above the timestamp minimum.
Each resolved instant still has to be within the finite timestamp range;
this does not widen DATE, plain TIMESTAMP, input or cast domains.
Timestamp subtraction measures elapsed stored microseconds and carries whole
24-hour chunks into interval days. Time subtraction retains a time component.
Clock ± interval ignores months/days, wraps the raw signed64 clock arithmetic
before reducing modulo24 hours and preserves TIMETZ's complete fixed offset.
The finite64 wrapping behavior is specific to clocks, not timestamp/interval math.

Interval addition/subtraction preserves separate month/day/microsecond fields
without justification. Scalar operations truncate month/day components, cascade
30-day months and24-hour days with PostgreSQL float8 rounding stages, and round
the final microseconds ties-to-even. Interval infinity uses widened comparison
span signs for infinite multiplication; indeterminate operations fail `22008`.
Division by ±0 fails `22012`; a typed NULL interval divided by zero is NULL.
Finite calendar/component overflow is `22008`; unsupported overloads are `42883`,
ambiguous unknown operands `42725`, with operator-token diagnostic positions.
Runtime arithmetic diagnostics carry no invented position. Each child retains
the existing demand/evaluation order; NULL never conceals a failing child.

The selected result owns unrestricted typmod−1 and no source origin. Aliases,
empty/all-NULL metadata and native driver codecs are preserved: DATE/TIME/TIMETZ/
INTERVAL strings, INTEGER int64, finite timestamps time.Time, infinities owned bytes.
Computed INTERVAL results admit the selected one-way explicit INTERVAL→TEXT
output edge with postfix, standard and `text(...)` spellings. It uses the existing
interval output codec and captured IntervalStyle (all four styles), so the
surviving output coercion is STABLE. This does not add TIME→INTERVAL or broader
cross-family casts; raw INTERVAL metadata/codecs are unchanged.
Nine ordered DATE/TIMESTAMP/TIMESTAMPTZ comparison pairs and same-family clocks/
intervals support all six comparisons and SQL NULL propagation. Existing implicit
TIME→TIMETZ comparison uses the execution-session offset. Existing interval
30-day comparison and TIMETZ UTC-clock/offset tie-break remain unchanged.

TIMESTAMPTZ ± interval (including zero) and commuted interval + TIMESTAMPTZ
are intrinsically STABLE; plain timestamp arithmetic and timestamptz subtraction
are IMMUTABLE absent stronger children. Held prepared constants freeze input,
parameters/settings use the execution snapshot and cursors retain output settings.
Closed STABLE defaults remain symbolic across Dump/Restore and evaluate on INSERT;
surviving STABLE generated/index expressions fail `42P17` at existing Execute DDL.
Native ordinary arithmetic projections remain row-owned on empty input, unlike
PostgreSQL's eager constant/custom planning errors. Existing bounded conditional
folding still diagnoses demanded row-dependent CASE constant failures.
Native logical replay, staged atomicity, cancellation and retained cursor ownership
use existing controls; this arithmetic family adds no functions or cast families.

Feature smoke: `make check RUN='^TestReferenceDatetimeArithmeticFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeArithmeticFeatureSmoke$'`.

### Datetime formatting — change note

The selected strict STABLE signatures are `to_char(timestamp,text) -> text`,
`to_char(timestamptz,text) -> text`, `to_char(interval,text) -> text`,
`to_date(text,text) -> date` and `to_timestamp(text,text) -> timestamptz`.
Ordinary and `pg_catalog`-qualified calls share scalar binding and compose in
the existing query, DML, DEFAULT, CHECK, view and introspection consumers.
Datetime formatting uses ordinary no-FROM SELECT, including existing scalar
siblings and WHERE. INSERT expressions use the
existing INSERT surface; INSERT RETURNING remains an unsupported suffix, while
UPDATE/DELETE RETURNING consume formatting expressions.
Their STABLE classification excludes stored generated columns and expression
indexes, even for fixed English names or timezone-free sources.
Stored-view cast presentation uses captured type usage and canonical postfix
syntax. Already-typed literals retain nested-cast parentheses outside function
arguments; pretty function arguments preserve existing flattening. Replay keeps
explicit CAST syntax, and qualified domain names follow the supplied coherent
catalog/path rather than the original schema spelling.

Templates recognize PostgreSQL 18's case-specific calendar/ISO, clock, fraction,
era, ordinal, Roman-month and zone tokens. `FM` affects the next field; `TM`
uses the captured lc_time C/POSIX English-name provider; `TH`/`th` add ordinals and uppercase `SP` is
a no-op. Lowercase `sp` remains literal text.
Quoted text and escapes are template syntax, not Go layouts. Output preserves
literal text, name padding and interval's separate month/day/time fields:
interval hours may exceed 23 and do not normalize days. Calendar names,
weekday/era and zone fields that require an actual date reject intervals.
Meridiem fields remain valid for intervals. Empty output masks and infinite
sources produce NULL.

Input completion follows PostgreSQL formatting rules, not ordinary temporal
casts or DateStyle parsing. Short years are adjusted around 2020; ISO and
Gregorian conventions cannot be mixed. Non-FX whitespace and separators are
permissive; FX consumes exactly one source character per template separator
but does not require that character to match. Quoted input text skips source
characters rather than validating equality. Missing fields, trailing source,
duplicate zero/nonzero fields and precedence use release formatting semantics.
An empty source may leave a field unvisited and retain parsing defaults. A
numeric or named field already reached before whitespace exhausts the source
still demands its reader and can report an empty-field error; this is not
general full-template or full-source validation.
DATE is finalized within its own wider civil range, not through TIMESTAMP.
Milliseconds and microseconds are additive; formatted timestamp fractions
must stay below one second and clocks cannot exceed 23:59:59.

TIMESTAMPTZ output uses the Execute-captured session zone, including historical
abbreviations and second-resolution offsets. Numeric input offsets and the
execution-selected embedded Default/Australia/India abbreviation tables are
supported. TZ/tz readers prioritize session-zone abbreviations, then the selected
table; they do not read full IANA names. Customized installation files, runtime
file loading and non-C locale configuration are not expanded.
Integral-hour `OF` output omits `:00`.
NULL skips formatting itself, not errors from demanded child expressions.
Indeterminate implicit collation is diagnosed only when a reached `TM` name
needs case conversion. Exact localized input spelling bypasses conversion;
input fallback first demands `upper()`. Earlier parse/interval errors and
unvisited names take precedence, and NULL/infinite output does not demand names.
Returned values and metadata retain their original execution snapshot.

Numeric formatting is a separate implemented family described below.
One-argument epoch `to_timestamp` remains outside this datetime family and
native reports 0A000 `epoch to_timestamp is unsupported`.
PostgreSQL's implicit TIME-to-INTERVAL formatting route depends on a cast not
provided by the current native temporal substrate; native reports 0A000
`time to interval conversion is unsupported`. This does not implement that
adjacent cast. Typed ARRAY construction is supported by the array expression
family; it does not add nonexistent VARIADIC datetime function signatures.
The string-category resolver, DATE's implicit source promotion and namespace
visibility retain the existing overload rules. No temporal arithmetic,
new general casts or wire protocol is added. Locale category settings use the
separate bounded C/POSIX provider described in [session settings](SETTINGS.md#format-encoding-and-locale-settings--change-note).
Independent acceptance: `make check RUN='^TestReferenceDatetimeFormatting'`;
the pinned PostgreSQL counterpart adds `REFERENCE=1`.

### Numeric formatting — change note

The strict STABLE signatures are
`to_char(integer|bigint|numeric|real|double precision,text) -> text` and
`to_number(text,text) -> numeric`. SMALLINT chooses the preferred FLOAT8
overload; known types and domain bases retain their exact overload. Unknown
first arguments to `to_char` remain ambiguous. Result metadata is unrestricted
TEXT/NUMERIC, independent of the mask or first returned value.

Numeric masks recognize `9`, `0`, period/comma, `D`, `G`, `L`, `S`, `MI`,
`PL`, `SG`, `PR`, `RN`/`rn`, `TH`/`th`, `V`, `EEEE` and global `FM`.
Fixed UTF8/C output uses period, comma, plus/minus and an ASCII currency
space, not a dollar sign. Repeated `FM` sets rather than toggles fill mode;
mixed-case spellings remain literals. Quotes and escapes follow PostgreSQL
templates, including unmatched quoted text and the `SP` token collision.
Descriptor-order validation preserves accepted nonstandard token placements.
`D`, `G` and `S` read the captured lc_numeric policy; `L` reads lc_monetary.
Both categories support C/POSIX/empty-as-C only, without host-locale loading;
literal period/comma remain independent mask characters.

Integer and NUMERIC values retain exact coefficients; NUMERIC rounding is
half away from zero. REAL/DOUBLE PRECISION use binary rounding, six/fifteen
precision caps and signed zero. Roman output rounds first (float ties use
nearest/even), pads to fifteen characters unless FM, and emits fifteen hashes
outside 1–3999. Roman input reads a case-insensitive valid prefix.
Scientific output preserves large numeric exponents; NUMERIC/BIGINT mantissa
rounding carry does not renormalize the original exponent; INTEGER scientific
output instead uses printf-style binary rounding after exact promotion.
INTEGER `V` follows the pinned 32-bit conversion/product behavior; BIGINT
`V` checks the power conversion and product and reports22003 on overflow.
NUMERIC `V` output shifts exact decimal digits. `V` input multiplies by the
already-rounded power factor with calculation display scale capped at1000:
an underflowed factor remains zero even for a large input magnitude.
Input's private encoded precision/scale follows release sizing, without
mistaking the public declaration precision limit1000 for a long-mask limit.

`to_number` consumes template actions permissively: groups need not be present,
fraction scale follows consumed digits, literals skip source codepoints, and
trailing source can remain unused. It does not strip all nondigits or enforce
full-source width. Empty output masks produce empty TEXT; empty input masks
produce NUMERIC NULL. EEEE input reports 0A000. Mask and numeric/Roman parsing
errors retain PostgreSQL English diagnostics and an empty runtime Position.
Valid Prepare defers invalid masks to Execute; strict NULL bypasses formatting
only after both demanded children evaluate. Unreachable CASE arms stay
unreached. Malformed typed numeric inputs retain existing conversion precedence.

Queries, stored DEFAULT/CHECK/view definitions, existing writes, introspection
and native Dump/Reset/Restore use the same provider and expression walkers.
Closed DEFAULT compositions containing the selected UUID generators recurse
through both numeric-provider arguments using the existing admission walker.
For example, `to_char(num_nulls(uuidv4()),'FM9')` and
`to_number(format('%s',num_nulls(uuidv4())),'9')` produce TEXT/NUMERIC0 for each
new row. Dump/Restore retains symbolic definitions and reevaluates them for
new DEFAULT VALUES. This does not admit arbitrary volatile descendants,
parameters, row/stage dependence or volatile UUID shift expressions.
STABLE calls remain illegal in generated columns and expression indexes.
The inherited repeated-parameter `to_char($1::numeric,$1::text)` NUMERIC→TEXT
conversion still reports native 0A000; separate typed parameters and existing
supported casts are unchanged. This narrow adjacent cast boundary is not an
alternative formatting overload.
Independent acceptance:
`make check RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)'`;
the pinned counterpart adds `REFERENCE=1`. Feature smoke selects
`^TestReferenceNumericFormattingFeatureSmoke$` in both modes.


FROM supports comma products, CROSS JOIN, INNER/LEFT/RIGHT/FULL JOIN ON or USING,
optional OUTER for non-inner kinds, and NATURAL variants, chains and parentheses. Table/whole-join aliases with or
without AS support positional column aliases; qualified columns and mixed stars
resolve the visible namespace. USING's special AS alias exposes merged keys
without hiding operands. USING key order and NATURAL left-common-column order,
duplicate multiplicities and NULL nonmatch follow PostgreSQL18.0. Unmatched
operands receive typed NULLs before WHERE. LEFT/RIGHT merged keys select their
coerced preserved operand; FULL uses COALESCE with no physical output origin.
Every source is authorized and captures one coherent selected statement view.
Prepared execution rebinds all sources; eager results survive native Reset/Restore.
LATERAL joins, CTE and function FROM remain excluded; derived SELECT and
parenthesized VALUES are supported non-lateral FROM operands.
See [join contracts](QUERIES.md#inner-and-cross-joins--change-note).
Feature smoke: `make check RUN='^TestReferenceQuery(OuterJoins|Joins)'`;
independent pinned reference: `make check REFERENCE=1 RUN='^TestReferenceQuery(OuterJoins|Joins)'`.

Ordinary SELECT DISTINCT deduplicates complete projected rows using type-owned
NULL-equal key semantics, after WHERE/grouping/HAVING and before ORDER/OFFSET/LIMIT.
Projection is evaluated once per source occurrence; ORDER reads projected values
and must reference a projected expression, output alias or ordinal. Hidden keys
fail42P10 and all four locking modes fail0A000, also through DISTINCT views.
Native metadata, held execution, INSERT SELECT source-type equality and
Dump/Restore retain the same contract. Arrays/domains use structural key equality.
Enums and enum domains use declaration-ordinal keys. Explicit ORDER aliases and
ordinals preserve the selected visible occurrence, including independent repeated
volatile projections. Projected enum-domain arrays do not widen array storage.
Ordinary array operators/order remain excluded; ON key equality does not add ordering.
No implicit order is promised.
See [distinct projected rows](QUERIES.md#distinct-projected-rows--change-note-changelog).
Feature smoke: `make check RUN='^TestReferenceQueryDistinctSmoke$$'`.

`SELECT DISTINCT ON (expressions)` retains first input occurrences after ORDER and
before OFFSET/LIMIT/FETCH. Hidden keys do not widen metadata or demand payload
equality. ON uses output alias/ordinal resolution and matches effective leftmost
ORDER identities, not textual list order; partial prefixes and duplicates are legal.
All four row-lock modes still reject DISTINCT queries. See
[distinct on groups](QUERIES.md#distinct-on-groups--change-note-changelog).

Persistent CREATE VIEW supports the current SELECT body, optional prefix output
names, security_barrier/security_invoker Boolean options, nested ordinary reads
and INSERT SELECT. Bound relation/attribute identities survive base renames and
unrelated column compaction; dependency-aware RESTRICT/CASCADE integrates with
existing mutation paths. Zero-column views preserve source occurrences.
Empty SELECT target lists use the same source path (`SELECT FROM relation`),
retaining row occurrences independently of output width.
`pg_catalog.pg_class`, `pg_rewrite`, `pg_views` and `pg_indexes` are real bounded normal query
sources with OID/internal CHAR metadata; their exact supported columns and
unsupported tree/array fields are listed in [the view contract](SQL.md#views).
`pg_catalog.pg_description` is another readonly normal query source, relation2609,
with exactly `objoid OID`, `classoid OID`, `objsubid INT4`, `description TEXT`.
All four columns are NOT NULL; TEXT uses C950, and there is no row `oid`.
The complete identity key is `(objoid,classoid,objsubid)`; whole objects use0,
columns use live stable attribute numbers. Its PUBLIC SELECT does not require
SELECT/schema USAGE on the described object. Supported TABLE/COLUMN/SCHEMA/
SEQUENCE comments and exact seeds for already represented types/collations/
conversions appear here; ROLE/DATABASE/TABLESPACE shared descriptions do not.
No new COMMENT kinds, inquiry routing, catalog inventories or SQL predicate
syntax are implied. Native mutation and row locking remain0A000 virtual-source
boundaries, unlike PostgreSQL physical superuser catalogs.
See [exact catalog contract and reference smoke](SQL.md#pg_description-catalog--change-note).
`pg_catalog.pg_shdescription` separately exposes shared ROLE comments:
relation2396, exactly `objoid OID`, `classoid OID`, `description TEXT`, all
NOT NULL (TEXT C950), with no row `oid` or `objsubid`. The key is actual
principal identity/class1260, not a name. Only exactly representable uint32
principal identities appear; no comments are fabricated for native bootstrap
roles. PUBLIC SELECT and inquiry expressions share the immutable role snapshot.
Shared roles/comments survive native Reset/Restore and are excluded from Dump.
DATABASE/TABLESPACE commands and role transaction mutation support are not
expanded. See [shared catalog contract](SQL.md#pg_shdescription-catalog--change-note).
`pg_catalog.pg_sequence` is a readonly exact eight-field definition source:
`seqrelid OID`, `seqtypid OID`, `seqstart INT8`, `seqincrement INT8`,
`seqmax INT8`, `seqmin INT8`, `seqcache INT8`, `seqcycle BOOL`.
Relation2224 has known NOT NULL native metadata and no row `oid` or generator
state. PUBLIC SELECT exposes definitions even without privileges on a private
schema/sequence, including peer TEMP definitions by numeric OID; it never opens
the generator. RC prepared execution refreshes rows; RR retains its existing
transaction snapshot with current own DDL overlays. Prepare inside BEGIN
acquires that snapshot, unlike a statement prepared before BEGIN. Savepoint
rollback restores overlays, not the committed snapshot. Old cursors survive
ALTER/DROP/native controls; Restore reproduces definitions with reminted OIDs.
No `pg_sequences`, extra catalog inventories or physical counters are added.
See [schema, lifetime and feature smoke](SQL.md#pg_sequence-catalog--change-note).

`pg_catalog.pg_depend` is readonly relation2608 with exactly `classid OID`,
`objid OID`, `objsubid INT4`, `refclassid OID`, `refobjid OID`,
`refobjsubid INT4`, `deptype CHAR`, all known NOT NULL. Object addresses use real
catalog class/OID/stable attributes, including tableoid -6; no row `oid`.
PUBLIC SELECT is not filtered by endpoint privileges. Derived normal/auto/internal
edges cover represented table/view/sequence/type/default/generated/constraint/
index/rule owners. Pinned references are absent; public is unpinned. Domain arrays
are represented, automatic rowtypes and enum arrays are not. Catalog reads do not
expand REGCLASS column storage, TEMP-table creation or transactional DDL admission.
See [the exact dependency contract](SQL.md#pg_depend-catalog--change-note).

`pg_catalog.pg_cast` is readonly relation2605, PUBLIC SELECT, with ordered
`oid OID`, `castsource OID`, `casttarget OID`, `castfunc OID`,
`castcontext CHAR`, `castmethod CHAR`, all known NOT NULL natively.
Exactly69 complete-context numeric/MONEY/OID/temporal/character/BOOL/typmod/
JSON edges are owner-declared. Contexts i/a/e mean implicit/assignment/explicit
with upward closure; methods f/b/i mean function/binary/I/O, not native opcodes.
Non-function methods have castfunc0. Ordinary automatic string I/O, domain/base
coercion and identity are not a row inventory. Incomplete NAME/REGCLASS/BIT/
VARBIT/PG_NODE_TREE/internal-CHAR families and absent JSONB-scalar/TIME-INTERVAL
providers remain omitted without changing existing conversion behavior.
Row IDs are stable local generated identities, not another server's allocator
sequence; source/target/function IDs and relation2605 are exact PostgreSQL IDs.
Native writes/row locking retain0A000 virtual-source boundaries. Existing native
controls rebuild fixed metadata and retain old captured values/descriptors.
No CREATE/DROP CAST or pg_type/pg_proc inventory is implied.
See [exact boundary, lifetime and smoke](design/PG18-CAT-PG-CAST.md).


CREATE OR REPLACE VIEW preserves view/rule/attribute identity, owner, ACLs,
comments and dependents while replacing the definition and outgoing dependencies.
Existing names/types/typmods/collations form a required prefix; trailing columns
may be added. ALTER VIEW supports relation/column rename, owner/schema changes
and security SET/RESET; grouped DROP VIEW supports RESTRICT/CASCADE and IF EXISTS.
Scalar COLLATE annotations support default/C/POSIX and the fixed Unicode16
`pg_c_utf8`/`pg_unicode_fast` identities; arbitrary locale/ICU providers remain excluded.
Cyclic replacement is admitted, but recursive reads fail42P17 and native cyclic
Dump explicitly fails. CREATE RECURSIVE/TEMP syntax, check options,
view column defaults/type-changing ALTER, full catalogs/RLS/UDFs/optimizer facilities
and explicit-transaction view DDL remain excluded.
Native fixture controls serialize current definitions, not materialized results
or alteration history.
Feature smokes: `make check RUN='^TestReferenceView(Rows|Barrier|SmokeCatalogReplayControls)$'` and
`make check RUN='^TestReferenceViewReplace(Definition|Diagnostics|Prepared|SmokeCycleReplayControls)$'`.

Automatically updatable simple one-relation views support INSERT/UPDATE/DELETE
through nested stable attribute mappings. Direct columns and same-type no-op
casts are writable; other casts, explicit COLLATE and computed outputs are
read-only. A mixed view remains writable through its direct columns. DELETE
also works through computed-only and zero-column simple views. DISTINCT,
GROUP/HAVING, set operations, bounds, top-level aggregate/window targets,
joins and derived FROM inputs reject writes with PostgreSQL 55000 reasons;
unsupported query syntax retains its parser boundary. Stored rowmarks do not
disqualify a view. Omitted computed outputs are not evaluated during safe writes.
The physical base supplies defaults/identity/generation, constraints, unique
arbitration and RX/row locks. Every view predicate guards UPDATE/DELETE and
latest-predecessor rechecks, but no implicit CHECK OPTION is introduced.
Logical result columns/OLD/NEW remain those of the requested view.
Main-source SELECT is conditional on logical command reads, not a hidden view
predicate. Independent stored-subquery sources retain SELECT under their layer's
current owner/original invoker. Frozen source layouts consume live selected
versions, including own transaction changes; Prepare binds without executor ACLs.
INSERT RETURNING, trigger/rule rewriting and RLS remain excluded.
Feature smoke: `make check RUN='^TestReferenceViewUpdatableSmoke$'`;
use the same selector with `REFERENCE=1` for the pinned PostgreSQL contract.

SELECT targets support `AS label` and eligible bare labels for view definitions
and normal output metadata. ORDER resolves output labels and ordinals alongside
input expressions and accepts multiple keys. WHERE/HAVING do not expose output
labels; GROUP BY retains its input-name priority and existing ordinal rules.

FROM column aliases rename input names; SELECT labels rename output names only
and are not visible to sibling targets or WHERE. Bare ORDER labels prefer
output names; qualified/compound ORDER expressions use input names. Missing
column Hints use exposed base relation/view aliases with PostgreSQL18 bounded
character distance, qualifier penalties and one/two-candidate tie handling;
three tied fuzzy occurrences and distant names suppress suggestions. Exact
inaccessible base columns instead take precedence and produce singular/plural
Detail, with PostgreSQL's table-qualified-name Hint for an unqualified request
matching multiple hidden entries. Duplicate exact column spellings within one
base entry instead raise positioned42702, regardless of qualifier penalty or
earlier exact entries. Diagnostics never change visibility or physical-column
authorization. Complete logical English
diagnostics and character positions have independent pinned18.0 reference
expectations. See [alias diagnostic fidelity](QUERIES.md#query-labels-and-relation-aliases--diagnostic-fidelity-change-note).
Feature smoke: `make check RUN='^TestReferenceQueryAliases'`.

SELECT accepts repeated adjacent `FOR KEY SHARE`, `FOR SHARE`, `FOR NO KEY UPDATE`
and `FOR UPDATE`, with optional per-clause `OF` and `NOWAIT`/`SKIP LOCKED`.
The complete list appears before or after bounds, after ORDER BY. Existing joins,
scalar views and INSERT SELECT share occurrence-owned row arbitration. OF uses
the relation's alias if present and can name unprojected contributors; a whole-join
alias does not hide child lock targets but is not itself lockable. FROM VALUES
aliases are legal copy-valued targets without grants. Stored marks apply only to
their selected subtree occurrences. Strength (KEY SHARE < SHARE < NO KEY UPDATE
< UPDATE) and policy (blocking < SKIP < NOWAIT) merge independently.
Enclosing marks reaching an outer join's nullable side reject on Execute after
successful Prepare, including empty/LIMIT0 queries. Local stored input marks
remain legal on either side of an unmarked outer query and settle at real-source
demand, never at absent extensions. FULL path-capability0A000 is also Execute-only:
immutable Boolean constants or a usable opposite-side equality conjunct admit it;
inequality alone or same-side equality does not. Native left-major demand is not
PostgreSQL's hash-build lock order: empty-left LEFT skips right input marks,
empty-right RIGHT scans left input marks. Selected/preserved physical marks and
LIMIT0 guarantees remain independently tested across backends.
An unmarked upper aggregate may consume a marked view: input locks precede
grouping/HAVING, so an empty HAVING result does not release them. A FOR clause
reaching GROUP BY/HAVING/aggregate or DISTINCT output fails mode-specific0A000.
Plain DISTINCT retains its existing projected-tuple execution provider.
UNION/INTERSECT/EXCEPT execute with DISTINCT/ALL bag semantics and pairwise common
typing; direct/result/arm set locking rejects before arity/common typing. Inline
derived sets also reject enclosing row marks. Stored set views remain opaque
copy-valued outputs: outer row marks do not propagate to arm row identities,
relation-lock modes or UPDATE authority, including catalog and sequence sources.
Every arm still requires its ordinary SELECT admission; locally stored marks
inside a descendant view retain their own authority and locking behavior.
DISTINCT ON retains full expression/type/ORDER analysis before its mode-specific
DISTINCT row-lock rejection. Its nonlocking execution is supported. Sequences fail42809
at Execute even as unselected reference-mark siblings of a marked query, without
new UPDATE requirements or physical grants on such siblings. Selected virtual
catalog marks reject0A000 `row locking is not supported for virtual catalog relations`,
including empty/nested paths; unselected catalogs remain ordinary copy sources.
READ ONLY fails25006 at Execute. At RR, KEY SHARE retains snapshot values after
compatible non-key updates; stronger modes/deletions fail40001. All read sources
need used-column SELECT; only selected physical targets additionally need UPDATE
on any live column. NOWAIT is atomic55P03 without joining the row queue; SKIP
drops the whole unavailable candidate without refreshing it. These policies do
not affect relation-lock waits. Acquired OFFSET-discarded survivors stay held;
unavailable rows consume no bound quota. Bounded views settle inherited marks
before their own counts, while plain scalar qualification stays deferred.
UPDATE uses NO KEY UPDATE for unchanged keys, UPDATE for changed UNIQUE or
PRIMARY KEY datums; DELETE uses UPDATE. Matrix, lifetime,
savepoint contribution restoration and exact diagnostics:
[row locks](TRANSACTIONS.md#row-locks--change-note).
Smoke: `make check RUN='^TestReferenceQueryLockFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceQueryLockFeatureSmoke$'`.
Complete family: `RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)'` in both modes.

OID is also a supported column type: distinct unsigned32 type26, native int64
results, unsigned comparison/order and directional integer casts. See
[OID scalar semantics](TYPES.md#oid--unsigned-scalar-change-note) for text syntax,
signed-bit reinterpretation versus checked INT8 conversion and adapter metadata.
It adds no metadata catalog population, object lookup/allocation or OID arithmetic.
Smoke: `make check RUN='^TestOIDSmoke$'`.

### LIMIT, OFFSET and FETCH — change note

Table and ordinary no-FROM SELECT, standalone/FROM VALUES, shared INSERT
SELECT/decorated VALUES and stored views accept LIMIT/ALL, OFFSET [ROW/ROWS] and FETCH
FIRST/NEXT [count] ROW/ROWS ONLY or WITH TIES. OFFSET and LIMIT/FETCH accept either order;
FETCH defaults to1. Bounds use BIGINT assignment conversion and evaluate once,
OFFSET first. NULL OFFSET is0, NULL LIMIT/ALL unlimited; negative bounds give
2201X/2201W. LIMIT0 skips producing children but retains full analysis, source/
destination authorization, metadata and bound diagnostics. Nonblocking predicates/
views/joins are demand-driven; Sort/aggregate and local count are effect/lock
frontiers. WITH TIES requires this query's ORDER BY and forbids SKIP LOCKED;
all complete-key peers at the quota boundary are retained using type/collation
comparison, with NULLs equal. Finite ties pulls the first nonpeer before stopping.
Raw NULL count under ties gives2201W; runtime expression/parameter NULL is unlimited.
Comma LIMIT remains excluded.
See [demand and persistence](QUERIES.md#limit-offset-and-fetch--change-note-changelog).
Smoke: `make check RUN='^TestReferenceQuery(Ties(FeatureSmoke|PersistenceSmoke)|LimitMigrated(Smoke|LockSmoke))$'`.
Use the identical selector with `REFERENCE=1` as well.


Table SELECT and shared INSERT SELECT also support scalar/multiple GROUP BY
keys, one-based output ordinals, contextual BOOL HAVING, count(*) and
count(expression) (count/INT8/int64). Expression count ignores NULL, not zero,
false or empty strings. Global empty/all-NULL input returns one count0;
empty keyed grouping returns no rows with full schema. HAVING alone creates a
global group, and hidden count calls can filter groups without changing output
metadata. Typed NULL keys compare equal; numeric scale, NaNs/signed zero and
BPCHAR padding use typed equality, not text keys. Group order is unspecified and
projection duplicates remain separate occurrences. Ungrouped SELECT/HAVING/ORDER
source leaves and aggregate calls in WHERE/GROUP BY give positioned42803.
Bare int32 integer keys are output ordinals (including minus/parentheses);
out-of-list keys42P10 and raw noninteger constants42601. Unary-plus numeric keys
are constant expressions, not ordinals. Count-star requires table SELECT or a
live-column SELECT grant; actual key/HAVING/hidden ORDER reads retain column ACLs.
Eager group finalization preserves cancellation, result ownership and INSERT
atomicity; public Next performs no aggregate work. See [grouping](QUERIES.md#grouping-and-count--change-note).

`bool_and(BOOL)`, `bool_or(BOOL)` and `every(BOOL)` return BOOL/Go bool with
known nullable metadata, including NOT NULL inputs. NULL inputs are ignored;
empty/all-NULL global input returns one NULL, empty keyed input no rows.
Every aliases AND behavior, not its raw label or diagnostic name. Unknown
string/NULL literals and bare parameters infer BOOL; typed TEXT/INTEGER arguments
give42883 with actual signature, explicit-casts hint and character call position.
All eligible argument occurrences evaluate even after a decisive Boolean state.

`avg(INTERVAL)` returns nullable INTERVAL/Go string, ignoring NULL and returning
NULL for empty/all-NULL input. Checked signed32 month/day and signed64 microsecond
sums can overflow even when the mathematical average would fit (`22008`,
`interval out of range`). Fractional months cascade at 30 days/month, fractional
days at 24 hours/day; nearest-even floating-point rounding preserves physical
components without justifying days/time. The result does not inherit source
field/precision restrictions. Mixed positive/negative infinities fail22008;
same-sign infinity dominates only after finite transitions have been checked.
Unknown strings, NULL and unconstrained parameters give42725 ambiguity; use an
explicit cast to a supported AVG input. Invalid types/arity give42883 with the
real signature, explicit-casts hint and character position.

`avg(SMALLINT/INTEGER/BIGINT/NUMERIC)` returns unrestricted nullable NUMERIC;
`avg(REAL/DOUBLE PRECISION)` returns unrestricted nullable FLOAT8. Empty/all-NULL
global input returns typed NULL; empty keyed input returns no rows, retaining
metadata. NUMERIC sums are exact, with base-10000 division scale capped at1000;
packing the finite sum can fail22003 even if the mean fits. NaN or opposite
numeric infinities return NaN; one infinity sign dominates finite sums.
REAL inputs widen from stored binary32. Float AVG retains the full
Youngs–Cramer state: sum or Sxx overflow fails22003, `value out of range: overflow`.
Final division permits IEEE underflow/signed zero; NaN/infinity follow IEEE
arithmetic. Result metadata drops input typmod and physical origin. Eager
execution preserves late-error/cancellation and INSERT SELECT atomicity, without
promising an unordered floating accumulation order.
Public smoke: `make check RUN='^TestReferenceNumericAvgSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceNumericAvgSmoke$'`.

Change note: numeric `sum` supports exactly SMALLINT/INTEGER→INT8,
BIGINT/NUMERIC→NUMERIC, REAL→FLOAT4 and DOUBLE PRECISION→FLOAT8.
NULL inputs are ignored; empty/all-NULL global input returns typed NULL,
empty keyed input no rows. Results are nullable, typmod-1, without physical
origin. BIGINT sums are exact and cannot wrap at signed64. NUMERIC retains the
maximum finite input display scale, including zero and cancellation, and
checks finite format range only at finalization (`22003`, `value overflows
numeric format`). NaN or opposite infinities yield NaN; a single infinity sign
dominates even an oversized finite subtotal. Floating SUM retains first-value
negative zero and rounds every addition at binary32/binary64 respectively.
Finite transition overflow is immediate22003, `value out of range: overflow`,
and later compensators/specials cannot rescue it. There is no addition-underflow
rule or unordered accumulation guarantee. Existing FILTER/DISTINCT/ALL/order,
direct/prepared, eager publication, ACLs and view replay contracts apply.
Unknown unary inputs fail42725; invalid types/arity/star fail42883 with the
ordinary signature/hint/character position. Runtime INTERVAL SUM remains native
0A000; unary typed INTERVAL RETURNING preserves42803, not missing-signature42883.
Public smoke: `make check RUN='^TestReferenceNumericSumSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceNumericSumSmoke$'`.

`any_value(anyelement)` returns an arbitrary non-NULL datum of the concrete input
type, not a stable first/last/minimum/maximum winner. Empty/all-NULL global input
returns typed NULL; empty keyed input returns no rows. Result metadata is known
nullable, retains input collation, drops source typmod and has no physical origin.
Existing codecs and immutable datum representations are unchanged. Unknown
NULL/string literals and unresolved parameters give42804 without a position;
cast the argument explicitly. Zero/star/multiple arguments give42883. All input
arguments evaluate even after a candidate is retained, preserving late-error
and cancellation atomicity. Direct/prepared calls, views/native roundtrip,
HAVING/ORDER and INSERT SELECT share the ordinary aggregate pipeline.
Public smoke: `make check RUN='^TestReferenceAnyValueSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceAnyValue'`.

`min(value)`/`max(value)` select a same-type scalar overload for INT2/INT4/INT8,
OID, FLOAT4/FLOAT8, NUMERIC, TEXT, BPCHAR, BYTEA, DATE, TIME, TIMETZ, TIMESTAMP,
TIMESTAMPTZ and INTERVAL. VARCHAR/NAME/internal CHAR implicitly select TEXT;
REGCLASS selects OID; domains lose logical identity in overload resolution.
Unknown string/NULL/parameter arguments infer TEXT. NULL candidates are ignored;
empty/all-NULL global input yields one typed NULL, empty keyed input no rows.
Results retain the chosen datum representation and resolved collation, not source
typmod or physical origin; native metadata is known nullable. Equal candidates
may have different representations: BPCHAR retains prior padding, whereas exact
NUMERIC scale, INTERVAL fields and floating signed zero follow the incoming
equal datum. Unordered SQL does not promise a universal stable tie order.
String comparison is demand-time: conflicting implicit collations permit zero
or one eligible value, then fail42P22 on the second, with the COLLATE hint and
no fabricated runtime Position. Explicit final COLLATE resolves the conflict.
Missing signatures (including BOOL/UUID and zero/star/multiple arguments) use
42883 with the ordinary function hint and character Position; child errors,
namespace visibility and nesting retain aggregate diagnostic precedence.
NAME[] and native private PG_NODE_TREE are deliberate positioned0A000 boundaries
(`min for name[] is unsupported`, for example), not PostgreSQL absent overloads.
No public cast, array ordering or tree textual ordering is added.
Public smoke: `make check RUN='^TestReferenceExtremaSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceExtrema'`.

`string_agg(TEXT,TEXT)` and `string_agg(BYTEA,BYTEA)` return the corresponding
nullable base type with unrestricted typmod and no physical origin. BYTEA retains
raw octets, not encoded text. NULL values are ignored; NULL delimiters add no
separator. The first surviving value ignores its delimiter; each later value uses
its own delimiter, including empty values. Empty/all-NULL global input returns
typed NULL; empty keyed input has no rows and complete metadata.
Native TEXT Length is `(MaxInt64,true)`; native BYTEA Length is `(0,false)`.
Both lib/pq overloads expose `(MaxInt64,true)`; DecimalSize is unavailable.
All existing aggregates admit in-call `ORDER BY` source expressions, multiple keys,
ASC/DESC and NULLS FIRST/LAST. Integer keys are constants, not output ordinals;
projection aliases are invisible. Different calls sort independently. Without
in-call ordering, concatenation order is unspecified; outer ORDER sorts groups.
Unknown arguments infer TEXT, or BYTEA when either known operand selects BYTEA.
Existing VARCHAR/NAME/BPCHAR/domain-base implicit coercions apply; mixed TEXT/BYTEA
and integer/Boolean signatures fail42883 with full signature/hint/character position.
Both arguments and all ordering keys evaluate even when the value is NULL.
Native late failure/cancellation publishes no cursor or partial INSERT batch.
Calls in INSERT VALUES give42803 at the call. INSERT RETURNING remains an
independent native0A000 suffix boundary rather than aggregate-clause parity.
Public smoke: `make check RUN='^TestReferenceStringAggSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceStringAgg'`.

`array_agg(anynonarray)` collects scalar values including NULL into the actual
associated array type. Scalar domains remain element domains. `array_agg(anyarray)`
concatenates matching arrays into one higher dimension using the same array type;
domains over arrays are reduced to their base array, and arrays of domains retain
their element domain. Result typmod is unrestricted and origin is zero. Both
overloads return SQL NULL for no admitted input. Scalar DISTINCT and integer
local ordering keys work; array DISTINCT/array-valued local keys and valid OVER
retain the existing positioned native0A000 boundaries.
Array input preserves inner bounds and element NULLs. Whole NULL arrays fail22004
`cannot accumulate null arrays`; first empty input fails2202E `cannot accumulate
empty arrays`. Later rank, extent or lower-bound mismatch (including empty after
nonempty) fails2202E `cannot accumulate arrays of different dimensionality`.
Rank6 input fails54000 because the result would exceed the six-dimension limit.
Unknown NULL/string/unresolved parameter input is ambiguous42725 with the cast
hint, not an implicit TEXT overload. All SQL errors retain PostgreSQL English
diagnostics; runtime shape errors have no fabricated source Position.
Existing public carriers without an associated array, such as PG_NODE_TREE
from pg_attrdef.adbin, fail42704 `could not find array type for data type
pg_node_tree`, with no Position or optional diagnostic fields. This does not
add a new array type/provider or substitute a native capability error.
Public smoke: `make check RUN='^TestReferenceArrayAggFeatureSmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceArrayAgg'`.


Grouping uses the ordinary no-FROM local input. It does not add
aggregates other than count/bool_and/bool_or/every/avg(interval or numeric inputs)/sum(numeric inputs)/any_value/string_agg/array_agg/min/max,
other window providers or frame exclusions. GROUPING SETS, ROLLUP, CUBE, explicit
empty groups and ALL/DISTINCT grouping clauses are admitted. Group-expression
constant identity preserves representation (including NUMERIC scale), separately
from runtime typed grouping equality.
Feature smoke: `make check RUN='^TestReferenceQueryAggregateSmokeMigration$'`;
real independent oracle: `make check REFERENCE=1 RUN='^TestReferenceQueryAggregate'`.
Expression-count smoke: `make check RUN='^TestReferenceCountExpressionSmokeMigration$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceCountExpression'`.
Change note: omitted whole grouping expressions become typed NULLs; real input
NULLs retain included-key GROUPING bit0. The operation returns nonnullable INT4,
rightmost argument low bit, at most31 arguments matched to exact expressions of
their owning query. Default/ALL retain set occurrences; GROUP BY DISTINCT
deduplicates sets, not output rows. Empty sets produce a group even without input.
ROLLUP/CUBE arguments are ordinary expression units, with implicit sublists
indivisible; empty and recursive grouping constructs instead belong to the
general grouping-item list used by GROUPING SETS. Nested unreserved function
names retain scalar-call ownership and the existing native capability boundary.
Typed supplied parameters match by identity, not equal runtime values; held NULL
keys retain included0 versus omitted1 bits and do not capture a previous value.
CUBE permits12 units; raw expansion permits4096 sets before DISTINCT. Outer Var
GROUPING in scalar subqueries follows PostgreSQL18 ownership; correlated computed
arguments retain42803. INSERT RETURNING, domain constraints and other adjacent
boundaries remain unchanged. Smoke/oracle:
`make check RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'`
and `make check REFERENCE=1 RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'`.
Boolean smoke: `make check RUN='^TestReference(BooleanAggregate[A-Za-z0-9_]*|AnyValueSourceACL)$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceBooleanAggregate'`.
Parameterless count() gives42809 and preserves a qualified call name in its message.
Children are analyzed before FILTER's contextual BOOL check, then the
more-than-100-argument guard54023, namespace/visibility and signature resolution.
An absent explicit schema gives3F000 only after child/FILTER analysis and the guard; bad arity (including 100 arguments)
gives42883 with actual argument types.

Aggregate FILTER, DISTINCT and default-equivalent ALL are admitted across every
existing provider. FILTER is evaluated first; FALSE/NULL skip arguments and keys.
DISTINCT uses typed full-argument tuples, including NULL delimiter components,
before strict NULL transition skipping; arguments and keys still evaluate on
duplicates and NULL values. Local numeric keys are constants, not ordinals.
DISTINCT ORDER keys must match complete arguments, ignoring only implicit argument
coercions, otherwise42P10. Bare DISTINCT COUNT parameters and a matched UNKNOWN
local ORDER target infer TEXT. Plain `count($1)` still does not infer an unresolved
parameter. FILTER parameters infer BOOL.
All modifier reads retain source ACLs/dependencies even on empty/FALSE input.
Views/native replay retain modifiers and rebind typed comparators/key families.
Native aggregate DISTINCT/order on arrays or PG_NODE_TREE gives positioned0A000
(`aggregate DISTINCT is unsupported for type ...` / `aggregate ordering is unsupported for type ...`);
PostgreSQL supports these represented carriers. This does not add their ordinary
ordering or any aggregate/window provider.
Feature smoke: `make check RUN='^TestReferenceAggregateModifiersFeatureSmoke$'`;
oracle: `make check REFERENCE=1 RUN='^TestReferenceAggregateModifiersFeatureSmoke$'`.
Only a resolved aggregate call rejects nesting with42803 at the inner call.
Unmodified COUNT ANY does not infer bare parameter types: cast unresolved parameters explicitly.

Simple/searched CASE, COALESCE, NULLIF, GREATEST and LEAST are scalar constructs
in every existing general-scalar consumer, including DEFAULT/native roundtrip.
Native CASE and COALESCE skip unneeded runtime children; native NULLIF and extrema
demand all arguments. Pinned PostgreSQL can instead plan immutable bound
conversions before CASE/COALESCE demand, and `NULLIF(NULL,10/v)` at v=0 returns
SQL NULL rather than native22012. The reference suite asserts these outcomes
independently, not as an optimizer guarantee. All children still undergo analysis
and authorization. A bounded
execution-only constant simplifier can reject constant division by zero beneath
a row-dependent CASE/COALESCE, while decisive constant branches prune their
unneeded runtime expressions. This is not general Boolean short-circuiting.
Common-type and equality-overload resolution execute across the already-supported
scalar types, including implicit DATE→TIMESTAMP; this does not add general casts,
ordinary routines, arrays/VARIADIC or ordinary relationless SELECT.
See [conditional expressions](QUERIES.md#conditional-expressions).
Feature witnesses: `make check RUN='^TestConditionalSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceConditional'`.

### Standalone and FROM VALUES — change note

VALUES is a query and a parenthesized, optionally aliased non-lateral FROM source.
It exposes computed `columnN` columns, independently common-types each column
(unknown-only becomes TEXT), preserves duplicate occurrences and typed NULLs,
and supports local ORDER BY and LIMIT/OFFSET/FETCH. Positional column aliases may
rename a prefix. Existing inner/cross joins, expressions, INSERT SELECT and
stored views compose through the shared binder and occurrence runtime.
DEFAULT, aggregates and surrounding FROM references are not admitted in cells.
Missing-column suggestions include VALUES labels/aliases without a catalog
identity. Inaccessible preceding operands retain table/column LATERAL diagnostic
hints, independently of whether those operands are physical; no correlation is added.
Synthetic columns have no physical ACL/origin/locking identity. Standalone
VALUES rejects FOR UPDATE; FROM VALUES accepts existing SELECT locking with no
synthetic locks. Mixed physical sources retain ordinary lock/recheck behavior.
Native rechecks reuse eagerly captured synthetic values/effects instead of
PostgreSQL EPQ volatile-expression replay; sequence outputs/counters can differ.
Existing native join/view predicate boundaries also retain operand demand where
PostgreSQL may avoid an already-rejected operand. Physical row replacement and
predicate requalification remain supported; this adds no expression replay.
Stored views deparse and round-trip through native controls with owned metadata
and results. LATERAL and additional locking syntax remain excluded.
See [VALUES contracts](QUERIES.md#standalone-and-from-values--change-note-changelog).
Independent pinned reference: `make check REFERENCE=1 RUN='^TestReferenceQueryFromValues'`.


INSERT additionally accepts SELECT sources with the existing projection/filter/
expression-ordering surface, parenthesized sources and ordinary no-FROM literal/
parameter/scalar projections. Empty `(SELECT)` is one zero-width occurrence;
`SELECT FROM source` retains table occurrence/filter semantics. Explicit targets match source
schema width; implicit targets map first N live columns. Omitted columns use
declared defaults or typed NULL and enforce NOT NULL. SERIAL uses ordinary
width-checked defaults, distinct from identity overrides. The minimum fixed
early-bound nextval leaf is also admitted in declared DEFAULT expressions.
Already-supported casts and null/truth/distinct predicates, DATE and BYTEA are
preserved in INSERT sources. Shared SELECT analysis retains current cast result
names. Recursive conversion preflight uses the imported execution frame, outside
the engine lock and before source capture; analyzed query nodes remain immutable.
Selected sequence calls also work in INSERT SELECT/VALUES producers through
that same binder and StatementScope; sequence FROM sources capture the same
three-value row as ordinary SELECT. Cast preflight visits sequence arguments
without evaluating the sequence call. Prepare consumes no sequence values.

Plain or parenthesized undecorated VALUES remains destination-context input.
VALUES with ORDER BY or LIMIT/OFFSET/FETCH uses column-wise common typing,
generated `columnN` names, eligible ordinals/directions/NULL placement, and
implicit coercion before destination assignment. Only unsigned integer tokens
within int32 are output positions (also after unary minus); larger tokens,
fractions/exponents, strings, BOOL and NULL are non-integer raw constants.
Thus `-2147483648` is not an ordinal despite its signed value fitting int32.
Unary-plus numbers such as `+2`, `+1.5` and `+2147483648` are validated constant
expressions, never ordinals; every source occurrence has the same key and no
peer order is promised.
Raw parsing retains these constants and consumes the entire script. Target
relation/column analysis and VALUES width/common typing precede ORDER diagnostics;
a later raw syntax error wins before any analysis or prefix effects.
Unknown-only columns become TEXT; known TEXT does not
gain integer input semantics. Decorated VALUES assignment mismatch has42804/
rewrite-or-cast Hint/no Position (generated output Var); SELECT output assignment,
VALUES common-type/width and ORDER key diagnostics keep their own occurrence
positions. Mixed numeric columns use existing directional implicit casts, not
text-export/reparse or a transitive cast graph.
Missing VALUES ORDER names retain42703 and the key's original Position. A
bounded, case-sensitive character-distance search of this source's generated
names supplies a `*VALUES*.columnN` Hint for one or two nearest candidates;
three-way ties or distant names have no Hint. This does not extend ordinary
relation name resolution or its diagnostics.

Query INSERT has command-only metadata, exact zero/one/many occurrence counts,
fresh prepared source binding and a frozen self-source including earlier own
transaction writes. Both relation identities are retained: target RX first,
source AS second, independent target INSERT/source SELECT column manifests
(including WHERE/hidden ORDER). Empty SELECT/INSERT column manifests require
table rights or the corresponding privilege on any live column, even for a
zero-row source; dropped-column grants do not qualify. Eager source evaluation is unlocked; one complete
converted/constraint-checked batch reaches atomic WriteStage publication, and
ordinary cancellation/epoch errors keep their existing adapter boundaries.
Target-list names and duplicates are checked before source lookup/acquisition;
a missing or blocked source cannot mask a target-list diagnostic. Required
cursor cleanup and a final cancellation check also veto an otherwise complete
batch before publication.
Dump serializes persisted typed rows as literal VALUES. Restore rejects nonnil
query INSERT sources0A000 before trusted execution; canonical Dump roundtrips
and retained result ownership remain supported. Public table/column GRANT and
plain REVOKE provide actual shared dual-mode source/target authority fixtures;
sequence and other-object grant capabilities remain independent exclusions.

Feature smoke: `make check RUN='^TestInsertSelectSmoke$'`.
Combined real-oracle feature reference (not a PostgreSQL-only empty regex):
`make check REFERENCE=1 RUN='^TestReferenceInsertSelect'`.
INSERT SELECT preserves raw unknown string/NULL and unresolved parameter output
occurrences until destination assignment; ordinary standalone SELECT still owns
its existing TEXT fallback. An output occurrence unresolved before a later WHERE
inference cannot be globally retagged: an incompatible destination reports42P08
at that occurrence. Already-known INT4 outputs may assignment-cast to BIGINT
without changing the input parameter type; per-target NUMERIC/time/timestamp
typmods size each value independently.

DELETE FROM [schema.]target [[AS] alias] [USING from_list] [WHERE predicate] removes real live row
occurrences and retains the table/schema. WHERE reuses typed scalar comparisons,
Boolean AND/OR/NOT and True-only selection, including contextual BOOL parameters.
DELETE predicate columns accept visible target/alias qualification; alias hides
the original target name. USING reuses the supported SELECT source tree. Direct
and held prepared executions report exact affected counts, zero for no matches,
and unsupported LastInsertId. Omitted RETURNING has no result relation.
Qualified missing-column diagnostics use decoded identifier components without
SQL quoting, including `column D.Zzzzz does not exist` for `"D"."Zzzzz"`.
DELETE admits the same nullness, truth and DISTINCT predicate forms as SELECT,
including qualified target/alias operands. Without USING, baseline scalar CAST/`::`/function
casts admit unqualified children only; qualified cast children remain excluded
with whole-input rejection before effects. USING uses query qualification. Frame-only conversions precede
executor ACL checks and candidate capture even on empty input or decisive Boolean
branches; row-dependent conversions still evaluate per row, without guaranteed
logical short-circuiting. With stored `s='oops'`, native `FALSE AND int4(s)=42`
and `TRUE OR int4(s)=42` both fail22P02 and preserve all original rows; empty
sources succeed with count0 because no row conversion is demanded. Pinned
PostgreSQL optimizes these specific decisive predicates (FALSE AND count0 with
originals, TRUE OR countpopulation with no survivors), also when the unused cast
takes an invalid TEXT parameter. Native frame-only preflight still fails22P02
on empty or populated input. This is a bounded optimization difference, not a
general PostgreSQL short-circuit or operand-order guarantee.

Execution requires table DELETE plus SELECT on actually read predicate columns,
never a fabricated SELECT requirement for omitted/constant WHERE. Prepare binds
metadata and acquires RX but no row hold. Data analysis fixes transaction
characteristic timing and the REPEATABLE READ snapshot before relation waits.
READ COMMITTED/UNCOMMITTED capture execution candidates after relation grant:
peer commit skips an ended predecessor, rollback permits deletion, and newly
matching inserts during a wait remain outside the fixed set. REPEATABLE READ
rejects newer committed heads/ends with 40001 before liveness/predicate recheck.
Distinct rows and ordinary SELECT remain independent.
Successful own earlier insert/delete commands overlay subsequent commands. One
atomic frontier publishes all insertions/deletions before holds are released;
failure/cancellation discards everything admitted, including earlier explicit
private effects, while SQL remains 25P02 until recovery.
DELETE lifetime/publication authority uses the exact existing storage-shape
generation plus object/epoch and row/version/ordinal, not descriptor pointer
equality. Compatible TABLE/COLUMN COMMENT and ACL revisions preserve staged work
and fixed candidates through waits; replacement/removal/recreation or incompatible
shape changes still fail. COMMIT does not reauthorize already staged DELETE;
each fresh prepared Execute still checks current table/read-column privileges.
INSERT SELECT sources share live membership, including earlier own deletions;
self-insert captures survivors before its new batch and empty live sources insert zero.

Immutable end roots preserve retained membership/schema, duplicates and zero-width
occurrences. Dump serializes committed survivors only, never private deletes or
DELETE/tombstone history. Restore's whitelist stays unchanged. Self/foreign catalog
replacement and DROP-column shape changes preserve live membership; foreign
imports assign fresh destination identities and omit source history. NOT NULL
scans ignore deleted NULLs but reject live NULLs and enforce later INSERT. Removing
a row cannot violate supported NOT NULL; genuine FK rejection is an explicitly
PostgreSQL-only witness. Native FKs, triggers, DELETE CURRENT OF,
CTE and recursive inheritance targets remain excluded; leaf child DELETE works.
Smoke: `make check RUN='^TestReferenceDeleteUsingSmoke'`;
reference: `make check REFERENCE=1 RUN='^TestReferenceDeleteUsing'`.
Migrated baseline expressions: `make check RUN='^TestReferenceDeleteUsingMigratedExpressions'`.

USING admits currently supported relations/self-relations, comma/CROSS/inner/outer
joins, ON/USING/NATURAL, aliases/parentheses, derived SELECT and VALUES, views,
and catalog sources; existing FULL-path execution restrictions apply.
Immediate sequence USING sources pass effect-free Prepare but fail Execute with
42809: DELETE's reference-kind validation does not grant source tuple locks or
UPDATE privileges.
Source-local ON/derived analysis cannot read the mutation target. WHERE/RETURNING
see the combined namespace. Nonempty products delete each target once; empty
sources delete none, including zero-width cardinality. Deduplication is by physical
target identity, never values. Duplicate source winners/order are unspecified.
All source read manifests require SELECT, even for FALSE/empty/constant queries;
target SELECT is required only for actual reads, independently of target DELETE.
Sources and qualifying pair identities are frozen before the first row wait.
Rechecks use the latest same target and original source occurrence; a rejected
pair does not suppress later eligible pairs. Publication, abort/savepoint,
cancellation and committed-survivor Dump/Restore use the existing DELETE authority.

DELETE RETURNING supports mixed target/source/OLD/NEW stars and all already-admitted
general scalar projections, labels, parameters and sequences, with
`WITH (OLD AS o, NEW AS n)` output aliases. Actual successful deletion
predecessors provide OLD/default values; ordinary NEW is source-typed NULL
with nullable metadata and the same source-column SELECT requirement.
Explicit aliases/visible target or source names mask implicit OLD/NEW; output namespaces
do not leak into WHERE. Prepare is effect-free; held execution rebinds, checks
current ACLs and validates its output signature. Finalized rows are owned,
eager and never reevaluated by Next, including when Exec ignores output.
No-match output retains full metadata. Output errors or prepublication
cancellation discard transactional deletes atomically, not sequence draws.
Postpublication consumer cancellation does not undo deletion.
Dump/Restore persists committed survivors/objects and sequence state, not
RETURNING cursors/history; its mutation-script whitelist is not widened.
Whole-row records, SRFs, triggers/FKs,
rewrite-to-UPDATE and physical tuple/C/wire metadata remain
PostgreSQL-only boundaries. See [output details](QUERIES.md#delete-returning--change-note).
Feature smoke: `make check RUN='^TestReferenceDeleteUsingMigratedReturning'`;
reference: `make check REFERENCE=1 RUN='^TestReferenceDeleteReturning'`.

UPDATE RETURNING shares scalar projection/alias binding and eager result ownership
with DELETE RETURNING. OLD is the actual predecessor used by simultaneous SET;
NEW, unqualified and target-qualified outputs read the constrained replacement.
Both real versions preserve declared source metadata and SELECT dependencies.
Output aliases are visible only in RETURNING. Target aliases and ordinary
qualifications in SET/WHERE use the shared UPDATE namespace. Same-value matches
return/count once; duplicates survive and no matches retain metadata. WHERE analysis precedes RETURNING output
fallback, which precedes SET; prepared execution freshly checks ACLs and result
signature. Output/constraint/cancellation failure publishes no partial replacement
or output, following existing abort/savepoint rules; sequence draws are not undone.
Dump/Restore persists committed live data/catalog/sequence state, not output/history;
its script whitelist is unchanged. Triggers, whole-row records/record expansion,
rewrite rules/partition movement and physical/C/wire
tuple metadata are genuine PostgreSQL-only mechanisms, not native no-ops.
See [UPDATE output details](QUERIES.md#update-returning--change-note).
Feature smoke: `make check RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)'`; pinned mode: `make check REFERENCE=1 RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)'`.



Column DEFAULT declarations admit outer scalar/typed temporal/existing cast/
INT4/INT8 and floating unary/binary arithmetic (including FLOAT8-selected power), integral bitwise operations, comparison
and IS [NOT] DISTINCT FROM syntax.
CAST and postfix casts reuse existing baseline, character-family, bounded OID/
temporal conversions, same-type stored scalar usages and directional numeric
edges. Function-style casts retain their bounded whitelist, including oid.
Logical NOT/AND/OR and null/truth tests require parentheses
(e.g. DEFAULT (NULL IS NULL)), as PostgreSQL's restricted default grammar requires.
Columns (0A000) and parameters (42P02) remain prohibited inside casts and decisive
branches. Unknown input binds at CREATE Execute; destination sizing/conversion
is execution-demanded. CREATE utility Prepare remains analysis-free.
Each column's type resolves before its ordered NULL/NOT NULL/default declaration
validation, before later elements. A second DEFAULT is42601 at that token;
earlier type/nullability errors retain precedence. Complete declaration structure
and NOT NULL merging precede cooking any default expression. Restore shares this
binder, with no replacement publication on failure.
Omitted INSERT columns and per-cell DEFAULT use declared expressions or typed
NULL; explicit NULL bypasses defaults.
Reordered explicit targets map supplied parameters and DEFAULT to their named
destinations, not declaration-order input slots. DEFAULT markers consume no
parameter index; a supplied parameter NULL bypasses the declared default.
See [TABLES](TABLES.md) for declared metadata and native-control boundaries.
SELECT and ORDER-decorated VALUES sources use omitted destination defaults.
One execution-only default-demand phase
handles both ordinary and query INSERT after supplied-value/source preflight.
Only defaults consumed by some ordinary row or omitted by the query are planned;
a column explicitly supplied in every row, including NULL, bypasses this phase.
Immutable conversions and closed integer arithmetic are demanded even for an empty
query source. This bounded default-only recursive
planning handles the left first. Concrete non-NULL BOOL TRUE on OR or FALSE on
AND replaces the whole execution node with a private BOOL literal, without
visiting the discarded right branch. Existing typed operations fold closed constant
arithmetic/comparison/predicate/NOT children only after their required children.
NULL/nondecisive left operands still demand right operations; a bad left is not
rescued by a decisive right. CREATE continues analyzing both operands.
The schedule uses lazy private copies; catalog definitions remain unfolded.
Remaining runtime expressions evaluate only in rows/cells consuming the default.
Outside conditional boundaries, ordinary query/DELETE/UPDATE/supplied preflight
and runtime logical evaluator behavior are unchanged; no general Boolean
short-circuit/optimizer promise is made.
ORDER-decorated DEFAULT is42601 `DEFAULT is not allowed in this context` at its
marker; direct Execute/prepared Prepare. Ordinary and parenthesized undecorated
VALUES retain requests. Count-decorated SELECT/VALUES use the shared source
grammar; FROM-less WHERE remains excluded, without additional default features.
Public targetless DEFAULT VALUES requests one
all-default row, including on zero-column tables; a target list before DEFAULT
VALUES fails42601 at DEFAULT. Supplied INSERT/settings scalars are not widened.
Recursive analysis reports earlier child Boolean/input/operator errors before
later prohibited occurrences; valid decisive branches still analyze both children.
Defaults are immutable attribute-keyed descriptor definitions, not cached
evaluated values or nullable metadata. Plain LIKE excludes defaults; owner,
comment and NOT NULL changes preserve them, and DROP COLUMN removes its key.
Rounded finite endpoint literals keep the type-owned TIMESTAMP(0) encoding with
exactly one introducer, including within default comparison expressions.
Existing null/truth/distinct default nodes serialize recursively with parentheses,
without Eval, and retain shared typed predicate semantics through Dump/Restore.
Integer arithmetic defaults validate their private operator identity against the
existing concrete-width provider and serialize grouped operands without Eval.
Operand widths remain intact on replay; destination BIGINT does not widen INT4
operations. Ordinary arithmetic stays row-owned outside consumed-default planning.
Existing explicit character CAST typmods preserve Unicode truncation and BPCHAR
trimming through controls; destination assignment retains overlength errors.
Explicit default coercions validate their actual construction mode and retain
canonical CAST structure. Known TEXT cast children remain typed, preserving
deferred conversion timing through Restore. DATE leaves retain typed introducers
recursively, so civil BC ordering cannot silently become TEXT ordering on replay.
Top-level BYTEA defaults use the existing quoted hex codec in destination context;
public results remain raw bytes. No typed BYTEA compound or DATE/BYTEA cast is added.
Dump serializes expression structure/captured temporal input without Eval and
existing rows explicitly. Restore uses the same binder and public DEFAULT
requests, staging atomically. Temporal 'now' input captures CREATE's operation
clock; no new volatile producer, general function, subquery, cast family or arithmetic
operator/type is implied. UPDATE DEFAULT remains excluded. Identity columns use
an internally owned sequence, not a declared DEFAULT. SERIAL expands to integer
NOT NULL columns, width-matched AUTO-owned sequences and ordinary fixed nextval
defaults. Only early-bound nextval leaves and assignment/cast wrappers are admitted;
other sequence/volatile default composition remains excluded. Disk/WAL,
network protocol, native ABI extensions and external pg_dump/custom archives
are specifically non-engine mechanisms, never permission for successful stubs.
Feature smoke: `make check RUN='^TestDefaultSmoke$'`.
Mixed-parameter/reordered-target INSERT coverage:
`make check RUN='^TestInsertDefaultSmoke$'`.

### Identity columns — change note

SMALLINT/INTEGER/BIGINT columns admit `[CONSTRAINT name] GENERATED ALWAYS` or
`GENERATED BY DEFAULT AS IDENTITY [(sequence options)]`. Identity implies NOT NULL,
but no uniqueness. CREATE publishes the table and every internal sequence atomically.
Options include START, INCREMENT, MINVALUE/MAXVALUE, CACHE, CYCLE and explicit
SEQUENCE NAME; supplied OWNED BY cannot replace the internal link. Declaration
LOGGED/UNLOGGED selects sequence persistence independently of the ordinary logged
table. Duplicate persistence options conflict. ALTER identity SET LOGGED/UNLOGGED
retains PostgreSQL's XX000 unrecognized-option error; direct ALTER SEQUENCE
persistence and its native control reconstruction remain admitted.

INSERT omission and DEFAULT generate once per consumed row. BY DEFAULT accepts
explicit values; ALWAYS rejects them at analysis with428C9 unless SYSTEM or USER
override is present. `OVERRIDING SYSTEM VALUE` preserves supplied values;
`OVERRIDING USER VALUE` discards only identity inputs after normal type analysis.
Single ordinary VALUES binds discarded inputs and imports inferred parameters,
but does not execute their runtime conversion or volatile producer. Its remaining
supplied/default/identity expressions execute in destination attribute order, even
with reordered INSERT targets. Multirow ordinary VALUES demands every reached
source row, including discarded identity expressions, before that row's identity
projection. SELECT and singleton decorated VALUES prune unused nonvolatile
identity outputs after full analysis/inference/import; volatile outputs and
independent ORDER consumers remain demanded. Decorated multirow VALUES retains
all source-cell demand, including with LIMIT or ordering another column.
Complete destination rows are checked for NOT NULL and CHECK before the next
runtime identity row is demanded. Supplied statement-wide conversion preflight
still precedes executor constraints; only one fully constrained batch publishes.
DEFAULT VALUES does not admit an override clause.
Identity generation updates physical-session currval/lastval using sequence cache
and allocation authority; explicit bypass and empty sources consume no values.
An early constraint failure retains already-demanded sequence effects, never
demands later runtime rows, and cannot be masked by their sequence exhaustion.
Failure-demand smoke: `make check RUN='^TestIdentityFailureDemandSmoke$'`.

ALTER COLUMN ADD/DROP IDENTITY, SET GENERATED, SET sequence options and RESTART
compose with admitted nullability/drop-column actions and support transactions/
savepoints. They affect future inserts, never existing data. DROP retains NOT NULL.
Direct internal-sequence DROP fails2BP01 even CASCADE; OWNED BY reassociation fails
0A000. Removing identity NOT NULL or assigning a regular DEFAULT fails42601;
ALWAYS explicit UPDATE fails428C9, even for an empty target.
Repeated SET GENERATED is rejected during utility execution, not parsing;
numeric sequence validation takes precedence. ADD sequence validation precedes
table identity/nullability/default guards, while all proposed changes stay
private on any failure. See [identity ALTER diagnostics and shared regression
coverage](TABLES.md#identity-columns--change-note). SQL GRANT is not added by
this identity ALTER surface; owner-only ALTER uses existing role authority.

TYPE on an original identity column accepts only SMALLINT/INTEGER/BIGINT.
After binding every raw USING against the original tuple, it migrates the exact
linked sequence through AS before the table action passes, even for same-type
TYPE. Nonintegral targets fail22023 before row evaluation; DROP IDENTITY in the
same command cannot bypass that guard. Default type-extreme bounds migrate,
custom options remain subject to the existing sequence bounds checks, and plain
OWNED BY sequences do not migrate. USING and CHECK sequence calls demand the
private AS replacement. On failure/rollback its scalar state is discarded with
the candidate; physical currval/lastval and the caller's AS cache-tail discard
are not undone. Unseen peer cache tails remain untouched until committed AS is
demanded. Identity mode, linked address, privileges and comments remain intact.
Feature smokes: `make check RUN='^TestAlterTypeIdentity(Smoke|ControlsSmoke)$'`.

Native controls preserve definitions, actual sequence names/configuration/state and
literal identity rows without allocating values. LIKE INCLUDING IDENTITY copies
mode/configuration to an independent initially uncalled owned sequence. Ordinary/
identity UPDATE DEFAULT, arbitrary table DDL in transactions and full identity
catalog views remain excluded. Logical controls do not imply physical/WAL/wire support.


ALTER TABLE supports one `ALTER [COLUMN] name SET DEFAULT expression` or
`ALTER [COLUMN] name DROP DEFAULT` action. It uses the existing general expression
grammar (unparenthesized NOT/AND/OR and null/truth tests), unlike CREATE's restricted
DEFAULT grammar. Owner-only AX execution changes an immutable definition without
evaluating it or rewriting rows; invalid binding leaves the old default intact.
Subsequent omitted/DEFAULT inserts, including held prepared statements, see the new
definition; DROP twice succeeds and restores implicit NULL without altering NOT NULL.
ALTER semantic default diagnostics have empty Position; syntax and CREATE positions
are unchanged. Qualified/quoted targets, ONLY/ONLY(...) and trailing `*` are accepted
for standalone tables. CREATE inheritance is supported, but graph default ALTER
rejects the explicit native propagation boundary described in docs/TABLES.md.
Action lists and IF EXISTS for these default actions,
UPDATE DEFAULT, unimplemented expression families and explicit-transaction SET/DROP DEFAULT
remain excluded. Selected ADD COLUMN/TYPE/nullability/DROP COLUMN lists support
explicit transactions. Dump serializes current CREATE definitions and explicit rows; Restore's
existing whitelist does not admit ALTER TABLE history.
Feature smoke: `make check RUN='^TestAlterDefaultSmoke$'`.

Change note: ADD [COLUMN] [IF NOT EXISTS] admits existing scalar types/typmods/
collations and column NULL/NOT NULL, DEFAULT and CHECK declarations. Owner-only
AX execution publishes schema and widened rows together. A no-default addition
exposes typed NULL in old rows. Supported Immutable/Stable defaults are evaluated
once during ADD, including on empty tables; the admitted fixed-nextval volatile
default is evaluated for each effective live row. Definitions remain immutable
and future omission/DEFAULT uses the original expression. Failed default/NN/CHECK
validation changes no schema/data, but demanded sequence allocations persist.
IF NOT EXISTS skips an ordinary collision before type/default/CHECK binding and
emits42701 NOTICE; system-column names still fail42701. Complete syntax is required.
DROP and TYPE passes precede ADD; generated CHECKs can see later-added columns.
Mixed SET/DROP DEFAULT lists remain unsupported. ADD serial/identity/generated/
unique/FK declarations and inheritance/partition propagation are not admitted.
Stable attributes advance a retained per-relation ceiling across DROP/ADD;
pg_class.relnatts reflects that ceiling. This does not introduce pg_attribute,
physical tombstones, PostgreSQL missing-value tuple storage, wire/disk durability
or a pg_dump process. Native logical Restore may remint live attributes.
Feature smoke: `make check REFERENCE=1 RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$'`.

Scalar comparisons admit `=`, `<>`, `!=`, `<`, `<=`, `>` and `>=`; `!=` is the
`<>` alias. Both operands may be existing scalar literals, parameters, columns
or admitted character casts.
Every result is BOOL (`?column?`, native bool ScanType, no physical-column origin),
including empty results; either NULL yields typed BOOL NULL/UNKNOWN, which WHERE
does not keep. INT2/INT4/INT8 compare exactly across widths without narrowing or
floating-point conversion. Existing NAME/TEXT, BOOL, FLOAT4/8, exact NUMERIC,
UUID, BYTEA and same-type DATE/TIME/TIMESTAMP providers keep their existing admission and
ordering (C text order, FALSE before TRUE); no cross-temporal casts are added.
Unknown quoted strings/NULL take a known peer's type; two unknowns choose TEXT.
Known TEXT is not implicitly integer/BOOL. Parameter occurrences copy their
then-known shared type before comparison resolution: `i=$1,j=$1` can reuse INT4
against BIGINT, whereas a later BOOL comparison with that known INT4 fails42883.
An earlier bare `$1` still has TEXT output fallback and conflicts with later
INT4 inference (42P08). Inputs' Go kinds never choose SQL types.
Incompatible operands fail eagerly in Query/Prepare at the operator (42883);
selected literal input errors retain the literal position (22P02/22003).
No comparison implies row ordering. SELECT projection labels and expression
ORDER keys are supported; comparison chaining, casts outside the bounded
baseline and character-family sets, and comparison in INSERT/settings remain
excluded. Feature smoke: `make check RUN='^TestCompareOpsSmoke$'`;
independent pinned reference: `make check REFERENCE=1 RUN='^TestReferenceCompareOps'`.

BETWEEN predicates support `BETWEEN`, `NOT BETWEEN`, `BETWEEN SYMMETRIC`
and `NOT BETWEEN SYMMETRIC` through the existing comparison/logical binder.
Endpoints are inclusive; reversed ordinary bounds match no non-NULL values,
while symmetric forms accept either order. NOT forms use the complementary
inequalities. NULL follows binary comparison and SQL AND/OR truth tables:
`5 BETWEEN NULL AND 3` is false, its NOT form is true, and its symmetric forms
are UNKNOWN. Each binary comparison resolves its own types; there is no
three-argument common type or bounds sorter. Generated operator diagnostics
point to BETWEEN (or NOT), while input errors retain their original positions.
Group Boolean/nested endpoints, for example
`TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE`. These predicates compose in
existing SELECT/WHERE, DELETE, UPDATE, CASE, INSERT SELECT, grouping/HAVING
and parenthesized DEFAULT paths; they do not expand ordinary INSERT/settings,
no-FROM SELECT, comparison chaining, casts/types or provider admission.
Prepared temporal occurrences at a shared source position retain independently
typed frozen values (including precision); mixed DATE/TIMESTAMP endpoints do
not require a cross-temporal operator. Native Dump/Restore preserves grouped
and nested BETWEEN default definitions without evaluating them, so DEFAULT
inserts continue to use those definitions after replay.
ASYMMETRIC remains excluded. Feature smoke:
`make check RUN='^TestBetweenSmoke$'`; independent pinned reference:
`make check REFERENCE=1 RUN='^TestReferenceBetween'`.


UPDATE supports schema-qualified/quoted target relations, AS/bare target aliases,
ordered scalar SET assignments, supported inner/outer FROM trees and optional Boolean WHERE.
SET destinations remain bare physical target columns; qualification applies to
RHS/WHERE and RETURNING. FROM reuses table/view aliases, ON/USING/NATURAL and joins.
Row assignment, CURRENT OF and DEFAULT remain excluded. Scalar subquery assignments and existing scalar
casts/functions and RETURNING are admitted through the same expression binder.
Arithmetic retains its operand/type boundaries, NULL propagation and division/overflow errors, with no new provider.
Assignments are simultaneous against one old row, preserve unassigned fields,
apply destination type/typmod and then enforce whole-row NOT NULL. Counts include
unchanged matches and duplicate occurrences once each; no match is zero.
Analysis errors are eager even on empty/no-match input; unknown literal input
belongs to analysis, immutable destination casts fold during Execute (including
empty/WHERE FALSE), and row-dependent conversion runs only for qualifying rows.
Duplicate assignments are42601 without Position; assignment42804 retains the
rewrite/cast Hint and RHS expression-start Position in direct execution and
semantic Prepare. Grouping is transparent, postfix casts retain the operand's
start, and positions count UTF8 characters, not bytes. This does not change
operator-resolution errors' operator-token positions.
Runtime NOT NULL includes permission-filtered updated-row Detail
and schema/table/column but no Constraint/Position. Table SELECT, default owner
authority and superuser retain the complete unlabelled row. Otherwise Detail
lists modified targets plus SELECT-readable retained columns in physical order
as `(names) = (values)`, even when column grants cover every column. Unreadable
retained values are not formatted or disclosed; carrying them does not require
SELECT. Captured output settings and UTF8-safe field truncation remain unchanged.
REAL/FLOAT4 and DOUBLE PRECISION/FLOAT8 have direct assignment-only conversions
to each integer width: round to nearest with ties to even, then enforce signed
bounds (the positive 2^63 boundary is excluded, negative -2^63 is valid).
NaN/infinities/out-of-range rounded values report destination-specific 22003
without Position. NULL and provider ownership/context checks remain shared;
NUMERIC conversions retain their existing ties-away rounding.
The existing TIME/TIMESTAMP precision, UUID input/NULL, TEXT assignment and
NUMERIC ordinary/negative/wide-scale destination providers are reused for old
columns and fresh direct/prepared inputs. Late typmod failure discards the
complete statement. Typed parameter input failures carry the actual unnamed
portal parameter Context without a query Position; row SQL deadlines have no native tuple Context,
while independently asserted PostgreSQL tuple Context is structurally matched.
UPDATE contextual parameter-type conflicts use occurrence-positioned 42P08 and
old-versus-requested type Detail; other INSERT/settings destination-reuse 0A000
boundaries remain unchanged. Native TIME exports canonical clock strings,
while pq TIME uses civil time.Time; reference expectations preserve this distinction.

Committed storage retains one immutable physical head per stable logical RowID,
including ended heads; DELETE does not compact that vector. New VersionIDs
replace predecessors without changing physical cardinality. One visible/raw-index
mapping substitutes successful private heads before deletion filtering, including
earlier private INSERTs. SELECT and INSERT SELECT consume each live occurrence
once, preserving duplicates and excluding own UPDATE-then-DELETE or
INSERT-UPDATE-DELETE heads. Generic self/foreign catalog replacement selects live
heads through that same mapping once, detaches/remints them and omits ended history.
READ COMMITTED fixes candidates before waits; physical ordinals are hints, stable
RowIDs identify latest predecessors. Peer deletion makes waiting UPDATE count0;
changed peer UPDATE values require predicate recheck for UPDATE/DELETE without
widening the captured set. DELETE ends the actual latest version. Relation RX
and distinct NO KEY UPDATE/row UPDATE modes use the shared four-mode matrix on
one logical row resource; ordinary readers/different rows do not conflict. Prepare takes RX, no
row locks or data scan. Head vectors and immutable end roots share one all-relation
storage/length/generation/end preflight and frontier install. Cancellation/failure
publishes none and discards both kinds of prior private work, releases holds and
leaves25P02 until end. Compatible COMMENT/ACL revisions preserve genuine rowshape
and storage identity, not descriptor-pointer equality; current authorization still
applies. Dump/ALTER see live committed heads and retained results keep old values.
UPDATE privilege on assigned targets and SELECT on actual RHS/WHERE reads are
checked independently; retained protected columns do not imply SELECT.
Smoke: `make check RUN='^TestReferenceUpdateCoreMigratedSmoke'`;
quick: `make check RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$'`;
reference: `make check REFERENCE=1 RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$'`.
UPDATE FROM includes target and every expanded source in one coherent statement
view. Stable RowIDs, not values or target/source pair ordinals, suppress successful
duplicate replacements; eligible losing pairs still evaluate SET. RETURNING uses
the same winning source row with actual OLD and constrained NEW. Source-only reads
do not require target SELECT; target/source column dependencies are authorized
under their existing principals. Prepared execution refreshes every source and
result signature. Native eager preflight/atomic-output boundaries are preserved.
Smoke: `make check RUN='^TestReferenceUpdateFromSmoke$'`;
reference: `make check REFERENCE=1 RUN='^TestReferenceUpdateFrom'`.
Coexistence witnesses: `make check RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$'`;
`make check REFERENCE=1 RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$'`.
The feature smoke includes private UPDATE/self INSERT SELECT/DELETE count2,
no-resurrection count0, peer visibility, rollback, commit and live control roundtrip.

Logical operators use PostgreSQL's three-valued BOOL semantics: FALSE AND NULL
is FALSE, TRUE OR NULL is TRUE, other undecided NULL combinations remain NULL,
and NOT NULL is typed BOOL NULL. Comparisons bind above NOT, then AND, then OR;
NOT associates right and AND/OR left, with parentheses overriding precedence.
There is no guaranteed left-before-right evaluation or short-circuit contract.
Every operand is analyzed, including decisive branches for types, dependencies
and actual-column authorization. Unknown strings/NULL/parameters contextualize
to BOOL, but concrete non-BOOL values fail construct-specific42804; bad Boolean
input is22P02. SQL diagnostics retain character positions. Logical outputs have
?column?/OID16/typmod-1/no collation or physical origin, native bool ScanType and
unknown nullability even on empty output; NULL exports as nil. Prepared frames
are fresh, while output metadata and captured logical rows survive EOF/Close
and native controls. `SELECT $1 AND TRUE,$1 FROM t` infers BOOL for both;
`SELECT $1,$1 AND TRUE FROM t` fails42P08/boolean versus text at the first target.
Existing scalar comparisons form Boolean leaves in targets, WHERE and ORDER
expressions. Logical INSERT, settings arguments and adjacent unsupported
expression forms remain excluded. Feature smoke:
`make check RUN='^TestLogicalSmoke$'`; pinned independent reference:
`make check REFERENCE=1 RUN='^TestReferenceLogical.*$'`.
PostgreSQL parses `SELECT TRUE AND FROM t` / `SELECT TRUE OR FROM t` as TRUE
targets with bare aliases and/or. Native bare-label parsing of these
keywords continue logical expressions; native42601 points to FROM at17/16.
Independent reference coverage asserts PostgreSQL's positive BOOL outputs and
native failures separately, without normalizing this capability boundary.

Selected `num_nulls`/`num_nonnulls` calls additionally work in SELECT projections
and nested selected arguments, accepting1–100 heterogeneous typed datums without
common-type conversion. They are immutable, nonstrict and return nonnull INT4
(native int64) counts with function-name labels and no physical origin.
Explicit NULL positions count; zero, false and empty strings are nonnull.
Their decoded counts sum to the ordinary argument count.
Ordinary array arguments count the whole array as one argument: whole SQL NULL
counts as null, while empty arrays and arrays with NULL cells count as nonnull.
This is existing whole-value nullness composition, not array member expansion.

No-FROM projections use the ordinary local one-row input and the existing
binder/project/finalization pipeline, without a selected-call witness or
an implicit relation namespace. Function argument restrictions remain unchanged.
One demanded local occurrence returns one row and Exec RowsAffected1; table calls retain input multiplicity,
actual-used-column authorization and empty-result metadata.
Direct and prepared calls share this path. ANY does not infer a parameter type:
standalone `num_nulls($1)`/`num_nonnulls($1)` fail42P18 regardless of supplied Go
values. Later target TEXT fallback or WHERE inference does not rewrite an earlier
unknown ANY occurrence; that mismatch fails42P08 at the occurrence. Existing
bare-target42P08 and valid TEXT reuse remain unchanged.

Zero arguments fail42883 with the standard explicit-casts Hint; over100 fail54023.
Children are analyzed in order before the outer arity check; the first missing
column or nested-call error therefore precedes54023. An unresolved ANY parameter
is not a child analysis error, so over100 still fails54023 before whole-statement
parameter finalization.
Explicit VARIADIC scalar operands fail42804 at the operand, including unknown
strings/NULL; multiple ordinary/VARIADIC operands fail42883 lookup first.
Explicit VARIADIC arrays remain a native capability boundary:0A000
`VARIADIC arrays in null-count arguments are unsupported` at the operand.
PostgreSQL expands their members; native does not. Scalar VARIADIC retains the
existing42804 diagnostic. Ordinary NULL is a single null argument. Calls remain excluded
from INSERT, WHERE and setting arguments. General calls, function qualification,
casts outside the bounded baseline and character-family sets, arithmetic,
DISTINCT, in-call ORDER BY, FILTER, OVER, star/named arguments
remain exclusions rather than PostgreSQL diagnostic-parity claims.
Computed arithmetic in table-backed selected null-count arguments fails native0A000
`arithmetic in null-count arguments is unsupported` at the operator, including
nested calls. The same binder diagnostic applies without FROM; the local
unit does not bypass selected argument restrictions. Former signed numeric
literal inputs remain admitted; arithmetic does not broaden this call surface.
Character casts remain ordinary call operands.
Feature smoke: `make check RUN='^TestReferenceNullCount'`;
independent reference: `make check REFERENCE=1 RUN='^TestReferenceNullCount'`.

### Random functions — change note

Selected projection/nested-argument calls now include PostgreSQL18 `random`,
`random_normal` and `setseed`, through the existing direct/prepared expression
pipeline and ordinary no-FROM local input:

| Call | Result | Behavior |
|---|---|---|
| `random()` | FLOAT8 | Uniform `0 <= value < 1` |
| `random(INT4,INT4)` | INT4 | Inclusive bounds, including the full signed domain |
| `random(INT8,INT8)` | INT8 | Inclusive bounds, including the full signed domain |
| `random(NUMERIC,NUMERIC)` | NUMERIC | Exact inclusive decimal lattice at maximum input display scale |
| `random_normal([mean[,stddev]])` | FLOAT8 | Defaults mean0/stddev1; ordinary IEEE results, including negative deviation and specials |
| `setseed(seed)` | VOID | Finite inclusive [-1,1]; nonnull empty string result |

The decimal lattice can exceed2^64 points and retains numeric's full supported
display-scale/weight range; large coefficients do not pass through floating point.
Equal bounds return the endpoint without consuming a draw. Numeric results are
unconstrained, not resized to a source column typmod. Mixed INTEGER/BIGINT
chooses BIGINT; integer/NUMERIC chooses NUMERIC. Bounded FLOAT4/FLOAT8 calls are
not released overloads. Two unknown bounds and SMALLINT/SMALLINT are ambiguous
(42725 with the explicit-casts Hint); inferred parameter types do not depend on
supplied Go values. Missing signatures use42883 and the standard function Hint
at the function token's character Position.
The general PostgreSQL function-call limit is100 arguments: larger admitted calls
fail54023 `cannot pass more than 100 arguments to a function`, with function-token
Position and no Hint. Child-analysis errors precede that limit; calls within the
limit still resolve against each selected function's smaller released arities.

All selected random calls are volatile and strict. Preparing/describing them
does not invoke them. Literal NULL can eliminate a call and its volatile sibling
arguments after type analysis; runtime NULL still evaluates all sibling arguments
before suppressing the provider. Each evaluated row receives its own draw.
Arguments may use existing integer arithmetic/conditional/group expressions
through the ordinary query pipeline. This does not add general functions/casts
or random-call support in WHERE, DEFAULT, INSERT VALUES or settings.

One lazy physical-session generator is shared by all overloads and normal calls.
Repeated valid seed repeats that session's subsequent mixed series. Pool return,
statement failure, rollback/savepoint rollback and native Reset/Restore do not
undo seed/draw effects; physical close loses the state. Peer sessions remain
independent, and read-only transactions allow these functions. The implementation
uses standard-library PCG; outputs are not cryptographic and exact PostgreSQL
PRNG sequence parity is not promised.

Reversed bounds fail22023 `lower bound must be less than or equal to upper bound`.
Numeric special bounds fail `lower/upper bound cannot be NaN` or
`lower/upper bound cannot be infinity`, lower checked first. Invalid seeds fail
22023 `setseed parameter <value> is out of allowed range [-1,1]`, using six
significant digits or NaN/Infinity/-Infinity. These execution errors carry no
Position/Hint/Detail and leave an existing seeded stream unconsumed.
VOID is result-only, distinct from typed NULL, and does not admit stored columns
or VOID casts/operators.
VOID ORDER keys (including output aliases and ordinals) fail analysis with42883,
`could not identify an ordering operator for type void`, and
`Use an explicit ordering operator or modify the query.` Hint. The error points
at the ORDER reference on empty or populated input, before reseeding or evaluating
any unrelated visible sequence output.

Feature smoke: `make check RUN='^TestRandomSmoke$$'`.
Dual-mode independent regressions:
`make check RUN='^TestReferenceRandom[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceRandom[A-Za-z0-9_]*$'`.
The same definitions preserve per-backend reseed/range/lattice/demand witnesses;
native controls and PostgreSQL adapter boundaries are explicit phases, not
cross-backend stream parity or a widened DEFAULT/VOID storage surface.

### String length and search — change note

`length(text)`, `char_length(text)` and `character_length(text)` count Unicode
codepoints, including combining marks separately. They do not measure graphemes,
display width or normalized text. `octet_length(text)` measures UTF8 bytes and
`bit_length(text)` is eight times that byte count. All return INT4, typmod-1,
without result collation. Overflow fails22003 `integer out of range`.
Empty strings measure zero; successful signatures are strict, so NULL measures
NULL without suppressing invalid argument, signature or authorization errors.

VARCHAR preserves trailing spaces. `length`, both character-count aliases and
`octet_length` have exact BPCHAR overloads: character counts ignore trailing ASCII
spaces, but octet counts include physically stored padding. For CHAR(4) `'é🙂'`,
character counts are2, octet count8 and bit count48. Bit counts have no BPCHAR
overload and use its existing TEXT conversion, removing only trailing U+0020.
These operations never mutate stored CHAR values. Unknown strings and parameters
prefer TEXT; existing known BYTEA counts/search retain raw byte semantics.
No `char_length(bytea)` or `strpos(bytea,bytea)` alias is added.

`strpos(source,needle)` and ordinary quoted/qualified `position(source,needle)`
return the first one-based codepoint position, zero when absent. SQL
`POSITION(needle IN source)` reverses those operands. An empty needle returns1,
even in an empty source; either NULL input returns NULL. Matching is exact and
does not normalize or case-fold. Both BPCHAR operands convert to TEXT, while
TEXT/VARCHAR preserve spaces. Bare comma-form POSITION remains a syntax error.
Matching scalar `VARIADIC` spellings use the same fixed TEXT signatures, not array
arguments or a variadic execution plan; existing BYTEA admission is unchanged.

Search demands a determined **input** collation despite its noncollatable INT4
result. Conflicting explicit collations fail42P21 during analysis. Undetermined
implicit input collation fails42P22 with the COLLATE hint on nonNULL execution,
before empty-input shortcuts; strict NULL bypasses that runtime demand. An
explicit input collation resolves implicit conflict, including inherited wrapper
conflict. Count functions do not demand input collation.

Selected TEXT/BPCHAR functions use the existing query/write/RETURNING, grouping,
ordering, DEFAULT, CHECK and view infrastructure. Compact/pretty stored SQL and
native replay retain exact BPCHAR formal arguments and special POSITION syntax;
ordinary calls display existing implicit TEXT conversions where needed.
Typed structural carriers use `String <provider>` nodes, not PostgreSQL physical
node serialization. Parameter-free Restore INSERTs admit the selected family
through the existing staged, failure-atomic control path.
Codepoint, padding and match-prefix scans check cancellation at bounded work
intervals; search reuses the constant-space raw matcher without rune arrays,
normalization or payload-sized scratch allocation. BYTEA DEFAULT, encoding-aware
length, bit types, positional TEXT and two-argument POSIX substring remain excluded.
Independent scenarios: `make check RUN='^TestReference(StringLength|BinaryBasic|BinarySlice)'`;
pinned counterpart adds `REFERENCE=1` to that same command.

### BYTEA bit and byte access — change note

Selected signatures are `bit_count(bytea) -> bigint`,
`get_bit(bytea,bigint) -> integer`, `get_byte(bytea,integer) -> integer`,
`set_bit(bytea,bigint,integer) -> bytea`, `set_byte(bytea,integer,integer) -> bytea`
and the existing `reverse(bytea) -> bytea`. All are immutable and strict after
analysis/import. Count scans raw octets; getters do not decode text. Byte offsets
start at zero. Bit n uses byte n/8, mask 1<<(n%8): least-significant first within
each byte, unlike BIT orientation. Reverse reverses every raw octet.

Invalid offsets report 2202E `index N out of valid range, 0..M`, including upper
-1 on empty input. Bit offsets preserve signed INT8 extremes. Setters validate
bounds before new values; `set_bit` reports 22023 `new bit must be 0 or 1` for
anything other than 0/1. `set_byte` wraps to the low eight bits of any INT4,
including negative values. These body errors have no Position/Detail/Hint/Context.
NULL in any formal slot suppresses body errors, not invalid signatures, constant
input, imported input or authorization. Setters never mutate storage or retained
results. Raw BYTEA binds remain distinct from SQL byteain literals.

Resolution considers released BIT signatures without executing them. Thus
`bit_count('101')` is ambiguous (42725), `get_bit('101',0)` selects BIT and native
reports 0A000 `bit overloads of get_bit are unsupported` at the call, while
`get_bit('10',1::bigint)` selects BYTEA. `set_bit` and `bit_count` use the analogous
native boundary when BIT wins. All arguments eliminate incompatible candidates
before that boundary; no implicit INT8-to-INT4 narrowing is added. Existing
recognized BIT/VARBIT syntax/type capability boundaries are unchanged.

Inherited native admission boundaries remain explicit: direct unbound
`SELECT bit_count($1)` analyzes overloads before supplied-count validation, so
native reports 42725 `function bit_count(unknown) is not unique` at position8,
with `Could not choose a best candidate function. You might need to add explicit type casts.`
Hint. PostgreSQL's direct no-bind route instead reports 42P02
`there is no parameter $1` at position18 without Hint; Prepare reports the
ambiguity on both. Shared native namespace lookup precedes selected child
binding: `SELECT absent.bit_count(missing) FROM t` reports 3F000
`schema "absent" does not exist` at position8, while PostgreSQL reports 42703
`column "missing" does not exist` at position25. Unicode prefixes shift these
character positions, not byte offsets. These generic boundaries are not widened
by the access binder. Failed typed integer imports retain
`unnamed portal parameter $n = '...'` Context on both backends with no Position;
strict source NULL does not suppress import errors.

The existing query/write/WHERE/order/group/COUNT/SUM/UPDATE RETURNING/CHECK/view,
generated-column/expression-index and symbolic Dump/Reset/Restore consumers
share BinaryPlan. Restore rejects parameters recursively in every argument.
BYTEA DEFAULT functions, INSERT RETURNING and established mixed TEXT/BYTEA
parameter-cast limitations remain excluded. No adjacent BIT execution or casts
are introduced.

Witnesses: `make check RUN='^TestReferenceBinaryAccess.*$'`
and the same command with `REFERENCE=1`; feature smoke uses
`RUN='^TestReferenceBinaryAccessFeatureSmoke$'` in both modes.

### String padding, repetition and reversal — change note

Selected scalar signatures are `lpad(text,integer)`,
`lpad(text,integer,text)`, `rpad(text,integer)`,
`rpad(text,integer,text)`, `repeat(text,integer)`, `reverse(text)` and
`reverse(bytea)`. Each is immutable and strict after full analysis. The two
padding arities remain distinct; omitted fill is U+0020, not NULL fill.
Scalar VARIADIC spelling follows the fixed PostgreSQL signatures without
enabling arrays or a general variadic routine catalog.

Padding width counts UTF8 codepoints, not encoded bytes, terminal columns or
graphemes. Both directions truncate the right side of an overlong source.
Fill repeats from its beginning with a codepoint-safe final prefix; empty fill
prevents extension but not truncation. Negative/zero widths and repeat counts
produce empty TEXT. Repeat preserves exact bytes and spaces. Text reversal
preserves each original codepoint encoding while reversing codepoint order,
including combining marks and ZWJ; binary reversal instead reverses every raw
octet, including NUL, high bytes and invalid UTF8.

Textual inputs consume existing type-owned TEXT conversions: BPCHAR trims
trailing ASCII spaces in source and fill, whereas TEXT/VARCHAR retain them.
INT2 converts implicitly to INT4; INT8/numeric/float/typed TEXT counts require
an existing explicit cast. Unknown literals/parameters infer TEXT or INT4 by
formal role. Unknown reverse prefers TEXT; known BYTEA/domain-base BYTEA
selects the binary result without rebinding its operand. NULL does not hide
invalid signatures, input conversions, explicit collation conflicts or ACLs.
Text results propagate collation without requiring collation-sensitive work;
BYTEA remains noncollatable. Computed TEXT/BYTEA descriptors have typmod-1,
no source attribute origin and unchanged empty/NULL cursor metadata.

Before allocation, UTF8 padding checks `4 * effective_width + 4 <= 0x3fffffff`;
empty fill reduces effective width to the truncated source character count.
Repeat checks `source_byte_length * max(count,0) + 4 <= 0x3fffffff`.
Failures report54000 `requested length too large`, with no Position/Hint/Detail.
Two-argument pad body failures additionally carry
`SQL function "lpad" statement 1` or the rpad equivalent; three-argument pads
and repeat do not. Child/import errors retain their own diagnostics, including
22021's index-only portal Context. Long scans, emission and reversal check
cancellation without rune arrays or an intermediate full padding string.

The providers compose through existing query/write, grouping, RETURNING,
parenthesized DEFAULT/CHECK, view/generated/index and native control consumers.
Late row-dependent output errors publish no partial statement writes.
Restore traverses every selected operand and rejects parameters recursively;
held cursors retain values/descriptors across replacement. The existing general
BYTEA DEFAULT-function and INSERT RETURNING boundaries remain unchanged.
No TEXT-to-BYTEA cast edge is added: an earlier bare `reverse($1)` resolves
that parameter as TEXT, and subsequently casting the same parameter to BYTEA
retains the existing native0A000 cast boundary (PostgreSQL accepts that cast).
Independent TEXT and explicitly BYTEA parameters select their respective
overloads, and held bare reverse frames retain the preferred TEXT descriptor.
Independent family proof:
`make check RUN='^TestReferenceStringPad'`;
the same command with `REFERENCE=1` validates pinned PostgreSQL18.0.
Feature smoke uses `RUN='^TestReferenceStringPadFeatureSmoke$'` in both modes.

### String replacement and prefix — change note

`overlay(source,replacement,start[,count])` and
`OVERLAY(source PLACING replacement FROM start [FOR count])` return unconstrained
TEXT. Offsets and counts are INT4 **codepoints**, not bytes or graphemes; combining
marks are separate characters. The default count is the replacement's character
length after its TEXT conversion. Zero count inserts. Negative counts are valid:
`overlay('abcdef','XY',3,-1)` is `abXYbcdef`. Beyond-end starts append without
padding; large counts remove through the end. Nonpositive starts fail22011
`negative substring length not allowed` before start+count overflow is checked;
overflow fails22003 `integer out of range`. These runtime diagnostics have no
fabricated query Position. Existing BYTEA overlay remains raw-byte indexed.

`replace(source,search,replacement)` replaces nonoverlapping occurrences
left-to-right, literally and without recursively searching emitted replacement
text. Empty source or search returns the original source. It does not implement
regexes, escape expansion, case folding or Unicode normalization.
`translate(source,from,to)` maps each original codepoint by its first position in
`from`; if that position has no corresponding `to` codepoint, it deletes the
character. Extra `to` characters are ignored and duplicate `from` characters use
the first occurrence. Emitted text is not remapped; empty `from` is identity.
Both functions return unconstrained TEXT.

`starts_with(source,prefix)` and `source ^@ prefix` produce the same BOOL/NULL
result. Matching is exact and case-sensitive; an empty prefix matches even an
empty source, while an overlong prefix does not. The prefix may end between
codepoints of a combining sequence; there is no grapheme normalization.

All five operations are strict after successful analysis. Unknown constants and
binds infer TEXT in textual positions and INT4 in overlay's index/count positions.
Every child and signature is analyzed even under NULL. Existing character, NAME,
internal CHAR catalog values and character-domain inputs use their type-owned
TEXT conversion; BPCHAR trims trailing U+0020 in every textual argument, while
TEXT/VARCHAR preserve tails. Numeric/BOOL/BYTEA do not acquire implicit TEXT
conversion. Overlay accepts existing implicit INT2-to-INT4 inputs, but does not
implicitly narrow INT8/numeric/float or convert typed TEXT indices. Known BYTEA
overlay peers retain their existing overload and contextualize unknown byte
operands; all-unknown overlay prefers TEXT.
Unquoted REPLACE remains an ordinary unreserved keyword outside call syntax,
including CREATE OR REPLACE VIEW and relation/column/output names.

Input collation is independent of BOOL result metadata. Prefix demands input
collation before empty/length shortcuts; replace demands it after empty-source/
search shortcuts, including when a nonempty search has no match. Indeterminate
input collation fails42P22 `could not determine which collation to use for string
comparison`, with the existing explicit-COLLATE hint. Strict NULL bypasses that
runtime demand, not conflicting explicit-collation analysis42P21. Overlay and
translate propagate TEXT result collation without runtime collation demand.

These operations reuse scalar query/write, UPDATE/DELETE RETURNING, grouping/
ordering, parenthesized DEFAULT, CHECK and view consumers. INSERT VALUES and
INSERT SELECT consume the same providers; INSERT RETURNING retains its existing
native0A000 suffix boundary. Immutable plans and generation-owned descriptors
retain TEXT/BOOL metadata on empty/NULL rowsets.
Late overlay failures publish no rows/result or partial writes. Stored SQL keeps
special OVERLAY and prefix-operator spelling; ordinary overlay is quoted in
PostgreSQL deparse, with resolved formal casts for ordinary character/index
arguments. Pretty output omits redundant prefix-operator grouping under Boolean
AND/OR/NOT; compact output retains grouping. Native replay and typed
`String <provider>` carriers traverse every
operand and preserve held rows/metadata through staged replacement.
Prepared integer-input errors retain adapter diagnostics, including
`unnamed portal parameter $3 = '...'` Context for invalid overlay start input.
Parameter-free Restore admits the selected producers and recursively rejects
parameters. Search factorization is reused across replacement matches; cuts,
mapping, prefix comparisons, BPCHAR trim and output copies check cancellation
without payload-sized scratch arrays or maps.

Scalar VARIADIC spelling is accepted by the selected replace/translate/starts_with
fixed signatures, not an array/variadic execution feature. Unqualified comma-form
OVERLAY rejects VARIADIC with42601 at the keyword; quoted/qualified calls retain
ordinary function syntax. General public internal CHAR cast admission, TEXT/regex
substring, BYTEA DEFAULT functions and arbitrary/nondeterministic collations
remain outside this part.
Independent witness registry:
`make check RUN='^(TestReferenceStringReplace(Overlay|Replace|Translate|Prefix|Signatures|Diagnostics|Collation|Metadata|Consumers|Atomicity|Stored|Controls|Cancellation)|TestReferenceBinarySliceCapabilityBoundaries)$'`;
the same command with `REFERENCE=1` validates pinned PostgreSQL.

### String formatting — change note

`format(format_text[,arguments...])` and `pg_catalog.format` return computed TEXT
(OID25, typmod-1, ordinary derived collation, no physical table/attribute origin).
The first argument follows TEXT input/coercion rules; subsequent ANY arguments
retain their original datatype, typmod and output behavior rather than sharing
a common type. Output includes BOOL `t`/`f`, padded CHAR, numeric scale, the
existing temporal/settings environment and statement-visible REGCLASS names.
The function is stable and nonstrict, with every supplied child evaluated once,
left-to-right, including unused children and children of a NULL format string.
NULL format strings return NULL TEXT only after that child evaluation.

Runtime syntax is `%[position$][-][width]type`: supported types are `%s`, `%I`
and `%L`; bare `%%` emits one percent and consumes no argument. `%s` emits
datatype output without quoting, `%I` uses canonical PostgreSQL identifier
quoting, and `%L` doubles apostrophes and backslashes with uppercase `E` when
needed. NULL becomes empty `%s` or unquoted `NULL` for `%L`; NULL `%I` fails22004.
Unreserved identifiers such as `abort` need no quotes, while reserved words,
uppercase, non-ASCII, empty and otherwise nonidentifier spellings are quoted.

Positions start at1. Sequential consumption follows the last consumed argument,
not the greatest positional argument. Width may be decimal, `*`, or `*n$`;
dynamic width is consumed before the value. NULL width means0, negative width
left-aligns, repeated minus flags are accepted, and leading zeroes are decimal
width spelling rather than zero-padding. Width counts Unicode codepoints in the
final quoted output (including quotes/escapes); combining marks count separately.
Over-width values are never truncated. INT2/INT4 widths use their integer value;
other types use original datatype output and the existing INT4 input grammar.

Explicit `VARIADIC` accepts exactly the format string plus any already-supported
typed array datum, or a domain over it. It borrows the upstream immutable flat
payload and preserves each element's original datatype and NULL identity;
existing array input owns quoted `"NULL"`, empty elements, escapes, lower bounds
and row-major multidimensional order. NULL/empty arrays supply zero arguments.
Expansion may exceed100 elements; the ordinary syntactic call limit remains100
arguments, including the format string. Preceding ordinary arguments with
VARIADIC retain the recorded missing-signature error. Format implements no new
array types or syntax, arbitrary successful routine dispatch or `sprintf`.
REGCLASS-array literal annotations retain their existing array cast identity in
both `value::regclass[]` and `CAST(value AS regclass[])`, including native replay
of typed leaves in stored format expressions. Ordinary array output and borrowed
VARIADIC elements use statement-visible relation names. Direct REGCLASS-array
columns and bounded scalar casts are now admitted by the regclass change below.
Resolution-only absent-call admission is confined to actual selected format
arguments: missing nested children retain42883 without installing providers.
Selected format spelling/signature diagnostics remain unchanged. Outside those
arguments, old generic-call/keyword capability guards remain native0A000,
including standalone `sprintf` (independently42883 on PostgreSQL); no special
name handling or successful no-FROM widening is added. Bare `format` stays an
ordinary identifier, including table/type declarations.

Unknown format-position parameters infer TEXT only after all children, the
syntactic argument limit, namespace and signature selection have been analyzed.
An unresolved first occurrence constrained by a later `$1::integer` therefore
fails with42P08, `inconsistent types deduced for parameter $1`, Detail
`integer versus text`, at the first occurrence; a later `$1::text` is compatible.
ANY arguments alone cannot infer parameters: direct and prepared calls share
42P18/42P08 analysis rules, and no supplied Go value or later runtime width
changes parameter analysis. Pattern
grammar, widths and output errors remain evaluation-owned, not prepare-time
constant folds. English diagnostics preserve complete severity/code/message,
hint and character location: malformed verbs/specifiers and too-few arguments
use22023, zero argument positions22023, numeric/width overflow22003, and INT4
input failures retain22P02. Missing signatures42883, bad scalar VARIADIC42804
and excessive syntax arguments54023 retain their analysis locations. Invalid
verbs precede width/value extraction; NULL identifier precedes INT_MIN padding
overflow. For an ordered late NULL identifier failure the pinned18.0 contract
is an immediate Query error with no cursor, not a partially delivered result.

Selected format and its admitted diagnostic failure paths transform all children
once, then check the syntactic100-argument limit, then resolve namespaces and
signatures. Invalid children therefore precede both54023 and missing-schema
3F000, including NULL format strings and quoted `"FORMAT"`/scoped missing calls.
Quoted or missing calls with100 valid arguments retain42883 and the ordinary
explicit-casts hint;101 valid arguments use54023 at the call, without a hint.
Over-limit analysis traverses children without allocating executable argument
storage or diagnostic type-name slices; unrelated providers keep their existing
analysis order and unsupported-call guards.

The shared node participates in predicates, CASE/COALESCE/extrema, grouping,
sorting, joins, LIMIT legality, INSERT/UPDATE/DELETE RETURNING, DEFAULT/CHECK,
views, catalog retention/type rewrites and native normalization/restore. Stored
rendering preserves typed ANY leaves, VARIADIC spelling and held descriptor
generations. Stable format is rejected by immutable generated/index contexts;
volatile children remain volatile, and lazy conditional consumers remain lazy.
Collation metadata derives normally, including conflicts, but format itself
does not demand collation-sensitive execution. Parser, quoting, array decoding,
copying and padding use bounded cancellation checks without intermediate quoted
payload copies, token arrays or format caches.
Public metadata witnesses use standard `sql.Rows` descriptors and stored-view
results; derived collations are exercised by demanding `lower` consumers across
stored views. Exact OID25, typmod-1, collation100/950/6411 and no-origin assertions
use the real package binder/schema harness, not an unexposed `pg_attribute`.
Formatted aggregate descendants use the ordinary aggregate providers, including
numeric SUM. DEFAULT introspection joins exposed pg_attrdef and pg_class on
matching OID types rather than adding an OID=REGCLASS operator.
Direct computed format reports unknown nullable metadata. Projecting its stored
view column preserves the existing native catalog-known nullable descriptor
(`true/true`); pq still exposes unknown (`false/false`) nullable metadata.

Independent public witness registry:
`make check RUN='^TestReferenceFormat'`;
`make check REFERENCE=1 RUN='^TestReferenceFormat'`.
Actual end-to-end feature smoke:
`make check RUN='^TestReferenceFormatSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceFormatSmoke$'`.
Each selected check has a900-second external command deadline. Package units exercise the
provider, bound metadata/evaluation, immutable descriptor retagging and existing
typed array consumption; public witnesses share untagged definitions in both modes.

### Core string functions — change note

`btrim(text[,set])`, `ltrim(text[,set])` and `rtrim(text[,set])` remove endpoint
codepoints belonging to a set, not a substring. The omitted set is exactly ASCII
space; empty sets change nothing, and tabs/nonbreaking spaces are not implicit
whitespace. SQL `TRIM` supports BOTH/LEADING/TRAILING, the FROM variants and
ordinary argument-list variants, lowering to these providers with the source
first. There is no ordinary `trim()` function alias.
Endpoint and set-membership scans check cancellation during bounded work intervals
and return a slice of the immutable input without allocating a replacement string.
Shared trim names select TEXT or the existing two-BYTEA signature after analyzing
operands once; a known BYTEA peer infers unknown BYTEA arguments, while all-unknown
arguments prefer TEXT.


`||` resolves the release TEXT/TEXT, TEXT/ANYNONARRAY and ANYNONARRAY/TEXT string
signatures over existing scalars. Successful results are TEXT, typmod-1.
BPCHAR conversion removes trailing ASCII spaces; TEXT/VARCHAR preserve theirs.
BOOL mixed output is `true`/`false`, without changing ordinary boolean export.
BYTEA, numeric widths, OID, NAME, temporal values, UUID, REGCLASS and VOID retain
their type-owned text output. Prepared executions capture current ByteaOutput,
DateStyle, IntervalStyle and TimeZone settings; eager results remain owned.
REGCLASS presentation also uses the captured relation search path: names are
quoted and schema-qualified when needed, while nonexistent identities remain
numeric. Held prepared concatenation observes current execution namespace
visibility without changing its internal OID or retaining a live formatter.
Volatile VOID children execute normally even though their text output is empty.
Internal CHAR preserves PostgreSQL category-aware ambiguity with string/unknown
peers (42725); CHAR/integer is a successful mixed overload with printable octal
General array operators (including NAME[] comparison), `concat()` and new
cast/type families are not added. Existing BYTEA/BYTEA concatenation is preserved;
known BYTEA/unknown selects it, while unknown/unknown prefers TEXT.

`normalize(text[,form])` and `is_normalized(text[,form])` default to NFC.
Unquoted SQL `normalize` requires an NFC/NFD/NFKC/NFKD keyword; quoted or
qualified ordinary calls accept ASCII-case-insensitive TEXT form names without
whitespace trimming or Unicode lookalike folding (for example, Kelvin SIGN is
not ASCII K). `IS [NOT] [form] NORMALIZED` yields BOOL and uses the same
release quickcheck. Resolved signatures are strict: NULL skips the body; malformed
forms otherwise fail22023 without a runtime call Position. Binding/arity/type/
namespace failures occur during analysis, including beneath typed NULL.

`lower(text)`, `upper(text)`, `casefold(text)` and `initcap(text)` return TEXT/-1
and inherit input collation, not VARCHAR/BPCHAR width. Unknown strings, NULLs
and bare parameters infer TEXT; typed numeric/BOOL/BYTEA arguments and wrong
arities fail42883. BPCHAR-to-TEXT removes trailing ASCII spaces; TEXT/VARCHAR
preserve them. NULL bypasses case classification, but not analysis of invalid
children or explicit collation mismatches. NonNULL indeterminate collation
fails42P22 with the COLLATE hint; PostgreSQL18.0 casefold uses `lower()` in that
diagnostic. Namespace and authorization checks remain the ordinary shared rules.

The fixed modes are:

| Collation | Case conversion and initcap words |
|---|---|
| default100 / C950 / POSIX951 | ASCII lower/upper; fold equals lower; ASCII letters/digits form words and every nonASCII byte separates them |
| pg_c_utf8 811 | Unicode16 simple lower/upper and C+S fold; initcap uses simple UPPER then lower; Alphabetic plus ASCII digits form words |
| pg_unicode_fast 6411 | Unicode16 full lower/upper/title and C+F fold; initcap uses TITLE then lower; Alphabetic plus Unicode Nd form words |

Full mappings can expand (`upper('ß')` → `SS`, fold `ﬃ` → `ffi`, lower `İ` →
`i`+U+0307), without clipping to source width. Full lower uses original-input
Final_Sigma, skipping Case_Ignorable on either side. PostgreSQL18.0 also accepts
a nonempty all-ignorable prefix: lower(U+0345 followed by Σ) ends in `ς`, and
initcap produces `Ις`. Bare Σ and a nearest nonignorable uncased predecessor
remain nonfinal; a following nonignorable cased letter also prevents final sigma.
Context comes from the original input, not emitted title expansions.
Folding does not use final sigma and performs no normalization.
Cherokee folding can produce uppercase.
Unconditional SpecialCasing and Final_Sigma apply; tr/az/lt tailoring does not.
Initcap uses alnum runs, not linguistic word segmentation: underscores,
apostrophes and nonAlphabetic marks separate words; a leading digit consumes
the word start rather than capitalizing its next letter. Fullwidth digits are
word members only in the full mode. Scans, including sigma lookahead, check
cancellation during bounded work; unchanged strings are borrowed.

These seeded and registered user collation identities use deterministic UTF8
byte/codepoint ordering. CREATE COLLATION admits truthful libc C/POSIX and builtin
providers, not arbitrary libc/ICU locales or nondeterministic comparison. No new
native encoding setting is added. Native text remains UTF8. The real
PostgreSQL LATIN1 control rejects nonNULL casefold, including empty text, with
42601; strict NULL bypasses that restriction. Unicode-collation ILIKE is a
documented native0A000 boundary, not silently ASCII matching; plain LIKE works.
Case expressions use existing queries/writes/RETURNING/defaults/CHECKs/views,
stored deparsing and native replay. Parenthesized DEFAULT forms retain current
grammar limits; no new bare DEFAULT-call admission.
Compact/pretty stored expression and CHECK presentation quotes collation
identifiers only when required: `pg_unicode_fast` remains unquoted, while
`"C"` and `"POSIX"` retain their case-preserving quotes. Returned SQL is executable.
Qualified and unqualified case calls use the existing selected-string scalar
grammar within ordinary table and no-FROM queries. Plain LIKE/ILIKE projections
use the same local input, subject to their existing provider/collation restrictions.

Normalization uses pinned Unicode16 canonical/compatibility data, recursive
decomposition, stable canonical ordering, exclusions, blocking and algorithmic
Hangul. PostgreSQL18.0's inclusive TBASE composition consumes U+11A7 after an LV
syllable: NFC/NFKC of U+AC00 U+11A7 is U+AC00, yet its release quickcheck predicate
is TRUE. This is release compatibility, not a claim that the predicate always
equals normalize(input)==input. The independent official corpus remains unchanged,
with a separate release-boundary regression. Normalization performs no case
folding, locale conversion, stream-safe CGJ insertion or truncation.

These expressions compose in existing queries, writes/RETURNING, grouping,
defaults/checks, views and native Dump/Restore, including ordinary no-FROM
SELECT with existing scalar siblings. Native text stays UTF8;
there is no server-encoding configuration. The independent LATIN1 PostgreSQL
oracle records nonnull normalization's encoding restriction; strict NULL still
bypasses it. Input UTF8/NUL validation remains at existing textual boundaries.
Stored DEFAULT/CHECK string nodes support all existing compact/pretty deparse
signatures and typed native structural carriers. SQL TRIM, special NORMALIZE
(including omitted versus explicit NFC), and normalized predicates retain their
syntax through Dump/Restore, rename and supplied-relation rendering; ordinary
normalize calls render as quoted function names. Ordinary calls display resolved
non-TEXT character argument conversions, including source, set and form operands;
special syntax hides those implicit conversions. Rendering never installs runtime
coercions or evaluates arguments. Structural output records `String <provider>`
with recursive typed children, not PostgreSQL physical nodes.
Parameter-free Restore INSERTs accept the selected string function family,
LIKE escape normalization and the seven BYTEA hashes documented below;
they recursively reject parameters and unsupported nested producers. Failed
normalization publishes no replacement and preserves held old results.
String-dependent INSERT rows analyze source occurrences before target assignment;
their conflicting parameter diagnostics use existing occurrence coercion. This
does not change the native stringless assignment or settings inference boundary.
The shared concatenation grammar does not admit other selected functions as bare
CREATE DEFAULTs: their existing `0A000`, `functions are unsupported` rejection
remains positioned at the function occurrence, including visibility inquiries.

See [data provenance and regeneration](TYPES.md#unicode16-normalization-data).
Smoke: `make check RUN='^TestStringCoreSmoke$'`.

### Scale and rounding functions — change note

The existing selected-call projection/nesting and call-rooted singleton path
also admits these immutable, strict PostgreSQL18 mathematical overloads:

| Function | Arguments → result |
|---|---|
| `scale`, `min_scale` | NUMERIC → INT4 |
| `trim_scale` | NUMERIC → NUMERIC |
| `round`, `trunc` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8; NUMERIC, INT4 → NUMERIC |
| `sign` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8 |
| `mod` | INT2,INT2 → INT2; INT4,INT4 → INT4; INT8,INT8 → INT8; NUMERIC,NUMERIC → NUMERIC |
| `abs` | INT2 → INT2; INT4 → INT4; INT8 → INT8; FLOAT4 → FLOAT4; FLOAT8 → FLOAT8; NUMERIC → NUMERIC |
| `ceil`, `ceiling`, `floor`, `exp` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8 |
| `div` | NUMERIC,NUMERIC → NUMERIC |
| `factorial` | INT8 → NUMERIC |
| `gcd`, `lcm` | INT4,INT4 → INT4; INT8,INT8 → INT8; NUMERIC,NUMERIC → NUMERIC |
| `acos`, `acosd`, `asin`, `asind`, `atan`, `atand` | FLOAT8 → FLOAT8 |
| `atan2`, `atan2d` | FLOAT8,FLOAT8 → FLOAT8 (`y,x` argument order) |
| `cos`, `cosd`, `cot`, `cotd`, `sin`, `sind`, `tan`, `tand` | FLOAT8 → FLOAT8 |

Exact NUMERIC selects its numeric overload. Unary integer/REAL
`round`/`trunc`/`sign` select the preferred FLOAT8 overload. Unknown unary
`round`/`sign` also select FLOAT8, but unknown unary `trunc` fails `42725`
because PostgreSQL also has competing nonnumeric overload categories; those
providers are not added here. Numeric-only functions implicitly accept
integers, not known floating inputs. Scaled calls
accept SMALLINT scale widening, but not BIGINT/NUMERIC/FLOAT scale narrowing.
Mixed integer modulo selects the wider integer; integer/NUMERIC selects NUMERIC.
Two unknown modulo arguments are ambiguous (`42725`), not Go-kind inferred.
Missing signatures use `42883` and the standard explicit-casts Hint at the
function token. Invalid unknown literals use the selected input codec at the
literal position. Prepared signatures and result metadata remain fixed across
bindings; NUMERIC results have unconstrained typmod, even from sized columns.

`abs` selects exact integer/REAL widths rather than widening them. Integer/REAL
inputs to `ceil`/`ceiling`/`floor`/`exp` select FLOAT8; unknown unary inputs to
these functions or `abs` also select FLOAT8. `div` contextualizes both unknown
parameters as NUMERIC; `factorial` contextualizes its parameter as INT8 and
implicitly widens SMALLINT/INTEGER, but rejects NUMERIC/REAL/FLOAT8 (including
`factorial(5.0)`). No parameter's Go value changes the prepared signature.

`gcd` and `lcm` have exactly the three binary overloads above, with no SMALLINT,
REAL or FLOAT8 signature. Mixed INTEGER/BIGINT chooses BIGINT; an integer with
NUMERIC chooses NUMERIC. Two SMALLINT inputs, SMALLINT with an unknown input,
or two unknown strings/NULLs/parameters are ambiguous (`42725`). A known
INTEGER/BIGINT/NUMERIC peer fixes an unknown argument's type; the Go argument
kind never chooses an overload. Either typed NULL returns the selected typed
NULL, even `lcm(0,NULL::integer)`.
Quote-sensitive names retain their spelling: `"gcd"` resolves, while `"GCD"`
is admitted for ordinary missing-function `42883` diagnostics, not folded into gcd.
Invalid typed binds retain the shared unnamed-portal parameter Context on both
adapters, without a query Position; literal input failures retain source Position.

Both functions ignore finite input signs and return nonnegative exact values.
`gcd(0,0)=0`; `gcd(0,n)=abs(n)`; `lcm` returns zero if either finite argument
is zero. Fixed-width gcd rejects a positive result beyond the selected maximum:
`gcd(minimum,0)` and `gcd(minimum,minimum)` overflow, while
`gcd(minimum,-1)=1`. Zero lcm succeeds even with a signed-minimum peer.
Nonzero lcm checks the final result after dividing by gcd, so `lcm(maximum,maximum)`
succeeds but `lcm(maximum,2)` fails. Width errors are unlocated `22003`,
`integer out of range` or `bigint out of range`, at Execute rather than Prepare.

NUMERIC gcd/lcm use exact decimal arithmetic, not integer rounding or FLOAT8.
The result scale is the greater input display scale, including equal inputs
and zero shortcuts: `gcd(-1.20,0.1800)=0.0600`,
`lcm(-1.20,0.1800)=3.6000`, `gcd(0.00,0.000)=0.000`.
Either numeric NaN or infinity produces canonical NaN before the zero rule,
including `lcm(0.0,'Infinity'::numeric)`; STRICT NULL still precedes that body rule.
Numeric precision/scale remain subject to the full format limits; an excessive
finite lcm result fails unlocated `22003`, `value overflows numeric format`.
Computed numeric descriptors do not inherit input typmods. These providers
reuse ordinary child analysis, execution preflight, statement atomicity and
stored-view/generated/index consumers; they do not expand bare function DEFAULTs.


NUMERIC rounding uses ties away from zero; truncation uses toward zero.
The scale request clamps to [-131073,16383] for round and [-131072,16383] for
trunc, independently of NUMERIC typmod's ±1000 scale limit. Output has
`max(scale,0)` fractional places. Carry beyond the full format fails `22003`,
`value overflows numeric format`, without a fabricated execution Position.
`scale(8.4100)=4`, `min_scale(8.4100)=2`, `trim_scale(8.4100)=8.41`.
Finite zero/integer minimum scale is zero. Scale/minimum-scale return typed
NULL for NaN/infinities; trimming/rounding/truncation preserve those specials.
NUMERIC sign returns unscaled -1/0/1 or NaN.

FLOAT8 round uses nearest-even in the pinned reference environment; PostgreSQL's
manual permits host-dependent floating tie behavior. FLOAT8 round/trunc preserve
negative zero, NaN and infinities. FLOAT8 sign returns positive zero for NaN and
either signed zero, unlike NUMERIC sign(NaN).
Remainder has the dividend's sign, with exact NUMERIC output at the greater
finite operand display scale. Finite modulo infinity preserves the dividend's
original scale. NaN wins even modulo zero; infinity modulo a nonzero operand
is NaN. Other zero divisors fail `22012`, `division by zero`, without Position.
Integer minimum modulo -1 is zero without overflow.

Every child is analyzed before strict NULL elimination. Runtime NULL evaluates
demanded siblings first. Execution simplification visits immutable children
before eliminating a strict literal-NULL call: `round(NULL,mod(1,0))` therefore
fails division by zero, while a row-dependent sibling may be eliminated.
Prepare/Describe never invokes math providers. Shared query/DML execution
preflight applies math simplification even without a conditional wrapper,
including selected-call singleton queries. Existing execution conditional
pruning/folding, grouping by projection ordinal, UPDATE/INSERT SELECT atomicity,
ACLs, view capture and
native replacement remain the shared consumers. These bounded functions do not
introduce a general function registry, new WHERE/DEFAULT/INSERT VALUES or setting
admission, or general SELECT-without-FROM. Exact operators are covered separately
by [numeric arithmetic](#numeric-arithmetic--change-note).
Dump/Restore stores exact results and replays supported view syntax, not private
function state.
Selected calls remain projection/nested-call roots, not new CASE result leaves
or general GROUP BY expressions. Math around an existing CASE with scalar arms
and `round(count(*),1)` are admitted; min/max use the ordinary aggregate stage.

Independent public reference:
`make check REFERENCE=1 RUN='^TestReferenceMath(Signatures|NumericRoundScale|SignModulo|Diagnostics|Consumers)$'`.
The gcd/lcm family uses `make check RUN='^TestReferenceMathGcdLcm'`
and `make check REFERENCE=1 RUN='^TestReferenceMathGcdLcm'`.

### Inverse trigonometric functions — change note

The six unary and two binary inverse trigonometric functions above each have
exactly one unconstrained FLOAT8 signature. SMALLINT, INTEGER, BIGINT, REAL and
NUMERIC implicitly widen to FLOAT8; binary operands widen independently.
Unknown string literals and inferred parameters use FLOAT8 input, including
typed NULL results. Known TEXT does not implicitly convert. Qualification,
search-path visibility, quote-sensitive names, missing-signature diagnostics and
held-statement inference reuse the mathematical namespace/type contracts.

`acos`, `asin`, `atan` and `atan2` return radians; the `d` variants return degrees.
Principal ranges are `[0,π]`/`[0,180]` for acos/acosd,
`[-π/2,π/2]`/`[-90,90]` for asin/asind and atan/atand, and
`[-π,π]`/`[-180,180]` for atan2/atan2d. The binary order is **atan2(y,x)**:
axes, all four quadrants, signed-zero endpoints and infinite pairs are retained,
without division by x. asin/asind/atan/atand preserve negative zero.

Degree evaluation uses PostgreSQL's scaled inverse formulas rather than plain
radians×180/π. The notable results are exact: asind(-1,-.5,0,.5,1) gives
(-90,-30,0,30,90), acosd gives (180,120,90,60,0), atand(±1)=±45,
atand(±Infinity)=±90, and atan2d axis/quadrant angles are exact multiples of45.
These input/output lists describe separate calls, not additional SQL signatures.
NaN propagates through all eight functions, including either binary operand;
SQL NULL remains strict, distinct from NaN, and retains FLOAT8 metadata.

acos/acosd/asin/asind reject finite values outside[-1,1] and either infinity
with `22003`, exact English primary `input is out of range`. Execution diagnostics
have absent Position, Detail, Hint and other optional logical fields; no C source
locations are fabricated. atan/atand accept infinities, and binary zero x/both-zero
arguments are valid. No zero-underflow exception is introduced. Subnormal degree
results follow the release's operation order, not ideal real-number scaling:
minimum positive FLOAT8 gives60 minimum-subnormal units for asind and45 for atand,
with corresponding negative signs.

Prepare/Describe establishes types without provider execution. Existing immutable
child simplification precedes strict literal-NULL elimination; for example
`atan2(NULL,asin(2))` fails during Execute, while a row-dependent bad sibling can
be eliminated beside literal NULL. Runtime NULL still evaluates demanded siblings.
Existing projection/nesting, aggregate, INSERT SELECT, UPDATE and stored-view
consumers retain statement atomicity and native Dump/Restore/retained-reader rules.
No hyperbolic, pi/radians/degrees providers, general function registry
or new DEFAULT/WHERE/INSERT VALUES admission is implied.

PostgreSQL uses host C libm, whereas native evaluation uses Go math. Ordinary
last-bit accuracy and boundary behavior can vary by platform; independent tests
allow at most2ULP for their normal-range nonzero interior targets, but degree
specials, IEEE categories/zero signs and release-order subnormal witnesses are exact.
The pinned PostgreSQL18.0 oracle is unchanged.

Shared public smoke: `make check RUN='^TestReferenceMathTrigFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceMathTrigFeatureSmoke$'`.
Complete selected proof: `make check RUN='^TestReferenceMathTrig'` and the same
command with `REFERENCE=1` (900-second external deadlines).
The separate root-owned temporary public Go smoke is invoked as
`timeout 60s go run ./.math-trig-feature-smoke` after materializing its source;
it is removed after evidence capture, not installed as a permanent executable.

### Direct trigonometric functions — change note

`cos`, `cot`, `sin`, `tan` and their degree-input counterparts `cosd`, `cotd`,
`sind`, `tand` each have one unconstrained FLOAT8→FLOAT8 signature. They reuse
the mathematical binder, namespace resolution, implicit SMALLINT/INTEGER/BIGINT/
REAL/NUMERIC widening, unknown FLOAT8 input and held-parameter inference.
Known TEXT/BOOL arguments, wrong arity, named/VARIADIC calls and invisible or
quote-mismatched function names retain full `42883` diagnostics and explicit-casts
Hint. Invalid unknown input uses `22P02` with English double-precision wording:
SQL literals have character-based Position, wire bound input has portal Context
without Position, and SQL EXECUTE literal conversion has SQL Position.

Degree functions use the REL_18_0 scaled first-quadrant formulas with finite
argument reduction modulo360 before conversion. Notable results are exact:
sind at0/30/90/150/180/210/270/330/360 gives0/.5/1/.5/0/-.5/-1/-.5/0;
cosd at0/60/90/120/180/240/270/300/360 gives1/.5/0/-.5/-1/-.5/0/.5/1.
At0/45/90/135/180/225/270/315/360, tand gives
0/1/+Infinity/-1/0/1/-Infinity/-1/0 and cotd gives
+Infinity/1/0/-1/-Infinity/1/0/-1/+Infinity. These lists describe individual
unary calls, not additional signatures. Negative rotations and large finite
arguments retain the release's reduction and zero/pole signs.

NaN propagates and NULL is strict, distinct from NaN, with FLOAT8 metadata even
for empty results. Every selected function rejects either infinite input with
`22003`, exact English primary `input is out of range`, absent Position/Detail/
Hint/Context and all other optional logical fields. Runtime errors do not invent
C source locations. Tangent/cotangent poles permit signed infinities, without
`22012`; finite tiny values may round to zero, without an underflow exception.
Radian sin/tan preserve negative zero and cot(-0)=-Infinity. Degree sind(-0)=-0,
cosd(-0)=1, tand(-0)=+0 and cotd(-0)=-Infinity; degree tan/cot normalize every
zero result to+0. Degree subnormals follow binary64 operation order, not ideal
real-number scaling.

Prepare establishes types without provider execution, including bad infinite
constants. Existing immutable execution preflight and strict literal-NULL
simplification remain shared: `atan2(NULL,sin('Infinity'))` fails at Execute,
whereas a row-dependent child beside literal NULL can be eliminated; runtime
NULL still demands siblings. Existing projections, aggregates, INSERT SELECT,
UPDATE, SQL PREPARE/EXECUTE and stored views retain statement atomicity, recovery
and native Dump/Restore/retained-reader behavior. No new DEFAULT/WHERE/INSERT VALUES
admission, inverse/hyperbolic overload, pi/radians/degrees function or generalized
registry is added.

PostgreSQL uses host C libm; native uses Go math without C errno emulation.
Ordinary accuracy and boundary behavior can vary. Independent normal-range
nonzero targets permit at most2ULP; exact degree specials and IEEE signs/categories
do not. Near-zero radian/degree boundary witnesses assert finite nonzero category,
sign and bounded magnitude rather than universal ULP parity. Approximate binary
π/π/2 inputs are not snapped to ideal radian zeros/poles. Release provenance and
the pinned PostgreSQL18.0 UTF8/C oracle are unchanged.

Shared smoke: `make check RUN='^TestReferenceMathTrigDirectFeatureSmoke$'` and
the same command with `REFERENCE=1`. Complete direct/inverse public proof selects
`RUN='^TestReferenceMathTrig'` in both modes; private provider/parser/binder units
select `RUN='^TestMathTrig'`. Selected checks have900-second external deadlines.
The root-owned temporary standalone source comes from the living plan Appendix A:
`timeout 60s go run ./.math-trig-direct-feature-smoke`; remove it after recording
observations, not install it as a permanent executable.


### Numeric scalar functions — change note

`abs` preserves finite numeric display scale, normalizes floating negative zero
to positive zero, and maps negative infinity to positive infinity. Signed
integer minima fail `22003` with `smallint out of range`, `integer out of range`
or `bigint out of range`. Numeric/floating NaN remains NaN.

Numeric `ceil`/`ceiling`/`floor` return scale-zero integers, using exact decimal
fraction/carry rules; `ceil` and `ceiling` differ only in their SQL name.
For example, `ceil(-42.8)=-42` and `floor(-42.8)=-43`. All preserve NaN and
infinities. Floating operations preserve libm signed zero, including
`ceil('-0.5')=-0`. Decimal carry beyond 131072 integer digits fails `22003`,
`value overflows numeric format`.

`div` is exact truncation toward zero with scale-zero output:
`div(-9.00,4.000)=-2` and `div(9007199254740993,2)=4503599627370496`.
NaN wins even over zero; infinity/infinity returns NaN; infinity divided by a
finite nonzero value returns signed infinity; finite/infinity returns zero.
All other zero divisors fail `22012`, `division by zero`; overflowing finite
quotients fail `22003`, `value overflows numeric format`.

`factorial(0)` and `factorial(1)` are unscaled 1; `factorial(20)` is exact
2432902008176640000. Its maximum input is 32177; larger values fail `22003`,
`value overflows numeric format`. Negative inputs fail `22003`,
`factorial of a negative number is undefined`. Cancellation remains an ordinary
context error and does not publish partial numeric results or DML changes.

Numeric `exp` uses guarded decimal range reduction/series/squaring, not a
binary64 result formatted as decimal. `exp(1.0)=2.7182818284590452`; higher
input display scale retains greater precision. Finite result scale is
`min(1000,max(0,input_scale,16-trunc(clamp(float8(input)*log10(e),-2000,2000))))`.
The float estimate controls precision and PostgreSQL's finite ±6000 guard,
not the decimal coefficients. Inputs whose estimate reaches +6000 fail
`22003`, `value overflows numeric format`; estimates at or below -6000
produce zero at the selected scale. The guard uses represented float estimates,
so sufficiently close high-precision decimal neighbors can hit the same bound.
Numeric NaN/+Infinity persist; -Infinity yields unscaled positive zero.

Floating `exp` preserves NaN/+Infinity and maps -Infinity to positive zero.
Finite inputs producing infinity fail `22003`, `value out of range: overflow`;
those producing zero fail `22003`, `value out of range: underflow`. The pinned
PostgreSQL18.0 environment accepts nonzero subnormals, including `exp('-745')`.
Host floating results remain subject to the PostgreSQL manual's libm caveat.
Provider errors above have no invented Position/Detail/Hint. Prepare fixes types
without evaluating these immutable operations; domain errors belong to Execute.

Selected scalar coverage: `make check RUN='^TestReferenceMathScalar'`.
Independent pinned coverage: `make check REFERENCE=1 RUN='^TestReferenceMathScalar'`.


Table SELECT projection/WHERE admit `IS [NOT] NULL`, `ISNULL`/`NOTNULL`,
`IS [NOT] DISTINCT FROM`, and Boolean `IS [NOT] TRUE/FALSE/UNKNOWN`.
These return nonnull BOOL with `?column?` names and scalar origin zero;
native ScanType is bool and expression nullability metadata stays unknown.
Nullness accepts every supported scalar type; Boolean tests contextualize
unknown inputs to BOOL, but concrete nonboolean operands fail42804 at the
operand. DISTINCT uses existing typed equality for nonnull inputs, considers
two NULLs equal and one NULL distinct; NOT DISTINCT complements it. Both
sides admit scalar atoms, comparisons or grouped predicates/logical expressions.
Unknown operands adopt the known opposite type, or TEXT if both are unknown.
Incompatible concrete types fail42883 at IS even on empty/all-null input. A bare NULL literal on
either side rewrites to nullness without equality inference; a nil parameter
does not. Thus `$1 IS DISTINCT FROM NULL` fails42P18/no Position just like
`$1 IS NULL`, regardless of supplied Go value. Earlier nested unknown parameter
occurrences do not retroactively acquire later slot types: `$1 IS NULL,$1 IS TRUE`
fails42P08 at the first occurrence, while reversed order fixes BOOL and works.
Root output TEXT fallback and mixed-assignment limits remain unchanged.
Selected occurrence consistency precedes shared-slot gaps/unresolved types:
`$1 IS NULL,$1 IS TRUE,$2 IS NULL` fails42P08/$1 at character8, not42P18/$2.
An inconsistent `$2` occurrence similarly precedes the missing `$1` slot.
Without occurrence inconsistency, `$1 IS TRUE,$2 IS NULL` retains42P18/$2
without Position; validation still precedes count-sized parameter allocation.

ANY arguments and selected null tests retain unknown occurrences in shared
binding order: `num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE` fails42P08
for$2 at18; swapping its first two targets fails for$1 at8. A still-unresolved
ANY slot does not mask a later selected occurrence mismatch.
Parentheses group admitted atoms, comparisons, predicates and logical expressions.
Unary postfix chains nest left: `TRUE IS NULL IS TRUE` and `v IS NULL ISNULL`
both return false. The shared
nonassociative IS/ISNULL/NOTNULL precedence rejects an ungrouped predicate
competing with a binary DISTINCT right operand (`1 IS DISTINCT FROM 1 IS TRUE`
fails42601); explicit grouping admits the intended nesting. Scalar comparisons
bind more tightly than selected predicates, which bind above NOT, AND and OR.
`v=1 IS TRUE` and `(v=NULL) IS UNKNOWN` work in projection and WHERE.
Ordinary `v=NULL` still selects no rows; no transform_null_equals change.
Comparison operands admit bounded scalar arithmetic and baseline and
character-family casts, but not comparison chaining or nested logical/comparison
expressions. Predicates also accept these scalar expressions and use their
resolved type and expression start. General functions/casts outside the bounded
baseline and character-family sets, rows/composites and no-FROM scalar
queries remain excluded except for the selected call roots described above.
Null-count calls remain standalone projection roots, not predicate operands;
predicate expressions remain excluded from their arguments.
Ordinary INSERT VALUES/settings arguments remain scalar-only. INSERT SELECT
executes existing predicate and selected null-count projection roots through the
common query pipeline; a trailing `VALUES (1)` in
`INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)` still fails42601 at VALUES (42).
Genuinely unsupported calls retain their existing call guards.
Native controls retain finalized computed BOOL
values/metadata; dependency/ACL use includes both DISTINCT operands.
Feature smoke: `make check RUN='^TestCompareNullSmoke$'`;
native: `make check RUN='^TestReferenceCompareNull.*$'`;
real pinned reference: `make check REFERENCE=1 RUN='^TestReferenceCompareNull.*$'`.

Baseline explicit casts extend the scalar positions above: `CAST(expr AS type)`,
`expr::type`, and function-style int4/int8/text/bool calls, with nested/grouped
operands. Standard/postfix targets retain baseline declaration aliases; function
aliases integer/bigint/boolean/int are not admitted. The baseline matrix supports
INT4/INT8/TEXT/BOOL source and target types; character-family and bounded OID
edges are described separately. Other families, including identities, fail0A000.
The direct baseline matrix permits integer width conversion, integer
text output, TEXT input to integers/BOOL, BOOL text output as true/false and
INT4↔BOOL (nonzero true; false0/true1); INT8↔BOOL remains unsupported.
NULL keeps target metadata; unsupported pairs cannot bypass analysis with NULL.
Assignment/implicit policies do not gain these explicit edges.
OID adds INT2/INT4/INT8→OID implicit, OID→INT4/INT8 assignment, OID identity
and explicit TEXT I/O edges; CAST/::/oid(...) reuse the same providers.
OID→INT2 is42846, including NULL. No unrelated INT2 cast family is admitted.
No new ordinary no-FROM SELECT root is admitted.
Baseline casts remain scalar leaves in existing comparisons, null/Boolean
predicates, Boolean expressions and selected null-count arguments. Selected null-count-only no-FROM projections
may use literal/parameter cast arguments; standalone casts still require FROM.
Scalar grouping also admits existing column operands, e.g. `(id)=1` and `(i)<(j)`.
Grouped predicates also serve as ordinary comparison operands, such as
`(id=1)=TRUE`. Ungrouped comparison chaining, predicate arithmetic and ordinary
logical/comparison INSERT/settings expressions remain excluded.
First standard/postfix parameter casts establish source type, later casts convert
it; int4/int8/bool unknown parameter calls are42725, text($1) infers TEXT.
Standard/postfix names preserve strong column/function names, otherwise target
names; function casts use callee names. Same-type column casts retain origin,
converted outputs do not. Unknown input errors position the quote; typed
conversion errors are unpositioned and expose no partial query cursor.
Standard/postfix unsupported pairs are42846, function pairs42883.
Multirow INSERT and cast-bearing Restore remain atomic; nested Restore parameters
are rejected at their token. Dump serializes canonical values.
Execution preflights immutable row/stage-independent casts, including imported
parameters, even on empty/fully filtered input without moving typed failures into
Prepare. Fresh private frames prevent parameter reuse across executions.
SELECT preflight precedes executor table ACL outside engine locks; namespace USAGE
analysis remains earlier and valid casts still require SELECT. Row-dependent
conversions remain row-owned; custom/generic plan-cache switching is not implemented.
Preflight traverses null/Boolean tests and both DISTINCT operands without
folding predicates or pruning conservative dependencies.
Equality preserves child cast errors and diagnoses operator
failures from bound operand types. Review smoke:
`make check RUN='^TestCastExplicitReviewSmoke$'`.
Smoke: `make check RUN='^TestCastExplicitSmoke$'`; details/matrix in README.
Parameter/ACL smoke: `make check RUN='^TestCastExplicitPreflightSmoke$'`.

BYTEA is a stored column type (OID17, typmod -1, no collation), with arbitrary
octets, NULL and nonnull empty values. SQL literals accept hex `\x` (either-case
digits; space/tab/LF/CR between pairs only) and double-backslash/three-octal
escape forms after normal SQL string decoding. String/[]byte binds to inferred
BYTEA are raw pq-compatible octets, not byteain text; typed nil []byte is NULL,
while nonnil empty []byte is empty. Native rows export fresh owned []byte,
DatabaseTypeName BYTEA and ScanType []byte; metadata survives EOF/Close and
catalog replacement. Length/DecimalSize are unavailable natively; pq Length is
MaxInt64 and nullability unknown. Comparison is unsigned bytewise prefix order.
Malformed hex is 22023; malformed escape is 22P02 with exact English primary
`invalid input syntax for type bytea`, eagerly character-positioned at literals.
Invalid hex digits report the complete offending UTF8 character, not a lead byte;
both nibble positions preserve the literal's character-based source location.
BYTEA modifiers fail42601; quoted lowercase "bytea" resolves, "BYTEA" does not.
The builtin bytea_output enum defaults hex and supports escape, with generic
settings lifecycle and statement-captured NOT NULL failing-row Detail. Raw
SELECT octets never change with this setting. Native nonnull empty bytea
always exports nonnil []byte; pinned pq's text escape decoder exports typed
nil []byte for empty bytea (hex exports nonnil empty). Both remain distinct
from SQL NULL's nil interface. Canonical Dump literals always
use quoted lowercase hex; Restore preserves settings, atomicity and old
snapshots. Raw BYTEA operations are `||`, `bit_length`, `octet_length`, `length`,
two-argument `btrim`/`ltrim`/`rtrim`, `overlay(bytea,bytea,int4[,int4])`,
`substring`/`substr(bytea,int4[,int4])`, and `pg_catalog.position(bytea,bytea)`.
SQL OVERLAY PLACING/FROM/FOR, POSITION IN, SUBSTRING FROM/FOR (including reversed
keywords and FOR-only), and TRIM BOTH/LEADING/TRAILING standard and comma forms
are supported. Ordinary POSITION takes haystack then needle and must be quoted
or qualified; unqualified `position(a,b)` is a syntax error. Positions/counts
index bytes, not Unicode. POSITION returns first one-based INT4, zero if absent,
and one for an empty needle. SUBSTRING clips the interval at byte one/end;
negative counts fail22011, while INT4 start+count overflow means through end.
OVERLAY requires positive start (22011), checks start+count overflow (22003),
defaults count to replacement octet length, permits negative counts, and appends
beyond end without padding. Both errors use PostgreSQL English messages and
have no fabricated query location. FOR-only SUBSTRING explicitly casts count to
INT4; ordinary and FROM+FOR calls require implicit INT4 compatibility.
Trim uses endpoint byte membership, not whitespace or Unicode; BYTEA unary
TRIM has no overload. All providers are strict after successful input analysis;
empty results remain nonnull empty. Known BYTEA operands contextualize unknown
byte arguments, with INT4 index/count slots and raw Go binds. Results retain
BYTEA/INT4 metadata even on empty rowsets. Scalar query/write/view/CHECK consumers
and native replay share the providers; BYTEA DEFAULT functions remain excluded.
Positional TEXT/two-argument POSIX substring and encoding-aware length remain truthful0A000 boundaries;
selected TEXT/BPCHAR length and search siblings are supported as documented above.
The excluded `length(bytea,name)` candidate also recognizes unknown source/encoding
arguments and existing implicit TEXT/VARCHAR/BPCHAR-to-NAME encoding conversions.
It is not executed; genuinely absent signatures such as `length(bytea,integer)`
retain 42883 with the PostgreSQL missing-function diagnostic.
CHECK compact/pretty `pg_get_constraintdef` and `pg_get_expr` preserve SQL-origin
OVERLAY/POSITION/SUBSTRING/TRIM syntax; ordinary calls retain identifier quoting
(including `"overlay"`). Concat retains `||` grouping. Native `conbin` emits
typed `Binary <name>` nodes and recursive children without evaluation, not
PostgreSQL nodeToString interchange. Captured carriers survive native replay.
Special BYTEA TRIM nodes also record `syntax TRIM`; ordinary Binary tags are unchanged.
No new typed BYTEA literals, direct byteacat, byteapos alias, binary
functions beyond the selected raw-byte families or wire server are added;
generic arrays retain their existing element authority. Binary slicing smoke:
`make check RUN='^TestReferenceBinarySlice'`; pinned independent reference:
`make check REFERENCE=1 RUN='^TestReferenceBinarySlice'`.
Selected slicing/search fixed scalar signatures accept matching `VARIADIC`
call spelling without adding variadic execution or arrays. Reserved TRIM/OVERLAY
continuation words retain their qualified-name and projection-label legality.
SQL-special spelling is display provenance, not a semantic distinction in
GROUP BY/HAVING or duplicate-label ORDER BY. POSITION searches in constant
space with linear progress and bounded preprocessing/comparison checkpoints.
Migrated smoke: `make check RUN='^TestReferenceByteaSmokeMigration$'`;
Docker-free native witnesses: `make check RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`;
pinned independent reference of the same definitions:
`make check REFERENCE=1 RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`.

### BYTEA output setting — change note

Explicit BYTEA→TEXT via `::text`, `CAST(... AS text)` and `text(...)` invokes
the existing output/input conversion. `bytea_output=hex` (default) emits a
literal `\x` prefix and lowercase hex; `escape` doubles backslashes and emits
three-digit octal for bytes 0–31 and 127–255. NULL remains NULL; empty bytes
produce `\x` or empty TEXT. Input always accepts both formats. Raw driver
BYTEA results remain owned `[]byte`, and inferred binds remain raw octets.
No implicit or reverse TEXT→BYTEA edge or charset decoding is introduced.
Assignment conversion and immutable output classification remain unchanged.
Held column/bound casts capture each execution's mode; constant-only folding
is not disabled. Ordinary users may SET this enum without setting grants.
Existing session isolation, transactional SET/LOCAL/savepoint, DEFAULT/RESET,
pool reuse and physical replacement rules apply. Invalid values fail22023 with
`Available values: escape, hex.` and do not publish a new setting.
Native Dump remains canonical hex in either mode; Reset/Restore retain live
settings, raw schema/data and already-owned raw/TEXT snapshots. The complete
legacy BYTEA smoke assertion union now lives in shared reference scenarios.
Run both BYTEA family commands above before review (each under 900 seconds).

### BYTEA checksums and hashes — change note

Exactly seven unary BYTEA signatures are admitted in existing scalar consumers:

| Call | Result |
| --- | --- |
| `crc32(bytea)` | INT8: IEEE CRC32, unsigned range 0..4294967295 |
| `crc32c(bytea)` | INT8: Castagnoli CRC32C, same unsigned range |
| `md5(bytea)` | TEXT: 32 lowercase hexadecimal characters |
| `sha224(bytea)`, `sha256(bytea)` | BYTEA: 28 / 32 raw digest bytes |
| `sha384(bytea)`, `sha512(bytea)` | BYTEA: 48 / 64 raw digest bytes |

Hash input is immutable raw octets: embedded NUL, high bytes and invalid UTF8
octets are valid. There is no string normalization, byteain re-decoding or
output-format hashing. Strict NULL yields typed NULL; empty input yields a
nonnull digest (CRC zero). Unknown CRC/SHA SQL literals, NULL and parameters
infer BYTEA; Go string/[]byte binds are raw bytes. Literal `'\x41'` hashes A,
but a bound `\x41` hashes its four literal bytes. Inference is occurrence-based:
an earlier SHA occurrence can fix a shared parameter as BYTEA for later MD5.

MD5 has independent TEXT and BYTEA signatures. Unknown literals, NULL and
parameters prefer TEXT; known BYTEA uses raw-octet hashing. TEXT input hashes
exact UTF8 bytes, with no normalization or BYTEA interpretation; its existing
input codec rejects NUL/invalid UTF8. Implicit BPCHAR-to-TEXT removes trailing
ASCII spaces, whereas TEXT/VARCHAR retain them. MD5-first establishes TEXT and
a later SHA call fails 42883 `function sha256(text) does not exist` at Position16
in `SELECT md5($1),sha256($1)`; SHA-first establishes BYTEA. There is no backward
inference or Go-kind-based signature selection. Native typed nil `[]byte` bound
to inferred TEXT remains empty TEXT (empty MD5); pq sends NULL. Ordinary nil
returns NULL on both.

`to_bin`, `to_oct` and `to_hex` have exactly INTEGER and BIGINT unary signatures,
returning strict immutable TEXT. Zero is `0`, positive output has no leading
zeroes, and negatives use full 32/64-bit unsigned two's-complement width with
no minus sign or radix prefix. Hex digits are lowercase. Width is SQL-type
directed, including domains, not selected from Go values. SMALLINT and unknown
(literal, NULL or parameter) are ambiguous: 42725, primary
`function to_hex(unknown) is not unique` (or the actual function/type), empty
Detail and Hint `Could not choose a best candidate function. You might need to
add explicit type casts.` Other incompatible known types fail 42883 with the
standard cast Hint. Diagnostics use character-based function positions; input
errors retain codec positions/stages. Fixed scalar VARIADIC is accepted; named
arguments are rejected without discarding their labels.

Selected width and symbolic TEXT MD5 survive DEFAULT/CHECK/views, immutable
generated columns and expression indexes, native Dump/Reset/Restore and replay
of actual emitted SQL on PostgreSQL. Failed writes/Restore publish no prefix.
Results have ordinary TEXT metadata (typmod-1, not a digest length modifier).
Feature smoke:
`make check RUN='^TestReferenceStringNumericBaseFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceStringNumericBaseFeatureSmoke$'`.

Quoted lowercase hash names resolve normally. Quoted uppercase/mixed-case names
retain their spelling and fail 42883 rather than selecting a lowercase routine.
For these admitted calls, child-analysis errors precede the 100-argument limit;
101 valid arguments fail 54023 at the function token with no Hint, even when
the quoted spelling would otherwise fail routine lookup. Direct Query and actual
Prepare independently cover all seven names and these diagnostic stages.

Ordinary/pg_catalog calls and matching fixed-signature VARIADIC decoration use
the existing namespace/signature authority; no arrays are added. Wrong
types/arity use 42883; absent explicit schema uses 3F000. Inferred malformed
BYTEA literals retain the existing 22023/22P02 English diagnostics and
character-based quote locations, eagerly at direct Query/Prepare analysis.
Valid raw bytes have no hash-data errors. Runtime cancellation stays owned by
the existing context/adapter boundary, without a fabricated source Position.
Public precanceled tests use DB calls and DB-owned prepared statements;
PostgreSQL's watcher on an already borrowed Conn can race a tiny query.

Computed result names/types and raw int64/string/[]byte representations remain
available on NULL-only/empty results and retained metadata. Native computed
Nullable is unknown. MD5 TEXT Length is MaxInt64 on both adapters, native
CRC/SHA Length is unavailable, and DecimalSize is unavailable for all results.
Fixed digest sizes do not add BYTEA typmods. bytea_output does not change
digest values. Returned SHA buffers never alias stored inputs, retained cursors
or other executions. CRC reads raw octets directly with immutable standard-library
tables and no scratch or copies. Cryptographic hashes use per-evaluation state
and fixed 4096-byte scratch. Both paths check cancellation during input and before
publication. No unsafe aliases, proportional temporary input copy, new dependency
or PostgreSQL/OpenSSL provider-policy emulation is introduced.

Queries, predicates, grouping/aggregates, conditionals, writes and UPDATE RETURNING,
named CHECKs and views reuse the binary carrier and staged publication.
Dump preserves stored raw inputs/digests and structural CHECK/view calls;
Reset/Restore rebinds destination descriptors without evaluating stored rows.
Parameter-free Restore INSERTs admit these seven hash names recursively,
including `sha256('abc')`. Nested parameters remain rejected with 42P02 at the
parameter token. Parameter/input/read/cancellation failures publish no replacement,
even after a valid staged prefix, and retained results stay detached.
BYTEA hash functions remain excluded from DEFAULT; TEXT MD5 is supported through
the string provider. No SHA1, pgcrypto, routine catalog/OID publication or new cast edges are implied.
The separately documented binary conversion family now supplies encode/decode.

Feature smoke:
`make check RUN='^TestReferenceBinaryHashSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceBinaryHash'`;
provider units: `make check RUN='^TestBinaryHash'`.


### Size parsing and formatting — change note

The selected signatures are `pg_size_bytes(text) -> bigint`,
`pg_size_pretty(bigint) -> text` and `pg_size_pretty(numeric) -> text`.
They are strict and immutable, using existing typed expression, catalog,
coercion, metadata and staged-write paths, not adapter-local conversions.

`pg_size_bytes` accepts decimal/scientific numbers and case-insensitive ASCII
units `bytes`, `B`, `kB`, `MB`, `GB`, `TB`, `PB` (or no suffix), with C ASCII
whitespace around the number/unit. Units scale by powers of 1024; exact scaling
precedes integral rounding, with ties away from zero. Thus `1 MB` is 1048576,
`.00048828125 kB` is 1, and `-.1kb` is -102. No float conversion, radix syntax,
digit separators, KiB/EB, or NaN/infinity size strings are accepted.
Invalid sizes use 22023 with original input; invalid units additionally retain
the trimmed-unit Detail and valid-units Hint. Signed overflow uses 22003
`bigint out of range`; excessive numeric format uses 22003
`value overflows numeric format`. Consumed malformed numeric exponents retain
22P02 and their extracted numeric spelling. Providers have no source Position;
eager child/input errors retain the existing analysis/portal diagnostics.

Pretty retains bytes below magnitude 10240, including `1024 bytes`.
Promotion uses signed truncation by 512 initially, then 1024, before one final
half-unit rounding away from zero. The next-unit threshold is 20479 in those
half-unit intermediates. INT64_MIN formats as `-8192 PB`; the final PB unit
never promotes to EB. Pretty is lossy: positive BIGINT max formats to `8192 PB`,
which overflows when parsed back. Numeric bytes preserve original fractional
scale (`10.5000 bytes`); larger values truncate at each promotion before the
final half rounding. Numeric remains exact beyond BIGINT; NaN and infinities
format as `NaN PB`, `Infinity PB`, and `-Infinity PB`.

BIGINT/NUMERIC (including existing domains) select their exact pretty overload.
SMALLINT, INTEGER and unknown literal/NULL/parameter inputs remain ambiguous
(42725), requiring an explicit cast. Bytes infers TEXT for unknowns and admits
existing implicit textual conversions; Go kinds never select SQL signatures.
Known wrong types/names/arity use 42883 with the standard English cast Hint,
quoted case and character-based function Position. Fixed scalar VARIADIC is
accepted; named arguments fail without losing their labels. Child errors
precede the 100-argument limit. Strictness does not hide invalid signatures.

Query/conditional/grouping/aggregate/write/default/CHECK/view/generated/index
consumers share the existing scalar carrier. Failed writes and Restore publish
no prefix. Native Dump/Reset/Restore reconstruct selected overloads with symbolic
types, including small BIGINT constants and typed numeric NULLs, and retain
existing detached rows/metadata. Compact/pretty deparse acceptance is semantic
replay, not incidental whitespace/parentheses; actual emitted Dump SQL must replay
on PostgreSQL. Stateless builtins need no separate serialization. No pg_proc
expansion, additional cast edge or physical-size counter implementation is
implied. Scalar operators are covered by [numeric arithmetic](#numeric-arithmetic--change-note).

COUNT and SUM with size-expression arguments and grouped size calls use the
existing aggregate path; SUM of BIGINT bytes produces exact NUMERIC results.
INSERT size expressions expose exact affected counts and subsequent
state reads; UPDATE RETURNING is supported, but INSERT RETURNING is not added.
Internal `"char"` input has no native implicit TEXT cast, so its bytes call
reports 42883 where PostgreSQL converts and parses it. These do not change the
three selected signatures or authorize adjacent aggregate/cast/write features.

Feature smoke:
`make check RUN='^TestReferenceSizeFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceSizeFeatureSmoke$'`.
Complete reference family: `RUN='^TestReferenceSize'` in both modes.

Integral bitwise operators `& | # ~ << >>` now use a separate SMALLINT/
INTEGER/BIGINT provider in existing general scalar consumers: queries,
predicates, grouped/aggregate expressions, writes, conditional expressions,
defaults and view/native replay. Mixed AND/OR/XOR widen; NOT and shifts
preserve left width. Shift counts require INTEGER. Typed NULL and computed
INT2/INT4/INT8 metadata survive empty results and replay. Unsupported signatures
report42883 and ambiguous unknown signatures42725, with English messages and
operator positions. See [width/count behavior](TYPES.md#integral-bitwise-operators).
Catalog-qualified OPERATOR(pg_catalog.symbol) forms retain these same bitwise
providers and ordinary bounded cast rules; wrong namespaces do not bypass lookup, and
canonical defaults/views preserve explicit qualification.
No BIT/VARBIT provider or no-FROM widening is added.
Standard SMALLINT CAST/`::` now uses the ordinary existing directional numeric
coercion edges, including standalone and conditional occurrences. Known TEXT
input conversions and `int2()` function-style casts remain excluded.
Smoke: `make check RUN='^TestBitwiseSmoke$'`.

INT2/INT4/INT8 arithmetic uses the common expression binder in existing scalar
consumers. Operators `+ - * / %`, unary signs and parentheses follow ordinary
arithmetic precedence. Unary and same-width binary operations retain width;
mixed `+ - * /` select the wider integer result while retaining each input's
declared width. Mixed `%` widens to the wider homogeneous modulo signature.
Width is never selected from destination, result magnitude or Go argument kind.
Division truncates toward zero; remainder follows the dividend sign, and minimum
integer remainder -1 is zero. Overflow is checked (`22003`: `smallint out of
range`, `integer out of range`, or `bigint out of range`); zero division/remainder
is `22012` (`division by zero`). Runtime diagnostics have no invented position.
NULL results retain result width. Signed numeric literals fold under minus, including grouping.
`i% -1` and `i%(-1)` are remainder with a negative divisor; adjacent `%-`
is a combined symbolic operator and fails typed `42883`, not a successful
remainder. Arithmetic punctuation maximal munch preserves comment boundaries
and PostgreSQL trailing-sign stripping. Contextual errors use the expression
start/resolved type, while missing-operator errors point at the operator.
Computed results expose `?column?` and INT2/INT4/INT8 metadata, including empty
and NULL results, with native int64 ScanType and no physical origin.
Parameter widths follow SQL signature resolution, never Go argument kinds.
Unary unknown plus and unknown power select FLOAT8; unknown-only binary +/−/*
and unary minus are ambiguous (`42725`). Exact NUMERIC operators use the shared
typed seam described below; floating `%` is still missing (`42883`).
Existing baseline scalar casts compose with arithmetic without adding cast
families. Postfix `::` binds before unary signs; operand casts can widen arithmetic,
but result casts cannot prevent narrower overflow. Frame-only cast conversions
are preflighted recursively beneath arithmetic, including on empty/filtered
input; arithmetic roots still evaluate with rows. Computed arithmetic hidden
under casts remains excluded from bounded null-count arguments. Standard/function
cast labels and standalone identity-cast origins retain their existing contracts;
arithmetic results retain computed metadata and no physical origin.
Sequence calls retain their maximum child volatility through arithmetic and
nonidentity casts. Empty or filtered projections do not allocate or read session
sequence memory; imported argument errors still precede demanded effects.
Composition smoke: `make check RUN='^TestSequenceArithmeticCompositionSmoke$'`.
Arithmetic INSERT and parameter-free Restore use existing staged publication;
Dump emits canonical stored values, and failed/canceled replacement preserves
old state and retained computed cursors. No external pg_dump, physical storage
or wire-protocol parity is implied. Smoke: `make check RUN='^TestReferenceArithmeticMigratedSmoke$'`;
reference: `make check REFERENCE=1 RUN='^TestReferenceArithmetic'`.
Smallint feature smoke: `make check RUN='^(TestReferenceSmallintArithmeticSmoke|TestReferenceSmallintMigratedSmoke)$'`;
run the same definitions with `REFERENCE=1` for the pinned PostgreSQL oracle.
Owned defaults/views preserve literal widths across native replay, including
unused overflowing INT2 defaults. Cancellation seam tests prove evaluation/
staging-to-publication fences, not interruption of an in-flight public Restore.

DELETE arithmetic keeps existing actual-used-column SELECT authorization,
affected-row counts and atomic deletion staging. Qualified arithmetic remains
excluded without changing qualified atom predicates. Mixed numeric and temporal
projections preserve the existing codecs; selected temporal operators are
documented above. Smoke: `make check RUN='^TestReferenceArithmeticMigratedDeleteSmoke$'`.

### Floating arithmetic — change note

Unary signs preserve REAL/FLOAT4 or DOUBLE PRECISION/FLOAT8. For each of
`+ - * /`, REAL/REAL yields REAL; DOUBLE/DOUBLE and mixed REAL/DOUBLE yield
DOUBLE. REAL operations execute at binary32 width before export. A known
SMALLINT/INTEGER/BIGINT/NUMERIC peer selects a cross-width DOUBLE operation;
an unknown quoted value or parameter with REAL selects REAL/REAL instead.
Prepared signatures and computed metadata remain fixed across bindings,
including empty/NULL results; destination conversion cannot rescue REAL overflow.
Native REAL exports exact widened binary32 values as float64; pq parses the
server's shortest REAL text as float64 and advertises float32 ScanType.

Power selects DOUBLE/DOUBLE, including integer-only inputs and REAL with
NUMERIC. NUMERIC-only power uses [exact decimal arithmetic](#numeric-arithmetic--change-note),
not approximate floating arithmetic. `^` associates left and binds
above multiplication/division but below unary signs: `2^3^2=64`,
`2^(3^2)=512`, `-2^2=4`. Qualified `OPERATOR(pg_catalog.^)` retains generic
operator precedence. Adjacent `^-` is a single unavailable operator.

Finite overflow/zero-rounded underflow report `22003` with
`value out of range: overflow` / `value out of range: underflow`.
Addition/subtraction cancellation and nonzero subnormals succeed.
Division by either signed zero is `22012`, except NaN/zero remains NaN.
Infinity combinations preserve IEEE NaN/infinity and signed zero.
Power handles NaN before domain checks (`NaN^0=1`, `1^NaN=1`).
Zero to a negative power is `2201F`, `zero raised to a negative power is undefined`;
negative/noninteger power is `2201F`,
`a negative number raised to a non-integer power yields a complex result`.
Runtime failures have no fabricated Position. Ordinary power accuracy permits
explicit small ULP tolerances against the pinned host library; diagnostics and
success/error classification are exact, never tolerance-based.

There is no floating modulo overload: even NULL/empty-source `%` fails at
analysis/Prepare with `42883`, full signature, cast Hint and operator Position.
Floating DEFAULT/CHECK/view replay preserves each typed input leaf and power
grouping. Query finalization, DML Finish and isolated Restore installation
retain existing atomic failure/cancellation and held-snapshot contracts.
The floating cutover introduces no new power() function, general no-FROM SELECT,
types, cast families or storage mechanisms. Exact NUMERIC is covered separately below.
Smoke: `make check RUN='^TestReferenceFloatingArithmetic(Smoke|ControlsSmoke)$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceFloatingArithmetic'`.

### Monetary operators and casts — change note

MONEY uses static PostgreSQL scalar signatures: MONEY+MONEY and MONEY-MONEY
return MONEY; MONEY multiplied by SMALLINT/INTEGER/BIGINT/REAL/FLOAT8, in
either order, returns MONEY. MONEY divided by those five scalar widths returns
MONEY; MONEY/MONEY returns DOUBLE PRECISION. Integer division truncates raw
minor units toward zero without a float intermediate (`±878.08/11` → `±$79.82`).
Float multiplication/division converts raw units to binary double and rounds
nearest-even (`878.08/11::float8` → `$79.83`). REAL retains binary32 operand
precision. MONEY/MONEY converts each raw operand to FLOAT8 before division,
including binary precision loss for large units; `123::money/2::money` is61.5.

Unknown divisor `'2'` selects MONEY/MONEY, unlike integer2. NUMERIC scalar
peers select the implicit FLOAT8 route, not a new exact monetary overload.
Unary ±MONEY, MONEY*MONEY, integer/MONEY, monetary remainder/power and
MONEY+integer are absent42883 operators. Strict NULL retains the resolved
MONEY or FLOAT8 result identity. Raw comparisons, ordering, grouping and the
owned default money_ops B-tree class share exact signed-unit comparison;
no MONEY hash class or aggregate family is added.

Zero divisors fail22012 `division by zero`. Checked monetary overflow/NaN/
nonrepresentable float results fail22003 `money out of range`; floating
intermediate overflow/underflow retain ordinary FLOAT8 range messages.
INTEGER/BIGINT→MONEY and NUMERIC↔MONEY are exactly four assignment/function
pg_cast facts (functions3811/3812/3824/3823). Cast scaling/rounding stays exact;
cast overflow is22003 `bigint out of range`, not the monetary operation error.
SMALLINT and direct float casts to MONEY, and MONEY outgoing integer/float
casts, fail42846. Generic textual I/O adds no fictitious pg_cast facts.

Monetary I/O and all four monetary conversions are STABLE and consume the
captured statement policy; raw arithmetic/comparison is IMMUTABLE. Existing
C/POSIX/empty-as-C support is unchanged, including to_char's currency **space**,
which is distinct from MONEY's dollar fallback. No libc/ICU locale loading,
cash_words, array execution or general function-style cast widening is added.

Safety exception: native guards minimum signed raw units divided by -1 at
SMALLINT/INTEGER/BIGINT provider boundaries and returns22003 `money out of
range`. PostgreSQL18.0 REL_18_0 cash_div_int64 contains an unguarded division;
we do not issue that dangerous SQL in the shared oracle fixture or claim an
observed crash. Genuine provider units cover the native exception.
Smoke: `make check RUN='^TestReferenceMoneyFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceMoneyFeatureSmoke$'`.

### Numeric arithmetic — change note

NUMERIC `+`, unary `+`, `-`, unary `-`, `*`, `/`, `%` and `^` use the
same typed operator resolution, immutable decimal payload and row evaluator as
existing consumers. Integer peers widen to NUMERIC; mixed REAL/NUMERIC retains
DOUBLE PRECISION resolution. Computation never routes NUMERIC-only results
through binary FLOAT8.

Addition/subtraction retain the maximum operand display scale and exact
coefficients. Unary signs preserve scale and canonical positive zero.
Multiplication computes the full product at the combined scale, then rounds
half away from zero at the stored scale cap16383. Division selects at least
16 significant digits from normalized base10000 leading digits, retains operand
scale where possible, caps calculated scale at1000 and rounds half away from
zero. `1.0/1.0` therefore has20 fractional places while `5.0/2` has16.
Remainder is exact `a-trunc(a/b)*b`, follows the dividend sign and retains
maximum scale; it shares the selected SQL `mod` implementation.

Power remains left-associative: typed `2::numeric^3::numeric^2::numeric` is
`64.0000000000000000`; explicit right grouping gives512. Raw integer2^3^2
still selects FLOAT8=64. Integral int32 exponents use guarded decimal squaring;
fractional and larger integral exponents use private decimal ln/sqrt and the
same explicit-scale exponential kernel as SQL exp. Result scale is selected
from PostgreSQL's precision schedule, capped at1000; this does not add SQL
power(), ln() or sqrt() functions. Huge exponent parity is read from exact
decimal digits, not floating conversion. Irrational outputs are rounded decimal
results at the selected precision, not infinitely exact real numbers.

NaN propagates, except NaN^0 and1^NaN are canonical1. Opposite infinities added,
equal infinities subtracted and infinity multiplied by zero produce NaN.
Finite/infinity division is canonical0; finite%infinity retains the finite
input scale. NaN/0 andNaN%0 are NaN; other zero divisors fail22012
`division by zero`. Zero to a negative exponent fails2201F
`zero raised to a negative power is undefined`; negative bases to finite
nonintegral exponents fail2201F
`a negative number raised to a non-integer power yields a complex result`.
Strict typed NULL does not evaluate provider payloads but does not suppress
errors from eagerly evaluated children.

Each operator validates its packed result, including intermediate results before
later cancellation. Physical numeric overflow and power's general-path range
guard fail22003 `value overflows numeric format`; very small calculated values
round to scale1000 zero rather than raising floating underflow. Destination
typmod sizing is separate and retains its numeric-field-overflow Detail.

Computed metadata is NUMERIC/OID1700/typmod−1, native string ScanType,
unknown DecimalSize/nullability and no physical origin, including empty/NULL
results and constrained inputs. Parameters obtain Numeric slots from SQL
resolution, not Go argument kinds. Held string/bytes/int64/NULL inputs retain
fresh scales. Existing DEFAULT/CHECK/view capture preserves typed leaves and
power grouping; deparsed expressions and native Dump/Restore replay actual
numeric behavior. Query finalization, statement Finish and isolated catalog
installation preserve atomic failure/cancellation and held snapshots.
Cancellation is checked between convergence/squaring stages and publication
fences, not claimed inside an indivisible big.Int primitive.

Smoke: `make check RUN='^TestReferenceNumericArithmeticFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceNumericArithmeticFeatureSmoke$'`.



Character declarations add CHAR/CHARACTER/BPCHAR(n) and VARCHAR/CHARACTER
VARYING(n), length1..10485760 counted in UTF8 code points. Unmodified
CHAR/CHARACTER defaults1; BPCHAR/VARCHAR is unlimited. BPCHAR pads constrained
values with ASCII spaces; all string types preserve stored/output tails.
Assignment truncates only ASCII-space excess or fails22001; string-family
CAST/:: silently clips arbitrary excess and keeps typed NULL metadata.
BPCHAR->TEXT/VARCHAR removes ASCII tails before sizing; same-ID BPCHAR casts
preserve physical bytes before resizing. The separate baseline cast matrix above
is retained; it adds no integer/Boolean explicit edges to BPCHAR or VARCHAR.
Existing assignment output conversion is not implicit.
Character casts infer only unresolved parameters from the target; known character
occurrences convert from their fixed source identity. Earlier unknown occurrences
and mixed INSERT inference boundaries are not rewritten. Diagnostic spelling is
`character`/`character varying`, distinct from catalog/metadata/Dump spelling.
Ordinary INSERT assignment failures use the resolved cast's descriptor rather
than its SQL alias spelling, matching INSERT SELECT assignment diagnostics.
Assigning BPCHAR/CHAR or VARCHAR casts to an INT column fails with `42804`,
source type `character` or `character varying`, the rewrite-or-cast Hint and the
source expression's character position. Direct execution and preparation reject
the assignment before changing destination rows; this does not add cast edges.
Ordered INSERT source VALUES classifies BPCHAR/VARCHAR with the existing string
family. Its row-order common-type rules and implicit conversions remain in use;
BPCHAR-to-TEXT trims ASCII tails, VARCHAR-to-TEXT preserves them. Target sizing
and atomic publication are unchanged.
Common source typmods require matching type identities as well as matching
modifiers; differing CHAR/VARCHAR identities use unconstrained common sizing
even when both declare length 2. In an unlimited BPCHAR destination, a
CHAR(2)-first mixed source preserves its original pad without padding the
VARCHAR row. A VARCHAR(2)-first source trims the CHAR row's ASCII pad.

Character-family comparisons reuse the existing target/WHERE comparison and
logical surface, with column/literal/NULL/parameter/cast operands on either side.
BPCHAR/BPCHAR and BPCHAR/VARCHAR ignore ASCII tails, BPCHAR/TEXT trims only
BPCHAR, TEXT/VARCHAR preserve significance; unknown literals beside BPCHAR use
BPCHAR semantics. ORDER BY uses UTF8/C byte comparison, without general
locale/collation claims. No general functions or no-FROM query expansion are added.
Column output labels survive nested casts; casts of unknown strings/NULL/
parameters instead use the outermost resolved type's catalog name.
An identical-Usage cast of a column retains native physical origin/nullability
proof; typmod changes or retyping produce an expression with no origin and
unknown nullability, also through later identity casts of that expression.

BPCHAR/VARCHAR/TEXT native metadata has string ScanType and Length n or
MaxInt64, retained for NULL/empty/closed/old-snapshot results. lib/pq's BPCHAR
interface{} ScanType and unlimited BPCHAR/VARCHAR Length -5 are independent
adapter contracts, not native behavior. String NUL/malformedUTF8 uses22021
with English byte diagnostics and actual parameter Context on import failures.
Both native and PostgreSQL CREATE utility Prepare accept invalid length/generic/
TEXT modifiers; direct/prepared Execute reports the positioned semantic error.
Keyword modifier syntax errors fail Prepare on both.
Generic BPCHAR/quoted character modifiers admit simple identifiers, decoded
through the existing integer input path; keyword lengths remain unsigned-only.
TEXT's modifier prohibition wins before modifier expression/input errors.
Native controls preserve constrained/unlimited declarations, physical
padding/tails/Unicode/escapes/NULL and retained readers, with atomic failed
Restore. Internal `"char"` remains a distinct unsupported identity; NAME has
the separate bounded value contract below, not character-family typmods/casts.
Smoke: `make check RUN='^TestCharacterSmoke$'`; permanent pinned reference:
`make check REFERENCE=1 RUN='^TestReferenceCharacter'`.

NAME columns reuse builtin OID19 with typmod -1 and C collation950. Textual
input preserves case and whitespace and silently clips to63 bytes at a complete
UTF8-code-point boundary, not63 characters; it does not apply identifier folding.
Full input validation precedes clipping, so malformed UTF8/NUL in a discarded
suffix still fails22021 with the English hexadecimal-byte diagnostic. Ordinary
string/[]byte/Valuer binds are textual, nil is typed NULL, and native typed nil
[]byte retains the existing non-BYTEA empty-text convention. NAME output contains
only visible spelling, not padding. Native metadata is NAME/string with no
Length/DecimalSize; pq instead exposes NAME/interface{} and []byte values.
Native column origin/nullability and NULL/empty metadata remain intact.

NAME/NAME and NAME/TEXT in either direction use C byte ordering with case/tails
significant. Known TEXT is compared without clipping; unknown quoted operands
and NAME-inferred binds clip first. TEXT/VARCHAR-to-NAME is implicit; BPCHAR
conversion clips before removing ASCII tails. NAME-to-TEXT preserves tails
implicitly; NAME-to-VARCHAR/BPCHAR assignment uses existing target sizing.
Other supported scalar assignment uses output/input: SQL TRUE/FALSE stores t/f,
while bound Go bool stores true/false. NAME-to-integer assignment and NAME/BOOL
comparison retain42804/42883 and existing hints/positions. NAME modifiers reach
CREATE utility Execute's positioned42601 prohibition before operand conversion;
Prepare remains metadata-only.

Native Dump/Restore preserves NAME schemas/data/typed NULL/empty tables and old
cursors, with deterministic redumps and no replacement on failed staging.
Explicit NAME casts/typed literals/functions, arrays, internal `"char"`,
arbitrary locale COLLATE, binary wire serving and PostgreSQL physical storage are not added.
PostgreSQL binary namerecv rejects64-byte input rather than clipping; native
byte binds do not implement that protocol. See [NAME values](TYPES.md#name).
Feature smoke: `make check RUN='^TestNameSmoke$'`;
pinned reference: `make check REFERENCE=1 RUN='^TestReferenceName'`.



UUID declarations use contextual quoted values or binds, not UUID typed literals.
UUID accepts either hex case, matching braces, omitted hyphens and single hyphens
at internal four-hex-digit boundaries; no whitespace/URN prefix/version validation.
Stored values are inline sixteen bytes (OID2950), output is canonical lowercase
8-4-4-4-12, ordering unsigned bytewise, with ordinary strict NULL equality and
ASC NULLS LAST/DESC NULLS FIRST. Native values/ScanType are string/string; pq
uses canonical owned []byte/interface{}. UUID metadata persists for empty/NULL
results; Length/DecimalSize are unavailable. Malformed text is 22P02 with exact
original-input primary; eager literal errors point to the quote in characters,
typed bind input errors have no Position and retain unnamed portal parameter Context.
The existing native Dump/Restore serializes UUID declarations/canonical quoted
values atomically and preserves retained snapshots. Smoke: `make check RUN='^TestUUIDSmoke$'`.
Generators, version/timestamp extractors, arrays/casts/typed UUID literals and
binary wire input remain excluded. PostgreSQL physical disk/network storage,
protocol serving and external pg_dump process/custom archives are non-engine
boundaries, never generic successful no-ops.

SMALLINT/INT2 has signed-16 bounds (-32768 through 32767), native int64 payloads and INT2 result metadata, including NULL/empty results. Quoted lowercase `"int2"` resolves; quoted `"smallint"`/`"INT2"` do not. INTEGER/BIGINT keep their current storage and signed-32/signed-64 bounds. Shared textual input accepts C whitespace, signs, radix prefixes and single separators; malformed/range errors are 22P02/22003 with original value and English smallint/integer/bigint names. Typed narrowing reports `smallint out of range`, unlike input's `value "32768" is out of range for type smallint`. SQL integer inference remains INT4/INT8; cross-width comparisons do not assignment-narrow. Checked imports/coercions precede publication, and staged Dump/Restore preserves all widths and retained cursors. Feature smoke: `make check RUN='^TestSmallintSmoke$'`; independent pinned reference: `make check REFERENCE=1 RUN='^TestReferenceSmallint'`.

SELECT execution uses one eager finite source/filter/sort/projection pipeline and common typed row result with identity/settings/SHOW producers; no second binder/catalog/storage path or added syntax. Every admitted target is finalized before statement completion, rejected rows do not evaluate targets, and later evaluation/sort failure exposes no cursor/fragment. Result schema remains available after EOF/error/Close, including empty relations; zero-column rows retain input multiplicity. Direct-column results retain immutable relation-local captured prefixes/indices; computed results retain projected values only. EOF/error release resources automatically; raw cursor errors (including ordinary context cancellation) stay sticky until idempotent Close, after which Next is EOF. Saved exported values survive cleanup and append/Reset/Restore; mutable timestamp-infinity bytes detach per export. WHERE keeps only TRUE; unordered results and sort peers have no portable sequence guarantee. Default ASC NULLS LAST/DESC NULLS FIRST remain without admitting NULLS syntax. Missing ORDER keys preserve source character Position in direct/prepared binding on empty or populated input. Feature smoke: `make check RUN='^TestQueryCoreSmoke$'`; independent pinned reference: `make check REFERENCE=1 RUN='^TestReferenceQueryCore'`.

Analyzed SELECT/INSERT missing-relation errors retain the first relation identifier's one-based character Position (for example, `SELECT id FROM t` gives42P01/`relation "t" does not exist`/16, or22 with an intervening `/*é*/ ` comment). The same normal analysis wrapper handles pre-lock and binder lookup; copied diagnostics preserve every other field and utility COMMENT/ALTER/LOCK/DROP records remain independently owned. Shared malformed-input witnesses use `SELECT id FROM t WHERE` →42601/`syntax error at end of input`/23 rather than treating PostgreSQL's legal empty-target SELECT prefix as shared syntax.

DATE (OID1082) stores civil days with proleptic Gregorian bounds 4714-11-24 BC
through 5874897-12-31, plus infinities. Native output/ScanType is string, including
expanded years, BC and infinities; typed NULL remains DATE/nil. pq exports finite
time.Time and default infinity []byte, with time.Time ScanType. DATE has no
precision: modifiers fail42601 at the type name, during CREATE execution or
typed-literal analysis, before integer conversion or unary-plus validation of
parsed modifiers (`date(3.0)`, `date(2147483648)`, `date(+3)`). Parser syntax
errors still precede type resolution; other types' modifier rules are unchanged.
Incompatible typed DATE assignments fail42804 during analysis/Prepare and name
the source `date`, not `date without time zone`; TIME/TIMESTAMP retain their
`without time zone` diagnostic names. Failed assignments preserve stored rows.
`date` remains an ordinary identifier; `"date"` is legal.
DATE modifier literals retain raw numeric spellings on a parser-private,
per-call continuation distinct from ordinary cast operands; no DATE keyword
or function-style DATE cast is introduced. Baseline int4/int8/text/bool calls
remain unchanged; selected standard/postfix datetime casts are supported.
ISO/month-name/compact/ordinal/Julian inputs, BC, epoch, DateStyle ordering and relative
now/today/yesterday/tomorrow reuse the shared civil decoder. Validated clocks/zones
never shift or carry the date; time.Time binds preserve their location's Y/M/D,
including Go year0 as1BC. The date domain is independent of TIMESTAMP's range.
Successful Go imports ignore clock/zone fields without validating or converting
them. On a civil-range failure only, the22008 primary includes the failed
temporal spelling, e.g. `date out of range: "5874898-01-01 00:00:00Z"`;
fractional seconds, numeric offset seconds and trailing ` BC` are preserved.
AD/BC numeric years beyond INT32 fail with the earlier `date/time field value
out of range` primary, before final DATE-domain range validation.
Pinned pq's BC time.Time encoder uses AddDate and normalizes year0 February29
to March1; native DATE preserves that leap day. Textual BC input avoids this
adapter limitation; independent reference expectations distinguish the bind paths.
Prepared typed/contextual constants freeze; bound values use execution-owned
transaction-start session-local date. Malformed22007, calendar/range22008 and zone failures
retain English messages, original input and character-positioned literal errors;
typed parameter input errors retain unnamed portal Context, without Position. Comparisons order civil days with
infinities and default NULL order. Dump/Restore quotes canonical values and
preserves snapshots/atomicity. Captured database/sql ColumnType values remain
usable after EOF/Close; database/sql itself rejects new ColumnTypes calls on
closed Rows. Native DATE/TIME comparisons reject42883. DATE/TIMESTAMP
comparisons are supported with the same PostgreSQL semantics, including midnight
equality, civil-day ordering, infinities and SQL NULL propagation.
Selected datetime casts and TimeZone/DateStyle are supported as documented in
[temporal types](TYPES.md#time-zone-aware-values); selected [temporal arithmetic](#date-and-time-arithmetic--change-note) is supported. CURRENT_DATE is supported as a bounded SQL value function. Ordinary no-FROM SELECT uses the shared query path. Smoke:
`make check RUN='^TestDateSmoke$'`; independent pinned reference:
`make check REFERENCE=1 RUN='^TestReferenceDate'`.

TIME has OID1083 and canonical string output/ScanType, preserving distinct `24:00:00` (pq instead anchors time.Time to Jan1/Jan2 year0). Precision0–6 uses fractional ties-to-even microseconds then half-up sizing; above6 warns22023/clamps, keyword negative precision is42601, quoted `"time"(-1)` is22023. Colon/compact clocks, AMPM, limited optional validated dates/Julian/T/noise, numeric/abbreviation/full-zone inputs and now/allballs are supported. Zones never shift the written TIME clock; historically varying full names require a date, fixed names do not. Rules are embedded from the pinned image's IANA2025b/backzone data without runtime zone I/O. Constants freeze at analysis; parameters use session-local execution transaction start. TIMETZ, selected casts and session zone settings extend these same providers. Feature smoke: `make check RUN='^TestTimeSmoke$'`; real reference: `make check REFERENCE=1 RUN='^TestReferenceTime.*$'`.

TIME ignores recognized `on`/`at` noise, but rejects standalone weekday tokens (`04:05 on Monday` is22007). Unknown full-zone names produce22023 with a lowercased token in the primary diagnostic (`America/Bogus` becomes `america/bogus`); the original input remains intact in malformed-input diagnostics.

TIME colon parsing checks lexical/component bounds first, then processes remaining tokens and validates the optional date, AM/PM and final clock domain in that order; TIMESTAMP retains its early clock-domain check. Typed assignment errors use the actual temporal source identity. Selected temporal casts follow PostgreSQL's implicit/assignment/explicit policies.

NUMERIC is exact immutable decimal, exported as string with fixed-decimal output and retained display scale (typed NULL is nil). Precision 1–1000 and independently signed scale -1000–1000 include negative/wide scale; omitted scale is zero, unconstrained scale is retained. Sizing rounds ties away from zero before precision overflow; NaN fits constrained types, infinities require unconstrained numeric. Ordering is -Infinity < finite < Infinity < NaN; NaN equals NaN, finite equality ignores display scale, and integer/numeric comparison is exact beyond 2^53. Integer assignment rounds/checks numeric; checked numeric/float conversions preserve binary semantics, numeric-to-TEXT uses canonical output. DatabaseTypeName NUMERIC/ScanType string and DecimalSize reflect declarations including negative scale, never values; unconstrained sizing is unknown. Numeric aliases are unquoted only except catalog `"numeric"`.

Input syntax is validated during INSERT/SELECT Prepare at unconstrained typmod; destination sizing is Execute-owned, with overflow Detail and no Position. Literal input failures are character-positioned; typed bind input errors have actual unnamed portal parameter Context without Position. Later expression conversion/sizing failures do not acquire parameter Context. Same-type repeated numeric parameters can size separately per destination. Dump/Restore quotes exact canonical numeric values, preserves scale/specials/NULL/metadata, rejects failed staged replacements atomically and retains old cursor ownership. Format bounds are 131072 integer digits/16383 fractional places; excessive scale rejects rather than truncating to zero. Fraction/exponent SQL tokens do not broaden SET integer syntax or its separate quoted setting codecs. General casts/arithmetic/functions/serial and SELECT without FROM remain excluded; existing SMALLINT participates in exact numeric comparison and checked integer assignment. Feature smoke: `make check RUN='^TestNumericSmoke$'`; pinned reference: `make check REFERENCE=1 RUN='^TestReferenceNumeric.*$'`. Native negative-scale DecimalSize is independently asserted rather than matching lib/pq's unsigned-scale adapter defect.

For NUMERIC column `v`, `SELECT $1 FROM n WHERE v=$1` fails Prepare with `42P08`, primary `inconsistent types deduced for parameter $1`, Detail `numeric versus text`, Position `8`; direct Query with supplied arguments reports the same record at execution. Zero-argument native Query still reports that `42P08` record, while lib/pq's PostgreSQL simple query instead reports `42P02`, `there is no parameter $1`, Position `8`. The different complete records are asserted independently, without false parity.

CREATE TABLE uses ordered explicit columns and LIKE elements, including multiple/interleaved table, existing-view or composite-type sources and zero-column `()`. LIKE always copies declaration shape/NOT NULL, never source rows/owner/ACL or automatic parent linkage; qualified/effective-path lookup requires source namespace USAGE, then validates kind before relation SELECT for table/view sources or type USAGE for composite sources. Composite backing relations remain nonstorage authorities; absent descriptors make their options effect-free. Ordered INCLUDING/EXCLUDING options have last-option precedence, including ALL. Real defaults/CHECKs/STORED generation/identity/indexes/column comments copy through their owning descriptors with fresh identities and row remapping; explicit external constants/dependencies remain external. Absent compression/storage/extended-statistics and other unimplemented providers are not fabricated. IF NOT EXISTS skips an existing authorized destination before semantic element analysis, including nested CREATE TABLE in CREATE SCHEMA; malformed syntax still fails before effects. Nested elements use the enclosing owner and local staged schema path, never change session identity/settings, and publish atomically. Outer CREATE SCHEMA IF NOT EXISTS still cannot contain elements.

INCLUDING INDEXES and ALL also copy admitted partial B-tree and partial UNIQUE
definitions. Both analyzed and execution predicates remap destination row
attributes without redirecting external REGCLASS/REGTYPE constants. Analyzed
dependencies remain held even if execution folds their arm away. Fresh roots
contain no source rows; only TRUE qualifies, before expression-key evaluation.
INCLUDING INDEXES/ALL copies real PRIMARY KEY kind and fresh backing identities;
plain LIKE copies only its NOT NULL shape. Explicit plus copied PK, or two copied
PKs, fail42P16. Copied PK and explicit UNIQUE stay independent even on equal keys.
Actual key providers and descriptor comments copy through the existing source
capture; SQL COMMENT ON INDEX remains a PostgreSQL-only capability.
Partial HASH and partial concurrent creation retain their existing exclusions.

TEMP LIKE composition preserves physical-session authority. Same-session copied
TEMP sequence defaults retain original linkage across native Reset/Restore;
constant and permanent-sequence defaults from TEMP sources also copy into
permanent destinations. Capturing a TEMP sequence in a permanent copied DEFAULT
retains native Execute0A000 `temporary sequence DEFAULT is unsupported`, with no
Position from the source SQL, while PostgreSQL permits the dependency. Excluding
DEFAULTS avoids this narrow boundary. IDENTITY instead allocates a fresh owned
sequence in destination namespace/persistence, including TEMP destinations.
Predicate-only index edges remain outside the bounded native `pg_depend`
projection; exact descriptor manifests and real dependency DROP/controls remain authoritative.

NOT NULL is an identity-bearing relation constraint, separate from CHECK.
Ordered column `[CONSTRAINT name] NULL/NOT NULL` declarations and table
`[CONSTRAINT name] NOT NULL column` are supported; compatible redundancies merge,
named NULL creates no identity. Column-local NULL/NOT NULL conflicts are42601
at the conflicting token; differently named column duplicates are18.0XX000,
validated before table-level merging even if a table constraint comes first.
Explicit column NULL plus table NOT NULL is valid in either order. Only NOT
NULL identities merge; different names at that stage are42601. CREATE table
constraints naming system columns fail0A000 `cannot add not-null constraint on
system column "ctid"` for ctid, as does selected ALTER ADD. Duplicate explicit
names on different attributes
are42710. Generated UTF8-safe63-byte `table_column_not_null` names consult all
destination namespace constraints, including staged nested tables and prior
current-command names, never future explicit names. Allocation follows original
column/table/LIKE element order after same-column merging. A later explicit name
colliding with an earlier generated name fails23505 for
`pg_constraint_conrelid_contypid_conname_index`; Schema pg_catalog, Table
pg_constraint and that Constraint are reported, with exact Detail
`Key (conrelid, contypid, conname)=(OID, 0, name) already exists.` using the real
locally allocated candidate relation OID. CREATE publishes no table; reversing
the declarations succeeds with a generated suffix. Explicit names remain
relation-local. LIKE copies identities in source constraint-name order at its
element position onto fresh attributes, never hierarchy or source dependencies.

Selected ALTER TABLE actions are ALTER [COLUMN] c SET/DROP NOT NULL,
ALTER [COLUMN] c [SET DATA] TYPE type [COLLATE name] [USING expression],
ADD [CONSTRAINT n] NOT NULL c, and DROP CONSTRAINT [IF EXISTS] n [RESTRICT|CASCADE].
They share t/t*/ONLY t/ONLY(t), OwnerOnly AX and metadata-only effect-free Prepare.
Execution captures the admitted command's lock_timeout policy; blocked utilities
fail55P03 without Position or catalog effects, even for semantic no-ops.
Prepare does not resolve or wait for these utility targets. In contrast, a
subsequent absence probe `SELECT * FROM t` is analyzed on direct Exec/Query or
semantic Prepare and returns42P01 at character15 on both native and PostgreSQL;
this SELECT record must not inherit the utility's unpositioned-error policy.
SET/ADD builds the final descriptor before one NULL scan in physical attribute
order. DROP actions run first, TYPE actions next, original SET actions next,
transformed ADD NOT NULL actions last (within each pass, original order). A later action error
precedes validation and installs no descriptor, dependency, generator or row
prefix. Executed IF EXISTS notices remain delivered even if a later action fails.
Validation uses fresh committed roots after AX grant, including under RR, plus
own successful INSERT/UPDATE/DELETE effects; ordinary RR queries stay snapshot
isolated. Existing NULL fails23502 with column/schema/table, no Detail/Constraint.
Pure nullability changes retain the exact storage/backing; DROP COLUMN projects
rows and TYPE may rewrite live rows. INSERT23502 includes
failing-row Detail even for a named constraint. Both identify the decoded column
literally; all failures preserve schema/data. Compatible SET/ADD retains existing
name; DROP makes nullable; redundant operations are count-zero/no-revision.
Missing DROP42704 or IF EXISTS NOTICE00000 uses the relation/name diagnostic.
DROP NOT NULL on ctid/xmin/cmin/xmax/cmax/tableoid fails0A000 with
`cannot alter system column "name"`; ordinary absent columns remain42703.
DROP's default RESTRICT, explicit RESTRICT and CASCADE remove the selected
NOT NULL identity equivalently: supported NOT NULL constraints have no inbound
constraint dependents and own no SQL index. These modes do not implement a
general constraint dependency closure. CREATE TABLE UNIQUE and PRIMARY KEY own
real B-tree relations; DROP CONSTRAINT or an included key column removes that
backing relation atomically. Foreign keys and referenced-key RESTRICT/CASCADE
remain unsupported.
Unrelated NOT NULL identities and sequences owned by the
still-live table attribute survive even CASCADE.
Stable attribute IDs, ACL/comments and original storage/backing survive ALTER;
independently captured storage length fences a stale scan. DROP COLUMN removes
only the dropped identity. Retained prepared SELECT and parameterized INSERT
rebind current nullability across SET/ADD/DROP: nil23502 while enforced, nil
success after DROP, no cached-result-type error. Native known Nullable and
INT4 int64 ScanType differ deliberately from pinned pq's unavailable Nullable
and int32 ScanType. Retained snapshots keep the original metadata/values.

Dump captures and emits quoted named NOT NULL declarations, generated names
included. Restore uses common isolated CREATE/INSERT binding and atomic target
replacement. Scalar UPDATE uses existing expressions, destination coercions and
NOT NULL and CHECK enforcement (see below). CREATE inheritance merges both;
NOT NULL's NOT VALID/VALIDATE/NO INHERIT remain explicit native0A000 boundaries
with real PostgreSQL-only witnesses.
Selected nullability/named-NOT-NULL actions and existing DROP COLUMN compose
comma-separated atomic lists, including explicit transactions and savepoints.
Same-session metadata and DML see successful private definitions; rollback
restores descriptor, row shape and own-write roots. Commit publishes these with
sequence metadata/generators and DML through one callback-free installation.
Successive DROP actions see earlier staged view/owned-sequence removals; final
publication still fences the original dependency observations. Dependency
sequence admission preserves callback-safe replacement freedom without changing
ordinary sequence-function/DML reservations. UPDATE binding and staging use the
same private layout, including moved surviving slots and retained handles.
If the first ordinary RR snapshot follows private column projection, raw
committed roots retain authority while corresponding private dense snapshots
retain values/types. Savepoint restoration repairs a missing private projection
against those retained roots without refreshing ordinary RR visibility.
Preparation fences the owning transaction epoch before combining private
descriptors with the catalog/type generation. Provider validation remains
outside engine locks against that immutable generation; replacement is checked
again before installation and reports the ordinary catalog-replaced error.
Successful statement authorization is retained through Commit despite later
role-catalog revisions or internal actor changes, without reauthorizing table
ownership. SET/RESET ROLE and LOCAL role lifetime are supported in transactions
and savepoints; transactional SESSION AUTHORIZATION remains excluded.
PK and unrelated ALTER actions/transactional DDL remain excluded,
never successful no-ops. CHECK CREATE/enforcement and DROP CONSTRAINT are
supported; ALTER ADD CHECK/VALIDATE remains excluded.
Post-drop Dump/Restore captures nullable declarations, remaining NOT NULL
identities, rows, defaults/comments and owned-sequence state/associations; it
does not replay ALTER history or add ALTER to the Restore whitelist. Physical
server/WAL/crash durability, a wire/server process and external pg_dump/archive
mechanics remain outside the embedded engine.

CREATE TABLE admits ordered one/multiple INHERITS parents before ON COMMIT,
merging compatible columns/defaults/CHECK/NOT NULL with real normal dependencies.
IF NOT EXISTS skips source resolution for an existing destination. Malformed/
repeated/extra-tail input remains42601 with no script-prefix effects.
Native recursive parent scans/DML/ordinary LOCK reject0A000
`recursive table inheritance is unsupported`; physical INSERT and leaf DML work.
Graph shape/default/nullability/constraint ALTER rejects0A000
`ALTER TABLE inheritance operations are unsupported`, with sourced inherited
column/NOT NULL removal42P16 diagnostics. Whole-table rename/schema move and
dependency-aware DROP remain supported. See docs/TABLES.md for provenance,
generated/serial/identity behavior, native control replay and TEMP replacement policy.

Public smoke: `make check RUN='^Test(NotNull(Smoke|ControlsSmoke)|DropNotNullSystemColumnSmoke|DropConstraintOwnedSequenceSmoke)$'`.

### Session advisory locks — change note

The selected PostgreSQL18 session functions expose thirteen signatures:

| Function | Arguments | Result |
|---|---|---|
| `pg_advisory_lock` | `bigint` or `integer, integer` | `VOID` |
| `pg_advisory_lock_shared` | `bigint` or `integer, integer` | `VOID` |
| `pg_try_advisory_lock` | `bigint` or `integer, integer` | `BOOL` |
| `pg_try_advisory_lock_shared` | `bigint` or `integer, integer` | `BOOL` |
| `pg_advisory_unlock` | `bigint` or `integer, integer` | `BOOL` |
| `pg_advisory_unlock_shared` | `bigint` or `integer, integer` | `BOOL` |
| `pg_advisory_unlock_all` | none | `VOID` |

Bigint and pair keys are disjoint value domains, with signed arguments encoded
as their exact high/low uint32 bit patterns. Shared owners coexist; exclusive
requests conflict with other owners. Every successful blocking or try acquisition
adds one mode-specific count, including reentry. Matching unlock removes exactly
one count and returns true; wrong-mode, stranger, absent or exhausted unlock
returns false with one `WARNING`/`01000`, `you don't own a lock of type
ExclusiveLock` or `ShareLock`, without aborting the transaction. Unlock-all clears
only this physical session's advisory counts, never ordinary transaction locks.

These calls are strict, volatile ordinary scalar functions. Acquisition and
unlock-all return nonnull empty textual VOID; try/unlock return BOOL. Typed NULL
produces typed NULL without a request or warning. Demanded child analysis and
immutable simplification precede literal-NULL folding; runtime NULL still demands
both executable pair arguments, once, left-to-right. Prepare/Describe has no lock,
wait, count or notice effect. Supported query/write/view/CHECK consumers reuse
their current expression and admitted execution contexts. Volatility does not
widen DEFAULT, immutable generated/index expressions or Restore's direct scalar
INSERT allowlist. Stored CHECK/view syntax remains replayable; CHECK recomputation
uses the existing isolated Restore-stage session/manager, not live target locks.
Quoted nonexact spellings retain exact-name resolution, but more than100 arguments
still reports54023 after child analysis; an invalid child keeps its earlier error.

Counts survive commit, rollback, savepoint restoration and later statement errors,
even when failed row writes publish nothing. Returning a pooled borrower and
closing statements/rows do not end physical ownership. Matching unlock, unlock-all
or actual physical session end releases it. PREPARE TRANSACTION retains ordinary
transaction locks as a distinct detached participant; session advisory counts stay
with the origin and disappear on its physical close.

One canonical wait/deadlock manager arbitrates ordinary and advisory resources.
Live transient and persistent tickets collapse by physical session; detached
prepared tickets retain exact transaction identity. Reentry has owner priority,
new conflicting requests cannot barge, and compatible cohorts/soft-cycle search
remain intact. Advisory lock timeout55P03, statement timeout57014 and deadlock40P01
have no relation-analysis Position. Caller cancellation retains native context
identity. Deadlock failure does not release prior session counts: explicitly
unlock or close the owning session before demanding a blocked survivor finish.
Native advisory Detail uses unsigned key words and space1/2 with physical-session
cycle edges; ordinary edges keep their transaction/session/epoch convention.

Successful native Reset/Restore retains held advisory counts while terminating
old pending requests with ordinary `stuntgres: catalog replaced before publication`.
A real transfer before fenced readmission remains counted even though that command
fails. Fresh current-epoch admission reuses the same physical owner. Failed/canceled
staging preserves the target catalog and ownership. Dump emits stored definitions
and committed rows, never live advisory ownership. Transaction-level advisory
functions, `pg_locks` and other new lock introspection remain excluded.

Feature checks, each bounded to900seconds:
`make check RUN='^TestReferenceAdvisorySession'`
and `make check REFERENCE=1 RUN='^TestReferenceAdvisorySession'`.
The same family includes exact signatures/metadata, counts/warnings, lifecycle,
wait/cancel/deadlock, CHECK and native staged-replay witnesses; private tests supply
deterministic queue/fence barriers without adding a public inspection API.

### CHECK constraints — change note

CREATE TABLE admits ordered column/table `[CONSTRAINT name] CHECK (expression)`
with optional `NO INHERIT`. The complete destination row is bound before
publication, so references to later columns and current table/schema qualifiers
work. Existing scalar expressions, including supported volatile functions, use
the ordinary typed binder/providers. Definition binding and Dump do not invoke
providers. Parameters, aggregates and outside-row references are forbidden.
ctid/xmin/cmin/xmax/cmax report CHECK-specific42P10; tableoid is PostgreSQL's
exception but remains undefined in the native ordinary system-column surface.
Subqueries retain the CHECK-specific semantic exclusion; window calls report
42P20. SRFs, other CHECK modifiers and ALTER ADD CHECK/VALIDATE are not added.

After defaults/coercions and NOT NULL, the shared completed-row validator admits
TRUE/NULL and rejects FALSE with23514, actual Schema/Table/Constraint and
privilege-filtered physical-order failing-row Detail. All VALUES/default/query
INSERT and simultaneous UPDATE routes stage a whole successful batch before
publication. Sequence/random/session effects retain their existing
nontransactional semantics; row rollback does not rewind them.

On the first consumed row, execution prepares every CHECK's immutable expressions
before running any volatile CHECK provider. Detached executable nodes are cached
only in that command's evaluation context; stored syntax and dependencies remain
unchanged, including through ALTER rewrites and symbolic replay. Demanded immutable
child errors precede strict constant-NULL simplification: a CHECK containing
`pg_try_advisory_lock(NULL::integer,1/0)` binds at CREATE and prepares its INSERT
successfully, but INSERT execution fails22012 before earlier volatile CHECKs run.
Constant NULL suppresses volatile sibling lock/unlock/warning/wait effects;
runtime column NULL still demands executable siblings. The no-CHECK path does not
allocate preparation state.

Automatic names use distinct referenced attributes (`table_column_check` for
one, `table_check` otherwise), UTF8-safe63-byte clipping and current-command
suffixes. Explicit identities remain relation-local; generated identities avoid
existing namespace constraints. CHECKs enforce in alphabetical name order.
Current-command CHECK duplicates are42710; initial CHECK/NOT NULL name collisions
are pinned18.0's23505 pg_constraint unique-key diagnostic, with the actual newly
allocated relation OID, not a fixed allocator value. NO INHERIT persists and
enforces locally and is excluded when merging inherited CHECKs.

Dump/Restore retains named typed CHECK definitions, data and NO INHERIT.
Renames preserve names and render current attribute spellings. DROP CONSTRAINT
removes enforcement; DROP COLUMN automatically removes owner-local dependent
CHECKs and remaps surviving dense slots. Plain LIKE does not copy CHECK.
Supported sequence dependencies participate in existing RESTRICT/CASCADE
metadata staging: RESTRICT reports dependent CHECK identities; CASCADE removes
those constraints together with the sequence through the statement's catalog
publication authority. A failed drop leaves definitions and enforcement intact.
CHECK alone never changes known Nullable metadata.

Public smoke: `make check RUN='^TestCheckSmoke$'`.
CHECK interaction coverage uses the same untagged `reference/` definitions in
native and enabled PostgreSQL modes; the [complete selector](DEVELOPMENT.md#dual-mode-check-constraint-interactions)
excludes unchanged tagged oracle-only boundaries. No SQL surface is added by
this test migration.

### ALTER COLUMN TYPE — change note

`ALTER TABLE target ALTER [COLUMN] c [SET DATA] TYPE type [COLLATE name]
[USING expression]` shares the existing owner-authorized AX transaction,
catalog, binder and SQL type coercion providers. Without USING, conversion is
assignment coercion, not an arbitrary explicit cast. TEXT to INTEGER therefore
requires `USING c::integer`, even on empty/all-NULL tables. Every USING reads
the old row and may reference other old columns, including a source dropped in
the same list. For each live row, every TYPE expression runs in action order
against that original tuple, then final NOT NULL checks run in physical-column
order and affected CHECKs run on the completed transformed values, before the
next row is converted. An early constraint failure therefore precedes a later
row's conversion failure. No descriptor or row becomes visible until all
binding, conversion and validation succeeds. Invalid input, overflow, typmod,
dependency or cancellation failures preserve the complete previous definition
and data; explicit failures abort the transaction until rollback/savepoint recovery.

Character shrinking uses assignment length checks; an explicit character cast
inside USING truncates by characters. Numeric and temporal modifiers reuse their
existing rounding/range/environment providers. Stored defaults retain immutable,
unevaluated expressions and are independently assignment-coerced: USING never
changes a default. Existing standalone SET/DROP DEFAULT remains a separate
utility, not an action in TYPE lists. Existing named NOT
NULL, stable attribute/relation identities, comments, ownership and ACLs survive.
Dependent views/rules reject TYPE with0A000, even same-type commands; views
reading only unrelated attributes survive. Simple B-tree indexes rebind key usages
and rebuild sorted logical entries from the final candidate row shape atomically;
failed TYPE conversions, rollback and savepoint recovery preserve prior membership.

Surviving affected CHECKs are captured from their original canonical definitions,
reparsed after the entire TYPE/default pass against final destination usages and
DROP-adjusted slots, and atomically published with refreshed provider/attribute
manifests. Identity/same-type USING/collation-only TYPE discovers dependencies too.
Original concrete literals and typed NULLs retain their resolved usages and
values during TYPE capture; comparison constants are not reintroduced as unknown
input that could be inferred as the new column type. Private generated markers
transport immutable literal nodes through the existing CHECK binder, without
public parameters, new explicit-cast providers, or input-environment replay.
Final native CHECK serialization preserves the exact promoted value of retained
finite REAL literals, so Dump/Reset/Restore of a widened DOUBLE PRECISION CHECK
keeps the original comparison threshold. This does not change public casts,
ordinary floating output, or DEFAULT/view serialization.
TYPE traversal follows TYPE action then retained CHECK declaration order,
deduplicating a CHECK touched by multiple columns; stored CHECK order and ordinary
DML enforcement stay alphabetical. FALSE produces `23514`, `check constraint
"NAME" of relation "TABLE" is violated by some row`, with Schema/Table/Constraint
and no failing-row Detail. CHECK reparse operator errors have empty Position,
not the source position of an internally generated CHECK wrapper. Runtime CHECK errors
retain their own SQLSTATE/message. Incompatible defaults precede CHECK reparse;
CHECK reparse precedes any row conversion, including on empty/all-NULL targets.
Native controls retain the final rebound CHECK and NO INHERIT, not TYPE history.
Internal scalar reparsing uses an existing CREATE TABLE/CHECK grammar wrapper
with a quoted owner name, then binds only the extracted CHECK expression to the
completed descriptor. CHECK context preserves selected function calls instead of
misreading them as function-style casts; visibility inquiries use the existing
implicit BIGINT-to-OID provider after a column is widened. The wrapper is parsed,
never executed: it creates no metadata and does not broaden ordinary SQL admission.

Column COLLATE (also in final CREATE declarations for Restore) resolves
`pg_catalog."default"`100, `"C"`950, `"POSIX"`951, `pg_c_utf8`811 and
`pg_unicode_fast`6411 and registered user descriptors with namespace authority,
deterministic UTF8 byte/codepoint comparison and distinct stored identities.
Omission resets to the target type's intrinsic default. Missing names,
noncollatable types and unavailable providers fail; no fallback to C is invented.
Scalar annotations use the same catalog identity/provider authority.
Dump emits final type/typmod/nondefault collation/default/NOT NULL/CHECK declarations,
not ALTER history; Restore admits collation definitions before their consumers
and retains atomic replay.

Fresh direct/new prepared results expose updated metadata. Held prepared results
whose type/typmod/collation changes fail0A000 `cached plan must not change result
type`; stable result signatures rebind. Acquired eager rows retain old data/schema.
Held prepared INSERT/UPDATE retains its original inferred input parameter types,
then assignment-coerces to the new destination: widening input7 succeeds;
narrowing BIGINT input2147483648 to INTEGER fails22003 without inserting a prefix.
Fresh parameterized preparations infer the new destination type.
TYPE converts only grant-time live committed heads plus surviving own DML, not
invalid ended/deleted/replaced predecessors. Its RR owner reads the captured
live replacement; rollback/savepoints retain original immutable roots without
replaying USING. A peer RR future statement after replacement retains the native
catalog-replaced fence. This differs intentionally from PostgreSQL's physical
rewrite caveat (a pre-rewrite snapshot can subsequently see an empty table).
Physical MVCC rewrite, WAL/crash durability, ANALYZE statistics, pg_dump archives
and a wire/network server are not native logical-control mechanisms.

Smoke: `make check RUN='^TestAlterType(Smoke|ControlsSmoke)$'`.
Pinned reference: `make check REFERENCE=1 RUN='^TestReferenceAlterType'`.


Standalone ALTER TABLE RENAME [COLUMN] old TO new and RENAME TO new support
relation-level IF EXISTS. Both table and column targets accept qualified/path
names and t*/ONLY t/ONLY(t) markers for standalone tables. Graph column rename
is protected; whole-table rename preserves inheritance identities.
Destinations are single identifiers. Rename is count-zero OwnerOnly AX, with
CREATE on the containing namespace additionally required for table rename.
Prepare has no binding/authorization/lock/notice effects. Missing IF EXISTS
relations, including absent qualified namespaces, emit one NOTICE00000 naming
the unqualified relation; existing-table missing columns still fail42703.
Self/colliding column names fail42701; relation collisions fail42P07, atomically.
System source names fail0A000; system destinations fail42701. Modern oid remains
an ordinary user column. Utility diagnostics have no parse-analysis Position.

Renames preserve full relation/attribute identity, unchanged storage backing,
NOT NULL names and stable-attribute links, ACLs and comments. Published
descriptors and retained Rows are immutable. Prepared ASTs rebind original SQL
names rather than rewriting strings: old columns42703, old relations42P01,
changed star output labels0A000 cached-result-type error. Retained old-table SELECT
has Position15 on both native and pinned PostgreSQL: reanalysis preserves the
original relation-name occurrence. Cached-plan logical Context is empty on both
backends; C Routine is not a logical diagnostic field.
Canonical Dump emits current names/labels/comments/constraints, not ALTER history.
Stored views preserve stable sources and render current renamed identifiers.
Standalone RENAME CONSTRAINT supports represented NOT NULL/CHECK/UNIQUE/PRIMARY
KEY/EXCLUDE constraints, including writable explicit transactions and savepoints.
It requires table ownership, not namespace CREATE. Parent/constraint/backing-index
identities and definitions remain unchanged; current names drive violations and
prepared execution. CHECK names are enforced lexically after rename. Failed
renames and failing later transaction statements publish no partial group.
Graph-shape CHECK/NOT NULL ALTER remains0A000; admitted local UNIQUE/PRIMARY
KEY/EXCLUDE graph participants retain their existing allowance. Domain
constraints, foreign keys and constraint triggers have no native providers.
User row codecs retain their identity; simple B-tree index names remain unchanged while their stable parent/key identities survive renames.
Physical pg_dump/server disk/network mechanisms are outside native Reset/Dump/Restore. Smoke:
`make check RUN='^TestAlterRenameSmoke$'`.

Standalone `ALTER TABLE [IF EXISTS] target SET SCHEMA schema` moves ordinary
tables and all existing column-owned sequences in one indivisible CatalogStage.
Source USAGE and table OwnerOnly authority precede destination resolution/CREATE;
source CREATE and destination USAGE are not required. Same-schema requests still
check CREATE, then leave revisions and sequence plan stamps unchanged. Missing
IF EXISTS sources skip with NOTICE00000 before checking the destination.
In autocommit, Prepare returns no columns/parameters without lookup, locks,
authorization or notices. Prepare inside a native explicit transaction fails
0A000 `statement is unsupported in an explicit transaction`, even in READ ONLY,
and aborts the transaction under the existing utility admission contract.
Direct execution or a utility handle prepared before BEGIN instead applies
read-only execution precedence: READ ONLY fails25006 `cannot execute ALTER TABLE
in a read-only transaction`; READ WRITE retains the explicit utility 0A000 boundary.
Comma action lists are 42601 at the comma and never execute a valid prefix.

Owner-authorized table AX and deterministic owned-sequence AX protect the complete
closure. Collision preflight and final publication fences preserve the entire group
on failure/cancellation, including a later sequence collision (42P07 with destination
schema in the primary). Namespace/revision changes preserve storage, stable column
and NOT NULL identities, owners/ACLs, defaults/comments, sequence options/generators
and every physical-session cache/currval/lastval. Referencing views stay in place,
retain dependencies and render current source namespaces. Held qualified table and
sequence SQL reanalyzes original names. Native unqualified table statements bind
their original names against the current search_path at every Execute. PostgreSQL
can instead reuse a revalidated held plan while its effective search_path is unchanged:
after s1.t moves to s2 and a held statement rebinds there, recreating a same-shaped
s1.t does not alone redirect that PostgreSQL held SELECT. Native held SELECT sees
the new s1 shadow; PostgreSQL held SELECT continues reading s2. Fresh direct and
newly prepared SELECTs see the s1 shadow on both backends. This bounded cache
difference is independently tested, not full PostgreSQL cached-plan parity.
Canonical Dump/Restore uses current qualified persistent definitions/data and does
not replay ALTER history.

`ALTER TABLE view SET SCHEMA` is supported on both backends as an alias of the
view-only namespace move; it does not move the view's sources or dependents.
Native generic sequence SET SCHEMA still fails 0A000 `ALTER TABLE SET SCHEMA
supports ordinary tables only`; PostgreSQL independently supports that move.
Standalone generic index moves remain excluded; table-owned index movement is
part of the supported parent-table move.
Native pg_temp/actual temporary destinations retain 0A000 `temporary schema creation
is unsupported`, and pg_catalog retains 0A000 `system schema creation is unsupported`.
PostgreSQL temp moves fail 0A000 `cannot move objects into or out of temporary schemas`;
a privileged pg_catalog move is independently exercised with rollback. Native
read-write explicit transactions retain 0A000 `statement is unsupported in an
explicit transaction`; read-only execution rejects canonical ALTER TABLE first
with25006. Simple B-tree indexes move atomically with their parent, including
destination name collisions. User row types and their lookup-only arrays move
with the actual owning table/view. UNIQUE/PRIMARY KEY backing indexes move in
the same atomic schema group; native named NOT NULL and owned sequences are supported. Equal constraint names on unrelated tables
do not collide. Smoke: `make check RUN='^TestAlterSchemaSmoke$'`.

DROP TABLE supports grouped schema-qualified/path targets, IF EXISTS and RESTRICT/CASCADE with whole-group atomic preflight. Table owner, schema owner or superuser authority is required. Both modes remove selected tables and AUTO-dependent owned sequences, plus contained metadata/storage/ACLs; neither removes schemas/types/roles/independent LIKE clones. Retained Rows survive removal. Public DROP TABLE remains excluded from private Restore.

ALTER TABLE supports DROP [COLUMN] [IF EXISTS] name [RESTRICT|CASCADE] alone or in selected TYPE/nullability lists, ordinary t/t* and ONLY t/ONLY(t) for standalone tables. Table OwnerOnly (not schema-owner fallback) execution uses AX; metadata-only Prepare does not lock. Survivor attribute/origin IDs remain stable while dense rows/metadata compact. Native dependencies include named NOT NULL/ACL/comments, stored views and AUTO-dependent sequences keyed by stable attribute SubID. The existing transaction descriptor overlay carries projected committed/own rows and dependent view/owned-sequence removals through successful Finish, savepoint recovery and combined Commit. Final installation checks original storage pointer/root/independent length/generation/deletion roots. Index/FK/hierarchy mechanisms remain excluded; unrelated table DDL stays unsupported in explicit transactions. Retained snapshots stay immutable; prepared survivors rebind, changed result shapes retain the cached-type boundary.
The optional ALTER DROP COLUMN keyword consumes the shared dedicated COLUMN token used by COMMENT, not legacy KW_R text matching. Quoted keyword targets remain decoded IDENT names; preserve keyword categories and positioned parser errors when integrating grammar changes.
Canonical populated zero-column Dump uses one public DEFAULT VALUES insertion per row through common Insert. DEFAULT VALUES also applies declared defaults/typed NULL for nonzero-column tables; VALUES() remains invalid syntax. Restore does not accept ALTER TABLE history. Feature smoke: `make check RUN='^TestAlterDropColumn(Smoke|ZeroColumnControlsSmoke)$'`.

Do not widen syntax beyond the selected surface: no nested transactions,
2PC, SELECT aliases/qualified columns,
general casts/joins/functions/expressions,
UPDATE FROM/row-subqueries/CURRENT OF and DELETE CURRENT OF.
SQL BEGIN [WORK|TRANSACTION] and START TRANSACTION accept ordered isolation
READ COMMITTED/READ UNCOMMITTED/REPEATABLE READ/SERIALIZABLE, READ ONLY/READ WRITE
and [NOT] DEFERRABLE modes. SET TRANSACTION changes current owner modes;
SET SESSION CHARACTERISTICS AS TRANSACTION changes typed defaults.
SERIALIZABLE uses stable snapshots and nonblocking read/write dependency tracking;
READ ONLY DEFERRABLE waits for a safe snapshot before its first data analysis.
Malformed suffixes remain 42601. Driver default isolation honors defaults;
explicit RU/RC/RR/SER overrides and Go ReadOnly=false requests RW.
COMMIT/END/ROLLBACK/ABORT [WORK|TRANSACTION]
[AND [NO] CHAIN] retain qualified handles and one publication authority.
SAVEPOINT name, RELEASE [SAVEPOINT] name and ROLLBACK [WORK|TRANSACTION] TO
[SAVEPOINT] name support nested marks and newest-name shadow/reveal. Rollback
restores saved writes/deletes/settings/sequence overlays, retains the target
and destroys later marks. Release retains changes and removes target/later
marks, except it restores the target parent's read-only mode. A recoverable
error restores the newest mark and leaves the block Aborted until ROLLBACK TO.
Per-mark relation/row lock contributions are restored on rollback/error, retaining
the same ticket and premark modes. Cursor declarations after the mark disappear;
older motion, failure and CLOSE persist. PL/pgSQL remains excluded.
Modifying utilities outside selected ALTER TYPE/nullability/identity/DROP COLUMN
lists and sequence utilities remain a native 0A000 boundary in RW blocks.
Exec-only RO rejects supported INSERT/UPDATE/DELETE, CREATE/ALTER/DROP
table/schema/role and COMMENT with 25006/canonical tag before effects and that
native utility boundary; valid data-write Prepare still analyzes/describes.
LOCK, reads and settings remain allowed; session authorization retains its
separate native explicit boundary.

Settings use the existing registry, typed values and SESSION/LOCAL action journal.
Commit preserves SESSION, rollback/abort restores, LOCAL expires. Current
transaction_isolation/read_only/deferrable resolve the owner;
default_transaction_isolation/read_only/deferrable affect next starts only.
Current RESET/SET DEFAULT/set_config(NULL)
reject 0A000 `parameter "<name>" cannot be reset`; RESET ALL excludes current
modes but transactionally resets defaults. Different isolation after the first
snapshot fails 25001; identical requested mode succeeds (RU != RC). Late RO→RW
fails 25001; RW→RO succeeds. SET TRANSACTION outside a block warns25P01 at Exec.
DEFERRABLE changes after the first query fail 25001 even when requesting its
existing value; query-backed set_config therefore cannot assign it. A subtransaction
assignment fails 25001 before the late-query check. Savepoints restore all three
characteristics; attempted participating writes and predicate evidence survive
child rollback until the outer transaction ends.
Route current assignments before Engine/settings/Session locks, never invert
owner-first order. Providers/normalizers run outside publication locks.
Physical settings registration, search_path alias, timer and adapter boundaries
are unchanged. Restore whitelist does not expand. Multirow DML remains atomic.
SHOW ALL built-in descriptions are PostgreSQL 18.0's English short_desc metadata,
including all four current/default transaction settings; custom descriptions
remain the registered text, with no blanket built-in metadata exemption.

REPEATABLE READ retains all canonical relations' immutable slice headers/end
roots once, including empty/never-read relations, before semantic waits; no row
values are copied and BEGIN captures nothing. RC/RU execution remains grant-time
fresh. SELECT singleton/identity/settings functions and data Prepare freeze timing.
Current binding/ACL uses grant-time metadata. Missing saved addresses mean empty
committed roots; saved incompatible storage/row shape is ordinary ErrCatalogReplaced,
not historical-catalog/transactional-DDL support. Results pin roots independently.
Successful COMMIT AND CHAIN preserves final isolation/access despite changed
defaults and clears snapshot/writes/deletes/holds, even with a live child RO mode.
ROLLBACK AND CHAIN and failed-block COMMIT AND CHAIN first unwind all retained
marks and carry the oldest mark's parent readOnly, not a failed/live child mode.
Without marks, abort restores entry defaults before BEGIN/Go overrides and
rolls back in-block defaults; with marks it restores the failed level before
full end unwinds parents. Plain end then BEGIN uses session defaults.
A chained block uses its carried modes as the abort baseline, not session defaults.
For RR conflicts a post-frontier committed replacement reports concurrent update
even if subsequently deleted; only deletion of the snapshot predecessor reports
concurrent delete.
Read-only modifying utilities reject with 25006 before object resolution,
authorization and relation locks, including missing or locked targets and
statements prepared before BEGIN. DML retains semantic analysis before read-only
execution rejection. Selected ALTER TYPE/nullability/named-NOT-NULL/DROP COLUMN lists
and sequence utilities permit effect-free Prepare and transactional execution;
unrelated utility Prepare/execution retains its native unsupported0A000 boundary.
Feature commands: `make check RUN='^TestReference(Serializable|TransactionIsolation)'`,
`make check REFERENCE=1 RUN='^TestReference(Serializable|TransactionIsolation)'`.
The migrated SQL witnesses use the same untagged definitions in native and actual
PostgreSQL modes; backend-specific capabilities remain independent phases.
See [transaction/locking test migration](DEVELOPMENT.md#dual-mode-transactions-savepoints-and-locking)
for the 111-original/130-definition inventory and retained tagged controls.

Control classification precedes ordinary implicit birth/epoch/command gating. Prepare is effect/notice-free; start Prepare Aborted gives 25P02, end Prepare remains eligible without recovering until Exec. Same-epoch SQL Aborted COMMIT succeeds by rollback/count0, whereas Go Commit keeps its failed-transaction ordinary error. Completed controls Finish once without late authority checks/merge/command advance into a chained incarnation. Shared private helpers never reacquire admission; one physical-session owner and publishWrites remain sole authority. CHAIN reserves a fresh current-epoch ref and empty ticket before old publication under the existing short replacement read fence; install is infallible, then release old holds before waking waiters. Resolve old settings actions after publication and before clearing/installing the next incarnation; CHAIN inherits committed SESSION values, never LOCAL values or old actions. Old native GoTx refs cannot end a new SQL incarnation (intentionally unlike pq's unqualified old end). Healthy pool reuse/borrower Close preserves SQL blocks; pin and explicitly end before pool return. Physical end drains and discards only its own private work/holds. Restore's whitelist still rejects controls; no script-transaction expansion.

Failed-block SQL COMMIT rollback success is restricted to the same catalog epoch.
Successful native Reset/Restore fences Active and Aborted transactions, writers
and read-only blocks alike: valid ordinary work and every COMMIT/END variant
return ordinary `stuntgres: catalog replaced before publication`, not SQLSTATE.
The first admitted stale failure discards private writes and transactional
settings; repeated ordinary use remains fenced. An accepted failed commit ends
the old incarnation without installing a chain. Full ROLLBACK/ABORT intentionally
recovers across epochs, including fresh current-epoch ROLLBACK AND CHAIN.
End Prepare remains effect-free/eligible; retained handles are not deleted.


Relation locking is transaction-owned AS for ordinary SELECT, ROW SHARE for locking SELECT, RX for INSERT/UPDATE/DELETE, row UPDATE for DELETE and NO KEY UPDATE for keyless UPDATE, SUE for COMMENT Execute, and owner-authorized AX for selected ALTER utilities. Explicit `LOCK [TABLE]` accepts ordered targets with ONLY/star, all eight table modes (default AX) and NOWAIT in an explicit SQL/driver block, including read-only blocks. Documented table-level ANY-rights privileges apply; ordinary views recurse with owner authority, security_invoker with the original invoker. CREATE inheritance retains parent SUE through publication; ordinary graph-parent LOCK rejects recursion while ONLY remains physical. The complete matrix and savepoint restoration contract are in [relation locks](TRANSACTIONS.md#relation-locks). Semantic data Prepare acquires automatic table modes and fixes characteristic/RR snapshot timing before waits; LOCK/COMMENT/ALTER Prepare remain metadata-only. Existing fair compatible cohorts, deadlock resolution, absolute deadlines, typed lock_timeout, caller-context identity, exact grant-time relation identity, replacement fences and publication-before-release are unchanged. UPDATE/DELETE preserve frozen candidate identities, RC/RU recheck and RR40001 behavior. Session advisory locking is supported; transaction-level advisory locking and introspection remain excluded. Feature smokes: `make check RUN='^TestReferenceLockModes(Matrix|Order|Views|Savepoints)$'`.
Lock-timeout55P03 and deadlock40P01 during direct SELECT/INSERT/UPDATE/DELETE relation analysis or semantic Prepare carry the original ObjectName's first token character Position (schema token for qualified names). Cached Execute, SELECT/UPDATE/DELETE row waits and utility errors have no parse-analysis Position;57014 is excluded from this decoration. Do not put source-position policy into manager resources or fabricate Schema/Table fields.
The canonical manager searches finite stable queue-order constraints to resolve soft cycles without aborting; unavoidable relation/row cycles fail an unspecified actual cycle member with ERROR40P01 `deadlock detected` and Hint `See server log for query details.` Native Detail describes exact logical transaction/session/epoch, mode, relation ObjectRef and optional RowID cycle edges, not physical PostgreSQL PID/XID/ctid identities or tuple Context. Without marks, mandatory Finish discards victim private effects and releases transaction-owned locks, allowing survivors to proceed before explicit victim ROLLBACK. With recoverable marks it restores the newest mark's effects and held-mode checkpoint, releasing failed-level contributions while preserving ancestor holds. Prior session advisory counts are never part of that restoration/release. Explicit victims retain the existing25P02 recovery gate. SSI remains unsupported and is not a blocking dependency.
Queued work binds the grant-time descriptor of the exact retained ObjectRef; a same-identity definition revision is valid, while epoch changes/removal/recreation remain fenced. Keep subsequent grant-time descriptor pinning and catalog/storage publication guards. Post-drop quoted expression errors use literal decoded identifiers and character positions. Positive qualified ordinary-owner controls use one ordinary schema/table owner; separate distinct-owner Restore/denial/replay witnesses retain the namespace-USAGE and explicit-ACL Dump boundaries without adding native GRANT.
ALTER re-resolution must compare the complete ObjectRef with scope.lockedRef before owner/column semantics, notices or staging, then use the validated grant-time lockedDescriptor. A search_path shadow created during an AX wait fails ErrCatalogReplaced without effects or reacquisition; PostgreSQL's independently asserted utility behavior instead reacquires the shadow AX. Never reinstate a pre-wait descriptor-revision fence.
DROP COLUMN compaction removes only the dropped stable SubID's column comment and preserves table/survivor comments immutably. Validate comment keys with hasAttribute; Dump must capture attributes and index comment payload by attributes[i], not dense i+1. Restore rebinds canonical named comments to the reconstructed declaration's IDs.
Successful CatalogStage publication advances the shared revision allocator past staged installed revisions without lowering an already-leading frontier; MaxUint64 saturates and later allocations fail ordinarily. DROP keeps old revision + 1, with no early allocator reservation. Commit the candidate frontier with descriptor/storage installation only after final fences, so failed/canceled stages and skips do not consume revision tokens. Later TABLE/COLUMN COMMENT must publish strictly fresh tokens at the retained identity.

CREATE/ALTER/DROP SCHEMA and search_path/current_schema reuse existing catalog/session/role seams. Nested CREATE SCHEMA elements are persistent CREATE TABLE only with atomic publication. Schema provisioning/rename uses effective-superuser database-CREATE baseline; fixed native database label stuntgres is diagnostic-only. Native public retains PUBLIC USAGE|CREATE. Prepared AST re-resolution has a coherent frozen signature guard, not full PostgreSQL cached-plan caveat parity. Absent pg_temp lookup never allocates; table/sequence creation initializes the physical owner's namespace.

Search-path namespace selection has one kind-aware authority. Explicit entries
retain decoded order and deduplicate by namespace identity; missing, empty,
no-USAGE and foreign TEMP entries are ignored. `$user` expands CURRENT_USER.
This native foreign-TEMP exclusion differs from PostgreSQL18: an explicitly
named peer temporary namespace is searchable there when schema USAGE permits
it, so its relation can be `pg_table_is_visible` even from a different session.
An absent explicit pg_catalog is inserted before the explicit path; initialized
own TEMP is inserted before catalog for relation/type lookup only. Explicit
catalog/TEMP keep their stated placement. Unqualified function/operator lookup
excludes TEMP even when explicitly listed; qualified lookup restricts candidates
to its validated namespace, never falls back by familiar spelling.
Qualified non-TEMP schema USAGE denials preserve the namespace token's position;
COMMENT diagnostics retain their existing unpositioned utility policy.
Supported builtin type identifiers, null-count/sequence/settings functions,
random/random_normal/setseed, current_schema(), type-input functions, count(*) and arithmetic/comparison
operators accept catalog qualification in their existing expression contexts.
The min_scale/round/scale/trim_scale/trunc/sign/mod and
abs/ceil/ceiling/div/exp/factorial owners also accept catalog qualification
through this resolver, without allowing familiar spellings in a wrong namespace
to bypass lookup.
Qualified type names use catalog identifiers, not SQL aliases:
pg_catalog.int4 is valid, pg_catalog.integer is not an alias. Qualified operators
use OPERATOR(schema.symbol) and generic operator precedence.
Qualified temporal declarations such as pg_catalog.time(3) and
pg_catalog.timestamp(3) preserve precision and canonical TIME(3)/TIMESTAMP(3)
Dump/Restore declarations. Bounded temporal explicit casts and same-type precision
annotations work qualified or unqualified, without adding conversion providers.

The scalar function owner exposes stable, strict current_schemas(boolean).
FALSE returns the explicit searchable path; TRUE includes implicit namespaces.
Prepared BOOL parameters observe fresh settings/current-role state at execution;
returned values are immutable snapshots. NULL returns typed NULL; an empty
explicit path returns `{}`. Stored views containing current_schemas likewise
read the executing session's runtime path, including through held prepared
queries, rather than the view's creation-time path.
NAME[] has OID1003, catalog name _name, public
DatabaseTypeName `_NAME`, typmod-1, no origin and NAME-element collation950.
Native ScanType/export are string; PostgreSQL/libpq reports interface{} and
raw []byte instead. The selected bounded domain-base path adds textual array
input/storage/casts, not general array operations. Output quotes and escapes names.
NAME[] may be projected alongside scalar ordering, but native ORDER on NAME[]
itself returns positioned42883 at analysis, even for empty/all-NULL sources.
PostgreSQL supports array ordering; that external comparator is not native parity
or a reason to sort the encoded array as TEXT.
Native current_schema/current_schemas omit absent TEMP without materializing it;
PostgreSQL current_schemas may materialize a pending leading pg_temp. Once a
supported TEMP table or sequence creates the namespace, normal ordering/dedup applies.
SQL routine/type/operator DDL and general overload resolution remain excluded.
Native Dump/Restore preserves qualified persistent expression spelling and
objects/data, not session search_path; it is not external pg_dump, wire protocol,
server configuration or filesystem durability.
Feature smoke: `make check RUN='^TestSearchPathSmoke$'`.

### Index capability inquiries — change note

`pg_indexam_has_property(oid,text)`, `pg_index_has_property(regclass,text)` and
`pg_index_column_has_property(regclass,integer,text)` are STABLE/STRICT nullable
BOOL functions in existing typed scalar consumers. Property names match ASCII
case-insensitively without trimming. Unknown/wrong-level names, invalid numeric
identities, non-index relations and invalid index-column ordinals return NULL.
Missing named REGCLASS input is a normal resolution error, not an invalid-OID NULL.
OID, integer widths, TEXT/VARCHAR and existing domains implicitly select the
documented REGCLASS signature; bounded explicit casts and direct storage are
documented under regclass relation identifiers below.
String conversion uses captured statement input authority.

Fixed builtin AM IDs403/405/783/2742/4000/3580 answer documented capabilities;
this does not add pg_am, other catalogs or non-B-tree DDL. B-tree can_order is true.
All four whole-index capabilities, including backward_scan, are true for existing
B-tree definitions. Ordered keys report their DESC/NULLS FIRST definition.
INCLUDE columns are returnable but not orderable; their asc/desc/null-order and
search_array/search_nulls inquiries are NULL. Invalid property is distinct from
a recognized false capability. These are definition properties, not optimizer
or physical scan observations.

Views, CHECKs, grouping and existing expression consumers share the same node.
Native DEFAULT admission remains excluded, and STABLE calls are rejected in index
and generated expressions. Canonical index/view/CHECK definitions survive native
Dump/Restore and renames; stateless inquiries add no serialization mechanism.
See [design and complete capability tables](design/PG18-FUNC-INFO-INDEX-PROPERTIES.md).
Smoke: `make check RUN='^TestReferenceIndexPropertiesFeatureSmoke$'`;
pinned mode adds `REFERENCE=1` to that same command.

### Search-path visibility inquiries — change note

The four PostgreSQL18 inquiry names each have one STABLE/STRICT `(oid) → boolean`
signature: `pg_table_is_visible`, `pg_type_is_visible`,
`pg_collation_is_visible`, `pg_conversion_is_visible`. There is no function
overload visibility inquiry or SQL routine DDL in this change. They use the
existing typed expression path, including OID parameter inference, BOOL result
metadata and positioned signature/input diagnostics.
Children are transformed once before the more-than-100-argument guard (54023),
then explicit namespace/signature lookup and OID inference/coercion. A missing
child column gives positioned 42703 even when the explicit schema is absent or
the call has 101 arguments. With 101 valid children, 54023 precedes absent-schema
3F000; with one valid child, the absent schema still gives positioned 3F000.
STRICT NULL folding occurs only after this analysis and resolution.

NULL input or an OID missing from the queried object class returns BOOL NULL.
An existing object is visible exactly when its catalog identity is the first
eligible same-name identity in the effective path; shadowed/off-path objects
are false, not NULL. Relation kinds share a namespace. Types use catalog names,
including result-only/pseudo descriptors, not SQL aliases or storage admission.
No subject table SELECT/type USAGE/conversion-procedure EXECUTE check is made.
Schema USAGE still controls effective-path membership.

Relations/types include own TEMP; collations/conversions exclude it even if
explicitly listed. Missing TEMP is never allocated. An Execute whose bound
query, nested view, RETURNING or CHECK requires visibility captures one
immutable private-aware catalog at its existing statement frontier. The explicit
path is resolved once, with separate relation and no-TEMP paths. Unrelated
statements retain no visibility capture. Nested demand is derived from current
identity-bound view definitions, not cached on dependent views: replacement can
add or remove an inquiry without rebinding dependents or forcing capture after
removal. Held prepared SQL refreshes authority, not Prepare-time decisions.
Stored views remain execution-path sensitive;
CHECKs, including transformed ALTER COLUMN TYPE validation, share statement
authority. Stored inquiry CHECKs support compact/pretty pg_get_constraintdef
and pg_get_expr rendering and replay; native conbin is structural data, not
PostgreSQL nodeToString. DEFAULT admission still rejects these inquiries.

All25 native type descriptors, seven fixed collations (100,950,951,962,963,811,6411),
user collation descriptors and all128 fixed conversions (4402–4449,4452–4531)
have real inquiry metadata.
Collation lookup is namespace-first, then UTF8 encoding6 before encoding-1
fallback inside each namespace. A wrong-encoding existing collation is false.
An earlier namespace fallback beats a later exact encoding. Conversion lookup
uses name/namespace, not encoding pair/default/procedure privilege:4402 is visible
despite having no UTF8 side. Metadata does not enable conversion execution,
general locale comparison or imported OS collations. Registered user collations
support the same truthful C/POSIX/builtin capabilities as supported seeds.
Ordinary B-tree indexes participate in relation visibility, including same-name
cross-kind shadowing, and their definitions roundtrip through native Dump/Restore.
CREATE TYPE forms other than AS ENUM or AS (...), CREATE CONVERSION and foreign
table creation remain excluded; permanent materialized views follow the
[bounded supported surface](SQL.md#materialized-views--change-note), without
temporary materializations/sources, MV ALTER/index or REFRESH.
Builtin/catalog row identities stay lookup-only; supported user rows have
concrete codecs. TEMP tables use the ordinary table path.

Fixed identities rebuild on New/Reset; Restore replays legal collation seed metadata deltas.
Supported user objects/views/CHECK definitions roundtrip through existing SQL;
restored generated OIDs must be rediscovered. Session search_path is not dumped.
Feature smoke: `make check RUN='^TestReferenceCollationLegacyVisibility$'`.

### User collations — change note (changelog)

CREATE COLLATION supports FROM copies and PROVIDER/DETERMINISTIC/LOCALE/
LC_COLLATE/LC_CTYPE/RULES/VERSION attributes. Option spelling and positions are
retained through quiet Prepare; validation/provider loading and publication occur
only at execution. Copies receive independent identities and recompute actual
version; they do not depend on the source descriptor.

The registered execution boundary is libc C/POSIX and builtin C, C.UTF-8
(alias C.UTF8), PG_UNICODE_FAST. These preserve ASCII, Unicode simple and Unicode
full case semantics respectively. Distinct identities conflict even when their
providers agree. ICU reports0A000 `ICU is not supported in this build`; other libc
locales report0A000 `libc locale provider data is not available in this engine`.
No provider is silently replaced by byte ordering or ASCII case behavior.

ALTER COLLATION RENAME/SET SCHEMA/OWNER TO retains identity and owned dependencies;
REFRESH VERSION compares recorded/actual values, emits notices, and changes only
recorded metadata. User definitions own namespace/role dependencies. Domains,
columns, defaults, CHECKs, indexes and views retain collation identity and
participate in atomic RESTRICT/CASCADE closure. Seed bootstrap identities remain
pinned. Ordinary ownership, eligible SET ROLE and target schema CREATE authority
apply to owner transfer; superuser bypass follows PostgreSQL.

Pinned seeds also permit ordinary rename, permanent schema moves and owner
transfer. Their OID, pin and registered provider properties remain immutable;
DROP still reports2BP01. Initial/canonical seed ownership uses the genuine
bootstrap principal, not whichever role later has the name `postgres`.
Public role rename remains outside the existing native surface; package identity
regressions exercise role-name reuse without adding that role-rename SQL feature.
Native collation utilities retain the existing explicit-transaction0A000 boundary.

Read-only pg_catalog.pg_collation exposes provider/category/locale/rules,
determinism, encoding, owner and nullable recorded version.
pg_collation_actual_version(oid) reports actual registered provider version:
libc C/POSIX NULL; builtin1. Missing OIDs fail42704. Missing recorded versions
are distinct from empty versions; differing recorded builtin versions issue the
PostgreSQL version warning instead of changing comparison semantics.

Dump emits qualified collation definitions before domains/tables/views/indexes
and selects their named owners. Options reconstruct ordinary definitions;
immutable bootstrap FROM reconstructs exceptional encoding metadata (libc
all-encoding copies and builtin encoding6 copies). User-source FROM links are
never persisted, and no dependency on the bootstrap source is invented. Restore
replays privately and publishes atomically, retaining roles, existing held-row
snapshots and truthful provider capability; failed/canceled imports leave the
destination unchanged. Named owners must already exist. Its guarded private
collation destination authority handles independent schema/object ownership
without adding grants to ordinary SQL.

Dump emits changed seed name/schema/owner metadata before user definitions,
using current qualified bootstrap names for lossless FROM reconstruction.
Reset restores canonical seed metadata; repeated Restore restores the emitted
metadata and actual owners without reminting pinned objects.

Explicit CREATE COLLATION pg_temp.name initializes the owned temporary namespace.
Temporary collations are excluded from ordinary collation search-path lookup;
own alias/physical qualification works, and only genuine superusers may qualify
another physical session's temporary namespace. Ordinary same-role peers are
not the owning session. SET SCHEMA into or out of temporary namespaces fails
0A000 `cannot move objects into or out of temporary schemas` without mutation.
Physical owner end atomically cascades actual durable consumers using ordinary
dependency/storage projection and a genuine private cleanup session. Borrower
return does not end this lifetime. Namespace/children and dependent storage
publish together; lock/conflict/exhaustion failures leave the candidate unpublished
and are joined into the physical close error, never replaced by generic deletion.
Dump omits temporary definitions, rejecting durable temporary-provider references
before writing output. Reset/Restore removes durable consumers while preserving
the exact live target temporary descriptors and capabilities. Imported dynamic
OID collisions are reidentified throughout domains, arrays, expressions, frozen
views, index keys and dependencies; source snapshots and target TEMP are unchanged.

Feature smoke in both modes:
`make check RUN='^TestReferenceCollation.*$'`;
`make check REFERENCE=1 RUN='^TestReferenceCollation.*$'`.


COMMENT supports TABLE/COLUMN/SCHEMA/ROLE only, with decoded literals and NULL/empty removal (whitespace persists). Store text in immutable descriptors, column maps keyed by stable SubID, or shared RoleDescriptor values; never name-keyed side storage or adjacent SQL description functions. Lookup and live ownership precede mutation; COLUMN nonowner says relation, TABLE says table. Ordinary roles cannot self-comment; CREATEROLE/ADMIN expansion is excluded. Explicit-TX COMMENT remains native 0A000. Rename/owner transfer retains text; drop/recreate clears it. Plain LIKE excludes text; INCLUDING COMMENTS copies admitted column comments, never the table comment. Database Dump excludes shared role comments, Reset/Restore retain them.

DSNs are exact opaque process-local names, including empty string, scoped to a Driver. Different strings/Drivers isolate data. Closing pools/connections does not erase an engine. Reset atomically clears objects retaining process-local identity. Dump serializes persistent user schemas before qualified tables/views in common creation-address order among objects whose sources are ready, with owner directives as needed and table data/comments beside CREATE; unsupported explicit ACLs fail before output. Incumbent view topology takes precedence over exceptional remapped source addresses. Restore stages and atomically replaces, preserving independently mapped namespace/table owners through private stage-only destination authority, never fake grants. Controls require native pools, reject wrapped/foreign drivers and nil inputs, and preserve data on errors. Snapshots survive controls and schema lifecycle changes. Never hold target locks during external I/O.

Fixture publication preserves physical session identity, live role views and
baseline settings, while transaction abort/end resolves SESSION/LOCAL actions.
Host authorization for native controls does not grant SQL privileges. Failed or
canceled Restore before installation leaves committed state, epoch and otherwise
eligible transactions intact; already-Aborted transactions are not healed.
The control borrower is released before external I/O. A blocking Reader must
return before cancellation is observed. Foreign replacement checks cancellation
while filtering live source rows and once more after authority preparation,
immediately before indivisible identity/frontier/catalog installation. Cancellation
after that final successful observation cannot retract the replacement. Captured
cursors retain old typed rows/metadata, not authority to publish (Go sql.Tx may
itself close owned Rows).

BYTEA control values are canonical ASCII hex regardless of bytea_output.
Late malformed byteain, reader errors and cancellation before publication
leave the old typed catalog/data intact; retained byte buffers remain detached.

Feature smoke: `make check RUN='^TestControlTransactionsSmoke$'`.
Native fixture replacement has no PostgreSQL wire-command oracle; reference
tests independently assert READ COMMITTED/private visibility, English SQL
diagnostics and same-epoch failed-block recovery on the pinned real server.
Restore's existing private whitelist is unchanged; transaction-control scripts,
live settings and shared role effects are not authorized by these witnesses.

Trusted Restore INSERT and database-local COMMENT use local validated captured-bootstrap AuthorizationViews, never session identity mutation. COMMENT authority covers qualified lookup, lock resolution and metadata mutation, is restricted to local targets and is cleared after each statement. Named CREATE owner groups remain intact; per-statement private markers cannot affect public Prepare/INSERT/COMMENT or persist schema/table ACL grants.

Restore's initialized private catalog accepts CREATE/ALTER/DROP SCHEMA, CREATE TABLE with supported defaults/identity, CREATE SEQUENCE, INSERT with supported parameter-free scalar/default-request forms (including integer arithmetic and SYSTEM identity override), database-local COMMENT, generated sequence OWNED BY associations, numeric/persistence configuration of internally owned identity sequences, SELECT setval(name,value,is_called), and named SET SESSION AUTHORIZATION only. Nested parameters remain rejected recursively. Dump reconstructs bootstrap identity/name/owner changes before user objects; retained canonical builtin ACLs transform owner grantor/grantee, recreated public has absent ACL, and unchanged bootstrap retains legacy bytes. Local schema comments follow schema creation, table/column comments follow CREATE before INSERT under owner groups. Arbitrary explicit ACL serialization stays unsupported. Held INSERT rebinding INT4→TEXT rejects native 0A000 before importer/write; pinned PostgreSQL retains INT4 parameters and assignment-coerces to TEXT, an explicitly asserted difference.

The parameter-free scalar inventory includes the selected string functions,
floating and exact numeric arithmetic/power with typed leaves and preserved grouping,
LIKE escape normalization and `crc32`, `crc32c`, `md5(bytea)`, `sha224`,
`sha256`, `sha384`, `sha512`. Hash operands use normal signature/inference
validation, recursively reject parameters and preserve Restore atomicity;
this does not admit arbitrary functions, query INSERT sources or hash DEFAULTs.


SERIAL native controls use the same shared CREATE/default binder, including
canonical `nextval(CAST('qualified_sequence' AS regclass))` replay. Dump retains
actual integer widths, renamed sequence identities/options/state, AUTO ownership
and independent default dependencies without allocating. Direct sequence
RESTRICT blocks its dependent default; CASCADE removes the default only.
Feature smoke: `make check RUN='^TestSerialSmoke$'`.


## Sequences

Sequence objects support the PostgreSQL18 CREATE/ALTER/DROP option and administrative forms documented in the [SQL reference](SQL.md#sequences), including TEMP/UNLOGGED, optional BY/WITH, CREATE/ALTER RESTART, NO MINVALUE/MAXVALUE/CYCLE, OWNED BY, IF [NOT] EXISTS and grouped DROP. RESTART sets the current uncalled value, not configured START: CREATE START 5 RESTART 9 returns 9 first, and a later bare ALTER RESTART returns 5. Numeric options are integral and bounded by the declared smallint/integer/bigint type; function results always use INT8. Constant-space reservation arithmetic handles large CACHE and int64 extremes without allocating cached arrays. CYCLE wraps on reservation boundaries and can repeat values; it is not a uniqueness constraint.

Sequence SQL interactions now use the untagged mode-aware external reference
definitions. Select `^TestReferenceSequence[A-Za-z0-9_]*$` with
`make check` for native and `make check REFERENCE=1` for native plus
the pinned PostgreSQL18.0 oracle. Native snapshots/control replacement and server
cache/namespace/queue witnesses have independently specified backend phases;
the migration changes neither sequence semantics nor native exclusions.

CREATE accepts bare `RESTART`, `RESTART n` and `RESTART WITH n` through the
same option validation as ALTER, including START/restart bounds and duplicate
options. It initializes an uncalled generator at configured START (bare) or
the explicit restart value without changing START.
`CREATE SEQUENCE s START 5 RESTART 9` first yields9 from `nextval('s')`;
later bare `ALTER SEQUENCE s RESTART` makes the next call yield5.

Generic ALTER TABLE relation rename also accepts a sequence and atomically
publishes its fresh sequence-plan stamp without changing identity/generator/cache.
Generic sequence column rename rejects with42809 `cannot rename columns of relation
"s"` / Detail `This operation is not supported for sequences.` before attribute
lookup, including missing/system-column names and optional COLUMN syntax.

Sequence OWNER TO supports role names and CURRENT_ROLE/CURRENT_USER/SESSION_USER
identity operands; quoted identity spellings remain literal role names.
Same-owner requests need no renewed destination schema CREATE. Genuine ownership
changes by a non-superuser retain destination-role/schema checks; superuser
transfers bypass those transition checks. Actual linked-sequence owner changes
remain forbidden; successful transfers remap owner grantor/grantee identities
while preserving unrelated grants and same-owner requests preserve ACL/state.

CREATE/ALTER OWNED BY takes transaction-owned AccessShare on the referenced
table before fresh exact identity/column/owner/schema validation, waits behind
AccessExclusive and holds through transaction end. Canceled or timed-out
acquisition publishes no new sequence or association. OWNED BY NONE and
skipped IF NOT EXISTS/IF EXISTS commands take no dependency-table lock.
ALTER retains sequence-before-table ordering and participates in the canonical
deadlock graph, including soft queue reordering and unavoidable-cycle40P01.

Sequence SET SCHEMA rejects moves into or out of temporary namespaces with
`0A000` / `cannot move objects into or out of temporary schemas`, including the
`pg_temp` creation alias and the caller's actual temporary namespace identity.
Another session's temporary destination is rejected earlier by creation authority
with `42P16` / `cannot create relations in temporary schemas of other sessions`.
Linked-sequence and destination missing/CREATE checks retain their precedence. A
rejected move leaves identity, cached values, held analysis and physical-session
ownership unchanged; ordinary persistent-to-persistent moves remain valid.
Smoke: `make check RUN='^TestSequenceTempMoveSmoke$'`.

CREATE SEQUENCE resolves explicit actual namespace identities before CREATE and
requested-persistence checks. TEMP accepts its caller-owned temporary namespace;
ordinary logged creation there becomes temporary. TEMP in a persistent namespace
fails `42P16` / `cannot create temporary relation in non-temporary schema`;
UNLOGGED in any temporary namespace fails `42P16` /
`only temporary relations may be created in temporary schemas`. Foreign TEMP
creation retains the `42P16` ownership record above. Missing/denied namespaces
retain `3F000`/`42501` precedence, including IF NOT EXISTS; failures publish no
private namespace or sequence state.
Smoke: `make check RUN='^TestSequenceTempCreationSmoke$'`.

Temporary tables and sequences share session-owned namespaces. CREATE TEMP/
TEMPORARY TABLE and ordinary CREATE TABLE in own pg_temp/actual TEMP or a leading
explicit pg_temp path stage private descriptors/storage with ordinary types,
defaults, CHECK, generation, serial/identity, UNIQUE, PRIMARY KEY and indexes.
Owner lookup shadows permanent relations; peers never share values.
ON COMMIT default/PRESERVE retains rows, DELETE clears rows/index membership
without restarting owned sequences, and DROP removes the real dependency closure.
Rollback/savepoints restore descriptors/actions/roots; physical end removes owner
TEMP, not pooled borrowing. Native persistent Dump excludes TEMP; Reset/Restore
preserves exact live TEMP and bounded stored-reference provenance.
Retained text DEFAULT/CHECK/generated expressions keep lawful captured collation
manifest epochs: exact live physical-owner TEMP capabilities or validated pinned
seeds only, never a general stale-reference waiver. Ordinary subsequent DDL and
mutations still validate those manifests. Stored expression metadata does not
retain historical catalog roots for diagnostic name lookup.
Foreign TEMP creation fails42P16; native qualified peer lookup retains42501.
Permanent transactional CREATE/DROP, TEMP views and GLOBAL/LOCAL remain excluded.
TEMP-source SET SCHEMA fails0A000 `cannot move objects into or out of temporary schemas`.
See [temporary table contract](TABLES.md#temporary-tables--change-note).
Smoke: `make check RUN='^TestReferenceTemporaryTableFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceTemporaryTableFeatureSmoke$'`.

The four selected sequence functions and minimal regclass casts reuse the common typed scalar binder/eager evaluator. Literal regclass lookup happens at Prepare with character positions. Nonnull regclass parameters become canonical OIDs during coherent Execute parameter import, before zero-row evaluation, runtime NULL checks or nested function effects; input errors retain portal Context without a literal Position. Numeric/dash inputs require no existing relation. NULL arguments are strict and do not open a sequence. Exact session-before-use, missing/wrong-kind, option, bound, ACL and NOTICE diagnostics have independent pinned-release fixtures. This does not admit arbitrary relationless SELECT, user-defined functions or general casts.

### Regclass relation identifiers — change note

Regclass input follows PostgreSQL qualified-name splitting, separately from the
SQL statement lexer. Unquoted punctuation and comment-looking bytes remain part
of the name; only dots and six ASCII whitespace bytes delimit components.
Quoted components preserve case/embedded dots and decode doubled quotes; names
fold ASCII and clip at 63 UTF8-safe bytes. Exact nonempty ASCII digit strings
or exact `-` select the opening-free OID path, including the existing OID codec's
octal rules. Signed/padded numeric-looking strings resolve symbolic names.
Malformed complete input rejects before effects. Qualified input accepts the
current database and rejects foreign databases/excessive dotted qualification.
Input-boundary stream: `make check RUN='^TestReferenceSequenceRegclassSourceInputStream$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSequenceRegclassSourceInputStream$'`.
Smoke: `make check RUN='^TestSequenceRegclassInputSmoke$'`.

Both `value::regclass` and `CAST(value AS regclass)` share the bounded explicit
matrix: OID/TEXT/VARCHAR/integer inputs and OID/INT4/INT8/TEXT outputs. INT4 uses
unsigned bits; INT8 checks the OID range. Direct scalar/array columns, domains,
DEFAULT/CHECK/views and writes are admitted. REGCLASS constructors share the
[array-expression contract](#array-expressions-and-partial-assignments), including
nested rectangles, explicitly cast typed empties and inferred parameters.
Scalar stored input constants retain
relation dependencies across rename/move; numeric coercion trees and opaque
array/domain-array input do not gain DROP dependencies. Ordinary prepared SQL
reanalyzes its source spelling, unlike stored definitions.
Recursive view cascades include surviving-table defaults/CHECKs bound to removed
views, while restrictive details retain each immediate provider. DROP COLUMN
also prunes definitions referencing removed views on the same surviving owner,
without discarding unrelated definitions or retained data. Immediate-provider
traversal orders diagnostics/notices even through replaced views; genuine
consumer identities order mixed siblings and same-owner CHECKs keep declaration
order within their occupied sibling slots. Failing
INSERT/UPDATE rows, including UPDATE RETURNING, use captured symbolic regclass
output recursively through domains/arrays and retain existing ACL disclosure.

Native logical replay remaps actual data/scalar Const identities and defers
eligible closed defaults until definitions exist. Pure closed BOOL/TEXT/CASE
defaults with genuine captured scalar regclass authority retain their existing
cycle deferral; sequence defaults require captured provider authority. The same
per-definition decision controls prerequisites, CREATE omission and deferred ALTER
emission. Nondeferred defaults/CHECKs/generated expressions keep mandatory shared
prerequisites. Opaque regclass-array payloads under an inline BOOL default get
replay ordering but no scalar relation DROP edge; a BOOL result without captured
scalar authority remains outside the deferred subset.
Numeric coercion expressions preserve
their numeric bits, not logical bindings under a different allocator. Opaque
array view/CHECK payloads receive replay ordering without DROP dependencies.
These process-local controls preserve retained rows and atomically reject
failed/canceled restore; they do not promise disk/network/physical pg_dump ABI.
See [regclass type contracts](TYPES.md#regclass-relation-identifiers--change-note).
Feature smoke: `make check RUN='^TestReferenceRegclassFeatureSmoke$'`
and the identical command with `REFERENCE=1` (900-second deadlines).

Valid sequence CREATE still prepares without namespace permission checks.
At execution, schema CREATE denial has no Position; ordinary CREATE validates
options first, while IF NOT EXISTS checks namespace authority before skipping
an existing relation or validating options. Literal regclass schema USAGE
denial remains an analysis/Prepare error positioned at the input literal.

Standalone selected regclass projections use the `regclass` column label and
PostgreSQL name output: visible names are unqualified, shadowed/off-path names
are schema-qualified, and identifier quoting uses the shared keyword inventory.
Missing nonzero OIDs display decimal text; zero displays `-`. The common result
exporter uses an immutable execution output snapshot without replacing internal
typed OID datums or adding general OID casts/catalog queries.

Sequence metadata, transactional generator replacements and INSERT/UPDATE/DELETE row versions publish together at Finish/COMMIT through the same immutable prepare/validate/install frontier. Ordinary nextval/setval effects and physical-session memories are outside rollback. RX protects demanded nonnull function calls; ALTER uses SRX and administrative rename/schema changes use AX. Sequence row reads use AS and retain captured data/types through subsequent changes. Native log_cnt0 intentionally differs from PostgreSQL WAL prelogging counters; tests assert both independently.

Owner-TEMP CREATE/DROP TABLE, sequence DDL, COMMENT ON SEQUENCE and the selected ALTER TABLE action lists above are bounded exceptions to the general explicit-transaction DDL exclusion. TEMP namespaces are session-private, shadow persistent relations, and survive persistent Reset/Restore until physical end. Controls preserve settings/roles/temp memories, exclude TEMP objects from Dump, serialize effective persistent options and called state, and reject invalid staged replacement atomically.

The internal native catalog-fixture replacement seam imports a persistent source
projection only. Repeated self replacement preserves destination live TEMP once;
foreign or already-retired source TEMP is never imported or resurrected.
Destination TEMP identity, builtin type dependencies, generator, local values
and cache remain exact; imported persistent metadata and detached values use
coherent type identities without mutating retained source catalogs or rows.
Public Restore still rejects TEMP declarations rather than importing them.

Prepared sequence SQL is source, not a permanently retained regclass datum.
Its complete literal group reanalyzes through the existing binder at Execute
start on sequence relation-plan stamp, effective namespace changes or namespace
catalog invalidation. An
already-running function retains its analyzed OID across a queued RX wait.
Rename/move missing original names
fail42P01 without allocation; ordinary SQL DROP/recreation may resolve a new
identity. Numeric OIDs retain their bits and missing opening failsXX000.
Ordinary generator changes and relation shadow creation alone preserve held
analysis. Explicit sequence options rewrite even unchanged values; actual
owner/persistence transitions and sequence ACL publications invalidate.
OWNED BY-only, same-owner and same-persistence forms preserve the stamp;
bare ALTER SEQUENCE without an action or option is a syntax error.
Effective paths compare ordered namespace identities/implicit flags,
not raw setting text; unchanged execution reuses the session path cache.
Namespace publications invalidate both that cache and held sequence analysis,
even with an unchanged effective path, through an independent bounded scalar
without consuming descriptor revisions. Exhaustion rejects publication
atomically, including namespace ACL updates and schema drop closures; native
catalog replacement retains its separate epoch authority.
First committed TEMP namespace insertion uses this same checked authority before
catalog/generator/row/session installation. Rollback or relation-only creation
in an existing TEMP namespace does not publish a namespace event. Physical
EndSession retires native logical TEMP ownership without namespace invalidation
or epoch advance: PostgreSQL RemoveTempRelations preserves the namespace tuple
for backend-slot reuse. Native retirement installs detached coherent state;
previously captured catalog views and result rows remain immutable.
Temporal captures, parameter/result signatures and returned cursors remain
immutable. Native persistent Reset/Restore keeps its separate epoch-fence
contract; exact live caller-owned TEMP provenance alone may refresh.
Native sequence-only reanalysis preserves existing Prepare-time dynamic temporal
captures. PostgreSQL full-query reanalysis can recapture `TIMESTAMP 'now'` at
its reanalysis transaction start; this is an independently asserted bounded
native temporal distinction, not full prepared-query parity or a general
temporal cutover. Static temporal values and parameter/result/sequence contracts
remain exact.

RX is demand-driven after once-only argument evaluation. A queued rename
continues opening/allocating the original OID; the next held Execute reanalyzes
the original missing name and fails42P01. Queued DROP instead fails the running
opening withXX000/no Position, followed by42P01 with literal Position on the
next Execute. No function arguments or prior allocations are replayed.
Filtered/zero-row targets do not open/lock a sequence. These four strict builtins
simplify a known constant NULL argument to typed INT8 NULL, eliminating even
volatile sibling arguments; a runtime column NULL retains once-only nested
argument effects but skips the outer sequence opening/RX. This bounded rule is
not a generic SQL function optimizer.

Sequence DDL/restart and transactional INSERT/DELETE publish their catalog,
generator, appended-row and deletion-end roots atomically at one commit frontier.
DELETE predicates use the same sequence argument/preflight/demand contracts as
other scalar consumers; demanded nextval/setval effects survive row rollback.
DELETE of a sequence itself fails at execution with `42809` / `cannot change
sequence "s"` before predicate allocation. Retained row results and new
INSERT SELECT/Dump consumers respectively retain their captured snapshot and
observe only live survivors. Feature smoke:
`make check RUN='^TestReferenceDeleteUsingMigratedSequence'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDeleteUsingMigratedSequence'`.
The migrated sequence source/control stream is
`make check RUN='^TestReferenceSequenceSourceConsumerStream$'`;
its pinned counterpart adds `REFERENCE=1`.

## Timezone-aware temporal extension

TIMESTAMPTZ stores instants; TIMETZ stores clock plus fixed offset.
AT TIME ZONE/AT LOCAL and selected datetime casts/comparisons reuse the scalar
binder and captured session environment. TimeZone/DateStyle use existing
SET/SHOW/RESET/current_setting/set_config and transaction/savepoint lifecycle.
AT TIME ZONE is strict: a NULL temporal operand or NULL zone produces a typed
NULL result. AT LOCAL likewise preserves the result type for a NULL operand.
An unreachable CASE branch does not evaluate its zone conversion.
IANA rules, historical abbreviations and POSIX/fixed offsets are deterministic
embedded pinned-image data, not host lookups. DST gaps/folds follow PostgreSQL's
accepted transition-side preference. See [types](TYPES.md#time-zone-aware-values)
and [settings](SETTINGS.md#temporal-settings) for signs, precision, adapter
representation and DateStyle ordering. Dump/Restore remains native logical replay.
Standard/postfix temporal identity casts and temporal-to-TEXT output casts are
supported in existing scalar positions, including direct and prepared queries.
For example, `TIMESTAMP 'epoch'::timestamp` preserves the timestamp value and
`CAST(TIMESTAMP 'epoch' AS text)` renders `1970-01-01 00:00:00` under ISO DateStyle.
This does not expand unrelated cast families: integer-to-VARCHAR remains excluded.
Timezone name/abbreviation catalog views remain excluded.
Selected [INTERVAL arithmetic](#date-and-time-arithmetic--change-note) and
the five SQL current-date/time producers are supported separately.
The timezone-only INTERVAL
operand of SET TIME ZONE remains bounded independently of the general
[interval value type](#interval-values--change-note).
All nine `timezone` overloads are supported: text/interval plus TIMESTAMP,
TIMESTAMPTZ or TIMETZ, and unary session-zone forms. `AT TIME ZONE` / `AT LOCAL`
bind those same signatures, including preferred UNKNOWN resolution, implicit
DATE/TIME coercions and unrestricted result typmods. They are strict in every
formal; a constant NULL suppresses execution after complete child analysis,
whereas a runtime NULL does not suppress evaluation of other arguments.

`date_add` and `date_subtract` accept `(timestamptz, interval)` or
`(timestamptz, interval, text)`. Calendar months, then days, use the execution
session zone or the explicit full zone; elapsed microseconds are applied last.
Dynamic abbreviations such as MSK select their owning full zone for calendar
arithmetic, not a permanently fixed abbreviation offset. Explicit zone lookup
precedes infinity/zero arithmetic handling. Timestamp conversion infinities
instead bypass zone validation. Unary timezone, text/TIMETZ conversion and
two-argument calendar forms are Stable; the other selected forms are Immutable,
subject to child/coercion volatility. Symbolic defaults and stored definitions
retain that distinction across prepared execution and native logical replay.

Feature smoke: `make check RUN='^TestReferenceDatetimeZoneFeatureSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceDatetimeZoneFeatureSmoke$'`.


## Session and database identity — change note

The scalar pipeline supports bare `current_catalog`, `current_role`,
`current_user`, `session_user`, `user`, `system_user` and ordinary
`current_database()`. The first six identities excluding system_user return NAME;
system_user returns TEXT SQL NULL for trusted embedded sessions. Keyword labels
retain their lowercase spellings. The real catalog functions are current_database,
current_user, session_user and system_user: qualified/quoted calls use namespace
lookup, while unqualified keyword parentheses are syntax errors. There are no
ordinary current_catalog/current_role/user function aliases.
NAME has no arithmetic overloads: identity or NAME-column arithmetic reports
`42883` with the missing operator signature, operand types, operator position
and PostgreSQL cast hint, rather than a native unsupported-provider diagnostic.
Identity expressions follow the existing cast boundary: TEXT `system_user` can
participate in supported conditionals and explicit TEXT casts, but explicit
NAME-to-TEXT casts are not admitted (`0A000`). NAME identity assignment to TEXT
columns and TEXT defaults remains supported; this feature adds no cast families.

Identity-derived singleton comparisons/casts/conditionals are admitted without
FROM; unrelated general singleton expressions remain outside the bounded dialect.
Table projections/predicates, assignments, defaults, RETURNING and views reuse
the typed scalar path. Plans store syntax, not identity values: every execution
captures effective/session/database identity, and retained results remain detached.
Native Dump/Restore preserves expression syntax and stored data, not session
identity, authentication or database naming.
Supported quoted identity calls in views are dumped as canonical
`pg_catalog` calls so replay never turns them into forbidden bare keyword calls.
Ordinary identity-call arguments are analyzed before namespace lookup, preserving
undefined-child diagnostics even when the callee schema is missing or denied.

Database naming is the exact opaque connector DSN converted through NAME input:
empty remains empty, whitespace and URL-looking bytes are literal, invalid UTF8
fails, and valid names silently clip at a complete-codepoint 63-byte prefix.
The exact untruncated DSN still keys engine isolation. No database registry or
credential/URL parsing is added.

SET [SESSION|LOCAL] ROLE accepts identifiers/strings, NONE, TO/= assignment,
and RESET ROLE. Only CurrentUser changes. Eligibility uses SessionUser self,
superuser or cycle-safe all-SET membership reachability; ADMIN/INHERIT alone do
not authorize. Quoted/string lowercase `none` is the sentinel; uppercase `NONE`
is a real role name. Bare DEFAULT is a syntax error, TO/= DEFAULT resets, and
quoted/string `default` is a real name. SESSION persists on commit and restores
on rollback; LOCAL expires on either boundary. Savepoints restore effective role
and the retained SESSION candidate. Outside-block LOCAL warns25P01, validates
the target and leaves no lasting effect. RESET/rollback restore cached validated
references even after SET-option revocation; fresh named selection checks live
membership. Public connectors start as session_user; configured role defaults
are a validated internal SessionStart hook, not a public API or registered GUC.
There is no routine invocation frame yet, so SECURITY DEFINER role prohibition
remains contingent on that contract. SET SESSION AUTHORIZATION still uses the
original authenticator and resets both mutable identities; it remains excluded
inside explicit transactions. SQL membership administration is supported;
authentication remains host-asserted.
GRANT/REVOKE role memberships support ADMIN, INHERIT and SET, omitted-option
preservation, grantor-specific origins, cycle prevention and dependent
CASCADE/RESTRICT. CREATE ROLE/USER/GROUP initial IN ROLE/ROLE/ADMIN clauses and
ALTER GROUP ADD/DROP USER share the same atomic authority. New SET defaults true,
ADMIN false, and INHERIT follows the member's current role attribute; explicit
edge options override defaults and old edges are not rewritten by ALTER.
INHERIT paths supply ordinary object ACL and owner rights; special attributes
are never inherited. See [membership contracts](AUTHORIZATION.md#membership-options-and-provenance).
Membership utilities Prepare without lookup or permission caching, affect zero
rows and reject explicit native transactions0A000. Read-only Execute uses
GRANT ROLE/REVOKE ROLE/ALTER ROLE tags and25006 before lookup. SQL object grants,
membership catalogs and transactional cluster-role publication remain excluded.

### Role attributes — change note

CREATE ROLE/USER/GROUP and ALTER ROLE/USER support SUPERUSER, CREATEDB, CREATEROLE,
LOGIN, INHERIT, REPLICATION and BYPASSRLS positive/negative flags, CONNECTION LIMIT
signed_int4, PASSWORD string/NULL, ENCRYPTED PASSWORD string and VALID UNTIL string.
ALTER targets include CURRENT_ROLE/CURRENT_USER/SESSION_USER. Empty options/WITH
are valid; duplicate canonical attributes fail42601, limits below -1 fail22023,
malformed timestamp values use22007–22009, and missing ALTER targets use42704.
ROLE/GROUP default NOLOGIN, USER LOGIN; otherwise flags default false except
INHERIT=true, limit=-1, password=NULL and absent expiry. ALTER retains omissions.

CREATE uses direct effective SUPERUSER/CREATEROLE; ordinary creators atomically
receive ADMIN=true with INHERIT=false/SET=false. Ordinary ALTER normally requires
direct CREATEROLE plus ADMIN; PASSWORD-only effective-self changes are the exception.
SUPERUSER targets/toggles require SUPERUSER; selected CREATEDB/REPLICATION/BYPASSRLS
changes require matching direct attributes, never inherited membership power.
See [complete attribute authority](AUTHORIZATION.md#role-attributes).

Passwords become real fixed SCRAM-SHA-256/4096 metadata with random salt and
derived keys; recognized encoded passwords retain their bytes and NULL/empty clears
them. VALID UNTIL is captured-setting timestamptz metadata with absent/finite/infinite
states, not trusted role expiry. Native authentication remains host-asserted.
Derivation checks operation cancellation throughout supplied iteration counts:
valid preencoded secrets are not capped or reclassified. Cancellation preserves
the prior descriptor and permits same-owner recovery and physical teardown.
The contextual attribute words remain ordinary unquoted names in membership lists.
Physical limits count original authenticators, including idle pools; -1 is
unlimited, 0 denies ordinary admission, close releases and reuse adds no slot.
Superusers bypass limits but not NOLOGIN; new NOLOGIN admission fails FATAL28000.
Role rename, ALTER ROLE SET/RESET, password handshake/settings and pg_authid
remain excluded; no database, replication or RLS executor is added. DROP/COMMENT
retain existing superuser gates. CREATE/ALTER role utilities remain native
explicit-transaction0A000 boundaries, with read-only25006 before lookup.
Observed public smoke reached creator ADMIN, omission, expired trusted admission,
physical release, NOLOGIN and fresh held authority; repeat with
`make check RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'`
and the same command with `REFERENCE=1`.


Feature smoke: `make check RUN='^TestSessionIdentitySmoke$'`.
Pinned reference: `make check RUN='^TestReferenceSessionIdentity.*$' REFERENCE=1`.
The normal oracle authenticates with SCRAM and independently reports its method;
a dedicated real trust connection demonstrates system_user NULL.
The trust witness also runs independently under lazy fixture startup:
`make check RUN='^TestReferenceSessionIdentityTrust$' REFERENCE=1`.
Identity cancellation regression entrypoints include
`TestSessionIdentityCanceledRolePublication`,
`TestSessionIdentityCanceledDefaultWriteFinish`, and
`TestSessionIdentityFailedAndCanceledControlsPreserveState`; these cover the
role publication fence, staged default writes, and failed/canceled controls
without publishing replacement data or changing retained results.


## Ordinary B-tree indexes — change note

Native CREATE INDEX supports default/USING btree with simple or same-table scalar
expression keys, including `lower(v)`, parenthesized operators and repeated
expressions, ASC/DESC, NULLS FIRST/LAST, owned COLLATE/operator classes and INCLUDE.
The limit counts 32 total key plus included attributes. INCLUDE is projectable
noncomparison payload and needs no ordering provider; it neither guides search
nor affects uniqueness. Roots retain only sorted key vectors and live row/version
identities and are maintained through common row/catalog publication, not
metadata-only success. Explicit classes use their actual owned semantics, not
spelling-only admission. Scan/filter/sort remains query authority; no optimizer,
physical index-only scan, disk algorithm or unordered-result ordering promise is added.

Ordinary build uses Share, checks owner before waiting, accepts compatible
same-incarnation grant-time revisions and revalidates namespace/role authority.
Existing replacement/epoch and utility lock/cancellation boundaries apply.
The native exact-incarnation utility contract retains its documented
search_path-shadow distinction; no replacement is reacquired by name.
CREATE INDEX is excluded inside native writable explicit transactions (0A000),
whereas PostgreSQL ordinary transactional CREATE succeeds and rolls back.
Read-only execution is independently 25006 before target lookup.

Duplicate names are 42P07 in the shared relation namespace; missing target/key
are 42P01/42703. PostgreSQL18.0 missing simple-key diagnostics have no Position,
unlike newer development-source behavior. Binding precedes duplicate-name
checking, including IF NOT EXISTS. Semantic prepared errors occur at Execute;
syntax diagnostics retain one-based character positions. Unowned access methods
and unowned providers are bounded
capability exclusions, not ignored clauses. Invalid INCLUDE decorations get
42P17; expressions in INCLUDE get 0A000; excess total attributes get 54011.
Class parameter validation follows duplicate-name/IF NOT EXISTS handling.
Ordinary DROP/rename/rebuild utilities are described below; unique attachment
and broad index SQL inquiry catalogs remain excluded.

Ordinary USING hash is independently owned: one simple or immutable scalar
expression key, real duplicate-preserving non-NULL buckets and a narrowly proved
current-view strict equality probe with original-comparison residual. It supplies
neither range/order/NULL search nor a physical PostgreSQL hash implementation.
Mixed/coerced/provider-mismatched comparisons and unproved snapshots/private data
retain ordinary evaluation. Hash UNIQUE/INCLUDE/multikey/direction requests get
method-specific PostgreSQL errors. Complete reloptions validation precedes key
binding; a valid physical request is excluded only after class/type/collation/
direction, collision/IF NOT EXISTS and builtin class-parameter checks, at retained
WITH Position. Array hash classes remain recognized native0A000 boundaries.
TYPE, deparse, Dump/Restore and maintenance preserve actual method/class identity.
See [hash SQL](SQL.md#hash-equality-indexes) and
[hash design](design/PG18-INDEX-HASH.md).
Native HASH WHERE, including syntactic WHERE TRUE, fails at Execute after quiet
Prepare with unpositioned0A000 `partial indexes are unsupported`. Existing HASH
UNIQUE/INCLUDE/multikey errors precede this guard; predicate binding and root
construction never consume a HASH predicate.


Expression keys use the existing scalar binder, type/collation providers,
constant simplifier and evaluator, not an index-specific interpreter. Admission
checks immutability on surviving branches: eliminated random() CASE/COALESCE
branches do not reject an otherwise immutable key, but surviving stable/volatile
providers fail 42P17 without Position in PostgreSQL18.0. Binding still checks all
original branches. Aggregates fail 42803 and subqueries fail 0A000 at their call/
SubLink character positions; unknown expression columns fail positioned 42703.
Prepare is syntax-only and these utility diagnostics occur at Execute.
Mixed concatenation uses the concrete output provider after PostgreSQL's SQL
wrapper specialization: `integer_column || 'x'` is immutable, while DATE,
TIMESTAMP, TIMESTAMPTZ, INTERVAL and REGCLASS output retains stable dependence.
TIME/TIMETZ and BYTEA follow their declared immutable output procedures, not
an inferred classification from environment access. Domain output follows its
base provider. Original conservative expression metadata remains unchanged;
the surviving-tree check separately includes intrinsic planned dependence.
Build and common row publication evaluate derived keys before atomic install;
evaluation failures leave rows, descriptors and predecessor roots unchanged.
Definitions retain original analyzed trees/dependencies for current-name
rendering and TYPE rebinding; dump contents never serialize derived root history.
Ordinary unique expression indexes retain their explicit0A000 admission boundary.
Concurrent standalone UNIQUE admits computed keys through the same immediate
tuple checker and transaction-owned key claims, without changing the simple-slot
contract of constraint backing indexes. Ordinary nonunique expression support
is unchanged.

Dump writes actual definitions/current names, canonical explicit provider identities
and INCLUDE after parent data. Reset removes persistent indexes; Restore privately
rebuilds destination membership. TYPE reconstructs the whole dependent definition:
old-default classes become new defaults; genuinely nondefault classes/collation
overrides are retained and revalidated. Original expression definitions rebind
against the final shape. Dropping a key dependency or payload removes its index.
See [clause design](design/PG18-INDEX-CLAUSES.md). Feature smoke:
`make check RUN='^TestReferenceIndexClausesFeatureSmoke$'`;
the identical RUN with `REFERENCE=1` adds the independent pinned PostgreSQL oracle.
Shared smokes: `make check RUN='^TestReferenceBtreeIndex(Regression|Controls|Lifecycle)$'`;
use the same RUN with `REFERENCE=1` for independent PostgreSQL18.0 outcomes.
Reference families are `TestReferenceBtreeIndex...`, `TestReferenceExpressionIndex...`
and `TestReferenceIndexClauses...`.

Ordinary partial B-tree indexes add an immutable same-table Boolean WHERE definition. Aggregates
and subqueries get predicate-specific 42803/0A000 diagnostics; surviving mutable
providers get unpositioned 42P17. Predicates bind before keys. Only TRUE qualifies
(NULL does not); excluded rows never evaluate derived keys, including private
statement preflight. Failed predicate/key evaluation leaves the batch unchanged.
Window calls get predicate-specific 42P20 after signature analysis; a window
function without OVER retains its 42809 diagnostic. Neither path creates an
index or changes data, and utility Prepare remains quiet.
Original dependencies survive simplification and join current-name deparse,
drop, TYPE rebinding and destination Restore root rebuilding.
Associated-array name displacement retains its OID and retags both predicate
trees with current row types, including indexes whose only changed type consumer
is the predicate. Retained native cursors keep their original type metadata and
values; member uniqueness and outside-array admission remain unchanged.

Eligibility is strong exact immutable conjunction matching on unresolved typed
trees. Every predicate atom must match a query atom; OR is not split, and no
inequality/algebra theorem prover is supplied. Parameter atoms cannot prove fixed
predicates, but unrelated parameters do not disable an independent exact match.
For a sole direct nonlocking table SELECT only, a root captured under the same
mutex as the exact committed visibility can reject nonmember row/version pairs
before the full residual WHERE. Private catalog/writes/deletes, stable/historical
views and stale epoch/generation/prefix/ends/definition always fall back to the
ordinary scan. Retained roots/masks and selected rows remain immutable across
later publication or replacement. There is no key traversal/cost/order optimizer.
Partial UNIQUE uses the same owner as ordinary/attached uniqueness. Membership
precedes key/null/hash/reservation/probe work and filters private/staged rows and
accepted prefixes without hiding future UPDATE candidates. Old member claims
survive leave/delete until outcome; outside rows claim no partial key. DISTINCT
and NND apply only to members. Analyzed standalone BOOL TRUE normalizes to nil
predicate/no WHERE and ordinary strong key/history semantics. Retained logical,
comparison and converting-cast predicates remain partial even when execution
folds TRUE; partial-only key/predicate UPDATE does not stamp strong-key history.
Predicate-dependent TYPE validates final rebound definitions and projected
committed/private rows at Execute, retaining current disclosure authority.
Materialized roots include exact immutable row/version membership maps, including
empty maps; changed predicate identity forces rebuild. Validation-only PREPARE
publishes no roots/maps; rebuilt predecessor uniqueness is checked before delta.
INSERT SELECT preserves eager sort/set demand and lazy admission before the next
completed source row; excluded rows still evaluate demanded expressions/defaults.
Predicate/INCLUDE values do not become disclosed unique keys. Shared scenarios:
`make check RUN='^TestReferencePartialIndex'`;
use the same RUN with `REFERENCE=1` for independently asserted PostgreSQL18.0.
PostgreSQL also admits partial HASH indexes; native retains its unpositioned
0A000 exclusion at Execute for HASH WHERE, including WHERE TRUE. Ordinary HASH
is unchanged; its UNIQUE/INCLUDE/multikey diagnostics retain precedence.
Concurrent WHERE likewise remains a native capability boundary: quiet Prepare,
unpositioned Execute0A000 `partial concurrent indexes are unsupported`, including
syntactic WHERE TRUE. Ordinary CONCURRENTLY and ordinary partial creation are
unchanged; no concurrent predicate/snapshot or computed partial-unique consumer
is introduced. Shared direct/held Context/plain boundary and both same-name
recoveries: `RUN='^TestReferencePartialIndexConcurrentBoundary$'` in both modes.
The consolidated source/reference union keeps actual plain maintenance and
transaction consumers beside recorded Context owners. Standalone CREATE UNIQUE
INDEX now supports simple/composite keys, default/explicit NULLS DISTINCT and
trailing NULLS NOT DISTINCT. Duplicate builds fail atomically23505; runtime
INSERT/UPDATE reuse table UNIQUE's current-root checks, wait/outcome recapture and
SameDatum key-transition history, independently of a retained RR query snapshot.
Compatible directions, NULL ordering, owned providers and INCLUDE remain real
definitions; payload never affects equality. BPCHAR classes on TEXT/VARCHAR fail
explicit0A000 because their trimmed equality disagrees with existing key hashes;
ordinary admission is unchanged. A nonunique index may retain NULLS NOT DISTINCT
for metadata/deparse without enforcing it. Standalone indexes have no constraint
OID/pg_constraint row: direct DROP frees enforcement and rename changes only the
index/current diagnostic name. TYPE, REINDEX and native replacement rebuild the
same roots; Dump emits standalone definitions once after parent/data, while true
attachments remain inline. Current SELECT on the table or all key columns governs
build and runtime key Detail, including revoked owners. Native explicit writable
utility0A000/read-only25006 boundaries remain unchanged.
Feature smoke: `make check RUN='^TestReferenceUniqueIndexFeatureSmoke$'`;
repeat with `REFERENCE=1` for the pinned18.0 oracle.

### Concurrent B-tree creation — change note

`CREATE [UNIQUE] INDEX CONCURRENTLY [IF NOT EXISTS]` admits the same owned simple,
immutable computed, COLLATE/class, direction/NULL-order and INCLUDE surface.
Persistent SUE serializes same-table builds but permits ordinary readers/writers.
Initial false/false/true descriptor publication precedes captured writer waits;
a real first root and Ready publish atomically, then validation and database-wide
endangered snapshot waits precede Valid. Completion waits share the existing
hard deadlock graph and exact top-level ownership, not a queued parent Share.
Pre-ready admitted writes may commit after Ready without retroactive enforcement;
later Ready unique writes are enforced even while Valid is false.
Failed/canceled phases persist until DROP or successful explicit rebuild.
Unrelated catalog/row-shape work does not complete an invalid build.
Explicit concurrent execution fails25001; read-only25006 precedes that gate.
Dump rejects persistent invalid indexes before any output. Isolated Restore uses
the normal public UNIQUE parser and retained import authority, including NULLS
NOT DISTINCT; no UNIQUE-only parser option or execution gate exists.
Partial indexes and concurrent DROP/REINDEX remain unsupported.
See [authority and ownership](design/PG18-INDEX-CONCURRENT.md).


Native `InspectIndex(ctx, db, schema, name)` is a read-only control, not a SQL
catalog. Exact decoded names return detached descriptor `Ready`, `Valid`, `Live`
flags; existing completed ordinary/backing indexes are healthy. Missing schema,
missing index and wrong-kind errors remain ordinary Go control errors. An
unwrapped native pool is required; this adds no optimizer/query-plan behavior.
Its separate `WaitingForOlderSnapshots` Boolean observes actual live older-cohort
admission after validation ownership drops; it is neither a persisted descriptor
phase nor proof of a registered completion request.
`WaitingForWriters` observes the same coordinator after its granted cohort is
frozen, with Ready distinguishing first and second writer admission. Both live
cohort observations clear between phases and on owner end/epoch replacement;
descriptor flags alone are not proof that the writer cohort has been captured.


## LIKE and ILIKE — change note

LIKE, NOT LIKE, ILIKE and NOT ILIKE are whole-string predicates. Their operator
spellings are `~~`, `!~~`, `~~*` and `!~~*`, also available through
`OPERATOR(pg_catalog.symbol)` and the existing namespace/authorization rules.
Keyword predicates accept `ESCAPE`; symbolic predicates do not accept that suffix.
`%` consumes zero or more characters and `_` one UTF8 code point, including
newlines. Regex punctuation has no special meaning. No Unicode normalization
or grapheme counting is performed.

Backslash is the default escape. ESCAPE is empty (disable escaping) or one TEXT
code point; custom escapes quote the following character, including themselves.
Escape normalization precedes ASCII-only ILIKE folding under admitted
default/C/POSIX collations. Thus É/é do not match case-insensitively. C and POSIX
are distinct identities: conflicting implicit nondefault operands, including an
escape operand, raise 42P22 when matching non-NULL values. Implicit default yields
to a nondefault identity. Scalar COLLATE also admits registered user descriptors;
an explicit annotation, including default, wins over implicit conflicts.
Equal explicit identities combine; different explicit identities fail analysis
and Prepare with 42P21 at the second conflicting COLLATE, even for NULL or empty
input. Collatable casts, conditional results and supported string outputs retain
this expression authority. Stored view columns expose implicit physical identity.
Other locale/nondeterministic providers remain excluded.
Plain LIKE supports both Unicode capabilities. ILIKE/NOT ILIKE (`~~*`/`!~~*`)
with effective pg_c_utf8/pg_unicode_fast semantics (including user copies) fails native0A000,
`ILIKE is unsupported for this collation`, positioned at the operator during
analysis/Prepare. The guard uses the combined collation, including the RHS and
escape wrapper, after explicit-conflict resolution. Explicit C on either side
overrides an implicit Unicode column and allows ASCII ILIKE; existing42P21 and
indeterminate42P22 diagnostics remain authoritative.

BPCHAR matching preserves the left operand's physical trailing spaces; BPCHAR
patterns/escapes convert to TEXT and trim those spaces. VARCHAR retains them.
Existing BYTEA LIKE/NOT LIKE uses octets and an empty or one-octet escape; BYTEA
ILIKE has no signature (42883). No binary producers or casts are added.
Negation complements successful booleans only; NULL inputs produce BOOL NULL.

Invalid escape length is 22025 `invalid escape string`, with Hint
`Escape string must be empty or one character.`. A demanded trailing escape
is 22025 `LIKE pattern must not end with escape character`; mismatches or
exhaustion can return false without reaching it. Runtime errors have no query
Position. Incompatible operators/escape argument types fail analysis/Prepare
with 42883 at LIKE/ILIKE or NOT, using canonical symbols/function signatures.
Prepare does not perform matching. Execute preflights immutable row-independent
normalization, even on empty relations or a constant NULL lhs. Row-dependent
normalization is not run on empty/filtered input. A variable NULL lhs still
evaluates the escape child; a constant NULL lhs can eliminate row-dependent
matching after independent normalization preflight. Held executions never retain
normalized parameter values.

Matching participates in shared CHECK/view expression consumers, deparse/rebind
and atomic public Dump/Reset/Restore. Public POSIX regex APIs and additional
locales are not included. SQL SIMILAR support is documented below.
Feature smoke: `make check RUN='^TestReferenceLikeLegacyMigration$'`.

## SIMILAR TO and SQL regex substring — change note

Full-string `SIMILAR TO`/`NOT SIMILAR TO` support SQL `%` and `_`, alternation,
noncapturing user groups, greedy/lazy `*`, `+`, `?` and counted repetition
(PostgreSQL bounds 0 through 255), brackets/ranges, standard character classes,
collating elements, equivalence classes, word constraints and documented escape
extensions. Outside brackets `.`, `^` and `$` remain literal SQL punctuation.
Wildcards include newlines and advance UTF8 code points, not bytes/graphemes.
Default escape is backslash; an empty escape disables it; a custom escape is
one code point. The release consumes a terminal escape. Escape-double-quote
separators outside brackets create the sole capture; more than two raise 2200C.
User parentheses do not create numbered captures. Backreferences target only
that separator capture; malformed references and other regex syntax raise 2201B.

`substring(source SIMILAR pattern ESCAPE escape)`, the older FROM/FOR spelling,
and ordinary/qualified three-TEXT `substring` share SQL-regex extraction.
They require a complete match and return the selected separator capture, or
the full source when no separator occurs. Empty captures are empty TEXT,
not NULL; an absent separator capture on a winning cross-section alternative,
no match and NULL arguments return TEXT NULL. Source BPCHAR coerces
to TEXT (trimming padding); predicate BPCHAR keeps its physical source padding.
Pattern/escape BPCHAR coercions use the existing trimming contract.
Repeated groups select the final iteration's capture when it participates;
a final alternative bypassing that capture clears it to NULL. Greedy/lazy
partition choices follow the release's hierarchical subexpression preferences,
not first-success backtracking or a post-match capture heuristic.

Predicate classes use the immutable provider capability of default/C/POSIX/
pg_c_utf8/pg_unicode_fast and registered user collations, not an object's OID.
Copies retain the same ASCII/C.UTF-8/PG_UNICODE_FAST class distinctions and
pinned Unicode16 data, never host Unicode tables. The independent copied-provider
witness is `RUN='^TestReferenceCollationSimilarCopiedUnicode$'` in both quickcheck
modes.
The SQL substring wrapper's inner matcher uses database-default C even if
arguments carry explicit Unicode builtin identity; output collation metadata
still follows the established binder contract. Explicit conflicts fail in
binding; demanded predicate implicit conflicts raise 42P22 with the English
COLLATE hint. Normalization and compilation remain separate: invariant bad
ESCAPE can fail even on empty input, whereas a malformed row-dependent matcher
is compiled only when demanded. NULL/dead CASE/COALESCE branches follow the
release-oracle demand matrix, without treating malformed regex as no match.

The provider is independent of any public POSIX matcher. No POSIX operator or
function, arbitrary locale, two-argument regex substring, or positional TEXT
substring capability is introduced. Prepared plans retain descriptors/provenance
only; execution-local active-pattern state cannot leak parameter values,
captured offsets, NULL or compilation failures into held re-execution.
SQL regex nodes participate in current query/group/order, staged DML, CHECK,
DEFAULT, generated-column/index, view dependency/rebind and control consumers.
Only selected three-TEXT SQL-regex extraction is admitted as a function DEFAULT;
positional TEXT, two-argument POSIX, BYTEA and unrelated function DEFAULT
boundaries remain unchanged. Defaults stay symbolic until an omitted value
demands evaluation, so an explicit INSERT value does not evaluate a malformed
extraction default. Grouping identity ignores the four extraction spellings,
while stored SQL preserves their presentation provenance.
Native Dump retains symbolic SQL SIMILAR forms; PostgreSQL deparse retains its
canonical helper/operator presentation. Failed/canceled replay cannot publish.

**Security/complexity:** hostile patterns may consume substantial time and
memory, especially captures/backreferences and nested quantified ambiguity.
Use a context deadline for untrusted patterns. Native work polls context at
bounded primitive-work intervals, uses execution-local reusable state, and
checks a 64 MiB per-evaluation canonical/IR/scratch/state-memory ceiling before
allocation (caller-owned input excluded). Replacement drops obsolete cache
ownership before allocating; normalized canonical backing is counted once with
the active program, under one allowance, not a second translation allowance.
Strict NULL, failure and cancellation invalidate active ownership. Compilation
resource failures are 2201B
`invalid regular expression: regular expression is too complex`; runtime
resource failures are 2201B `regular expression failed: regular expression is
too complex`. This is a native boundary, not PostgreSQL's identical cutoff.
There is no arbitrary semantic pattern-length/CPU cutoff or universal linear
time claim; capture-free regular patterns use state-set execution.

Independent reference families: `^TestReferenceSimilar`. Feature smoke:
`make check RUN='^TestReferenceSimilarFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`. Both use a 900-second invocation
deadline and independently assert rows, metadata, complete diagnostics and
atomicity. Actual-feature cancellation/recovery is covered by
`^TestReferenceSimilarActualCancellation$` and the complete family.


## Stored generated columns — change note (changelog)

Explicit `GENERATED ALWAYS AS (expression) STORED` now uses the normal typed
CREATE/INSERT/UPDATE, catalog/deparse and native-control authorities. Complete
new-row computation precedes constraints/RETURNING; direct generated-value
writes fail428C9 and DEFAULT is admitted on generated UPDATE destinations only.
Original self/generated-column references remain illegal even in dead branches;
immutability is checked on surviving planned providers. Existing current-row
scalar operators/functions/casts and unconstrained domains compose without a
second evaluator. tableoid is the sole generated-context system-column provider.
Definition binding resolves a real column named like its table before the
whole-row fallback; generated attributes still receive the generated-reference
diagnostic. Surviving mixed TEXT concatenation checks the release-declared
concrete output volatility: integer and BYTEA output (including domains) remain
legal in either direction; DATE, TIMESTAMP, TIMESTAMPTZ, INTERVAL and REGCLASS
output fails42P17 at definition. Explicit domain I/O casts check both source
output and target input, so INTERVAL-domain-to-TEXT and TEXT-to-INTERVAL-domain
casts also fail42P17. Native formatting's use of an environment does not override
the release classification. Pruned dead volatile/I/O arms remain legal, and
destination assignment remains separate from explicit expression coercion.
Native Dump retains declarations and emits DEFAULT instead of computed literals;
replacement remaps owner-dependent stored cells without changing held snapshots.
Existing ALTER DEFAULT/TYPE and dependent DROP paths guard definition integrity.
LIKE INCLUDING GENERATED copies admitted STORED definitions, remapping owner-row
references without redirecting explicit external literals. Virtual/omitted-kind
generation, ADD generated and expression ALTER lifecycle remain unsupported;
generated destination TYPE without USING remains an explicit native boundary.
Shared scenarios: `make check RUN='^TestReferenceGeneratedStored'`,
and the same command with `REFERENCE=1`.

## Enum declaration and ordered values — change note

`CREATE TYPE [schema.]name AS ENUM ('sad','ok','happy')` is supported through
direct/prepared Exec/Query, with quiet Prepare and atomic execution publication.
The immutable scalar is distinct from TEXT and other enum declarations.
Labels are exact and case/whitespace significant, including empty labels;
empty lists are legal. The maximum declaration label size is 63 UTF8 bytes.
All six comparisons and ORDER BY use declaration order. Cross-enum comparisons
fail42883 with PostgreSQL English Hint/Position, including NULL and empty sources.
Invalid labels fail22P02; duplicate/overlong declarations and unauthorized schema
creation retain independent PostgreSQL diagnostics and never publish partial types.
Typed TEXT requires an explicit cast; unknown strings, inferred binds and nil
use contextual enum input. Explicit enum↔TEXT casts permit lexical comparison.
Modifiers/collations are rejected. Native enum metadata intentionally uses empty
DatabaseTypeName/string ScanType versus pinned pq empty/interface{}; native
values are string versus pq raw bytes. Logical type identity remains exact.
Qualified declarations/owners/ordered labels and stored dependent consumers
roundtrip through native Dump/Restore and import remapping. Held eager rows
survive replacement; old prepared logical contracts cannot retarget by name.
ALTER/DROP TYPE, pg_enum catalogs and general direct enum[] syntax/storage remain excluded.
AS (...) composites are documented separately. See [types](TYPES.md#enums--change-note).

`enum_first(anyenum)`, `enum_last(anyenum)`, `enum_range(anyenum)` and
`enum_range(anyenum, anyenum)` use exact actual enum identities, not domains.
Unary calls ignore values including typed NULL; ranges use inclusive declaration
order and NULL endpoints as boundaries. Empty/reversed ranges are non-NULL empty
arrays; empty extrema fail55000. Functions are non-strict STABLE, still evaluating
demanded arguments. Computed ranges use the existing associated array identity
and canonical transport; no direct enum[] annotation/storage capability is added.
Surviving calls fail42P17 in generated columns, index expressions and index
predicates; calls removed by conditional simplification do not block admission.
Computed range SELECTs and ordinary views remain valid. Native query-derived
enum-array destinations (CTAS and materialized views, WITH DATA or WITH NO DATA)
fail0A000 `unsupported array element type` before publication. PostgreSQL
supports those destinations. Scalar enum storage, supported domain-over-enum
element arrays and non-enum arrays retain their existing rules.
DISTINCT on a resolved enum support call fails42809 at the function name, rather
than a parser syntax error: these scalar functions are not aggregates. Result
metadata retains the native associated array name (for example `_MOOD`); pinned
PostgreSQL/pq leaves these dynamic type names empty.
See [enum function details](TYPES.md#enums--change-note).


## Index maintenance — change note

Ordinary DROP INDEX groups, ALTER INDEX RENAME and REINDEX INDEX/TABLE operate on
real logical B-trees. Repeated DROP addresses are deduplicated; late group
kind/missing/dependency failures remove nothing. Backing UNIQUE/PRIMARY KEY rename retains
both OIDs and the substantive root while updating current constraint enforcement
names; direct backing DROP remains2BP01 even with CASCADE.

The existing lock manager owns AX-parent-before-index DROP, Share-parent-before-
AX-index rebuild and SUE index rename admission. Non-index rename aliases delegate
to shared AX rename. Parent MAINTAIN permits non-owner rebuild; DROP permits
schema ownership, whereas rename also requires target ownership and schema CREATE.
Real selected/all-root rebuilds preserve rows/descriptors/version identities and
unselected siblings; no-index TABLE returns NOTICE without publishing metadata.

Native Prepare remains syntax-only. Native read-only25006 precedes lookup, ACLs
and waits; writable explicit0A000 follows before those phases. PostgreSQL supports
transactional utilities and REINDEX may resolve/wait before its read-only gate.
Physical SET/RESET, tablespace movement, statistics, valid REINDEX controls,
concurrency and global targets remain honest capability boundaries. Invalid
ordered reloptions retain their input-order diagnostics. After native transaction
gates, malformed/unknown REINDEX controls validate the complete list before
target/schema lookup, kind/ACL checks or waits, including a bad control after an
earlier valid one. Valid capability rejection stays at its existing target phase.
REINDEX uses whitespace controls, not '='; Boolean integer0/1 is not quoted
'0'/'yes'. Missing ALTER IF EXISTS skips semantic option validation.

Internal metadata projects complete stable-attribute/direction/null/collation/
actual-opclass vectors and original analyzed expression lists from one capture;
published flags are not invented invalid-index history. Total attributes include
INCLUDE repetitions, while class/collation/options contain only keys. Top-level
scalar collations use their real stable attribute/no indexprs where PostgreSQL does.
Central IndexClass owns actual identities; BindOrdering obtains default identities
from it, retaining enum/domain declaration ordering and internal Char/Regclass
without expanding excluded CHAR stored declarations or CHAR domains. All implemented
explicit classes/collations and INCLUDE payloads survive maintenance. Full/column/
pretty index deparse uses the existing PG renderer and compatibility reconstruction
owner; native Dump retains canonical replay, including admitted decorations.
The readonly `pg_catalog.pg_indexes` SQL source exposes five columns:
schemaname, tablename, indexname and tablespace are NAME; indexdef is TEXT.
Access method is inside compact schema-qualified CREATE INDEX text; native
tablespace is NULL and no unsupported physical options are fabricated.
Native disclosure requires schema USAGE plus parent table SELECT or SELECT
on any live column, intentionally unlike PostgreSQL's unfiltered view.
The operation invoker, not a stored-view owner, controls this policy.
Prepared executions use fresh catalog/ACL captures; existing eager results
remain owned through mutation and Reset/Restore. Generic native catalog reads
use latest committed state plus their own supported overlay, not an RR catalog
snapshot. SQL pg_index, node-tree lists, optimizer and physical storage behavior
remain outside this surface. See [the SQL contract](SQL.md#indexes).
See [the design](design/PG18-INDEX-MAINTENANCE.md).
Feature smoke: `make check RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders)'`;
repeat with `REFERENCE=1` for the independent pinned oracle.

## Subqueries and derived SELECT — change note

Scalar, EXISTS, IN/NOT IN, comparison ANY/SOME/ALL and row/single-row comparison
subqueries share typed binding and demand execution with ordinary SELECT.
Scalar output requires one column; no row yields typed NULL and a second row
raises21000. Quantified reductions retain SQL NULL/empty-input semantics.
FROM derived SELECT supports optional aliases/column aliases, without LATERAL.
Nearest visible query frames provide legal correlation; same-level derived
siblings remain inaccessible. Ordinary no-FROM SELECT uses a local one-row
input rather than a selected-function whitelist.

All nested sources are admitted and authorized before execution, including
ignored EXISTS targets. Eligible EXISTS may discard root target-only output,
plain GROUP BY, ordinary DISTINCT and ORDER work; actual aggregates, HAVING,
OFFSET (even zero), rowmarks and nonremovable LIMIT retain ordinary demand.
Required predicates and derived projections remain demanded; see the linked
eligibility contract. Source captures share the enclosing statement snapshot.
UPDATE/DELETE/INSERT consumers and
RETURNING use the same analyzer; DEFAULT/CHECK exclusions remain. Stored views
retain nested frames, source identities and dependencies across rename,
deparse, catalog remap and native Dump/Restore.
See [the full contract](QUERIES.md#subqueries-and-derived-select--change-note-changelog).


### REGTYPE — change note

REGTYPE (2206) and REGTYPE[] (2211) are stored identifier families, also admitted
through supported domains, casts, conditional consumers, grouping, unsigned
ordering/index comparison and existing string-format/output consumers.
Type-name input consumes a complete standalone shared-grammar entry; it is not
a SELECT wrapper or the regclass qualified-name parser. SQL aliases force
pg_catalog; ordinary/quoted/qualified names use captured type lookup authority.
Current-database three-part names are accepted; foreign databases fail0A000
before schema lookup, excessive qualification fails42601. `%TYPE` fails syntax
without a notice or column lookup. Accepted modifiers validate/discard; errors
and warnings keep their provider and literal/frame/runtime diagnostic lifetimes.

Lookup includes the released/initialized builtin inventory and actual user
table/view row identities with reciprocal arrays. Identity-only builtin entries
do not enable unsupported codecs, extra DDL, wire storage, cluster connections
or additional catalog projection sources; supported user rows are executable
composites. Ordinary sequences and indexes have no row type.
See [types](TYPES.md#regtype--change-note), [driver](DRIVER.md#regtype-input-and-held-descriptions)
and [native replay](FIXTURES.md#regtype-references-and-row-identity).

## Selected windows — change note

`row_number() OVER window` and `count(*) OVER window` admit parenthesized
PARTITION BY/ORDER BY keys and named WINDOW definitions. Both return nonnull INT8,
without physical column origin. No FROM uses the ordinary singleton; empty input
returns zero rows with full schema, unlike ordinary global count.

Default RANGE count includes the last peer of the current row; no ORDER makes
the entire partition peers. NULL partition keys group together. Window keys use
input expressions, never output labels or ordinals (`ORDER BY 1` is constant).
Names may be referenced by targets before their declaration; definition inheritance
may reference only earlier definitions. Copies cannot add PARTITION and cannot
override an existing ORDER. Even unused definitions are analyzed.

Explicit RANGE bounds admit inclusive offsets through registered B-tree support
for existing integer, floating, numeric and temporal carriers. Offset frames need
one effective ORDER expression; duplicate equivalent keys retain the first sort
direction. CURRENT ROW includes all peers, including NULL peers; empty frames
count0. Typed offsets use implicit support coercion, not key typmods or assignment
casts. TIME/TIMETZ use nonwrapping clock distances; zoned calendar bounds use the
Execute timezone. Independent offset subqueries and their catalog dependencies
are retained in inline/named/unused stored definitions. Bare OVER w admits its
frame; OVER(w) cannot copy an explicitly framed parent.

Demanded count frames reject NULL offsets22004 even on empty input; negative/NaN
offsets raise22013 only when the in-range predicate is reached. LIMIT0,
row_number-only frames and unused definitions do not evaluate offsets. An active
count frame inside EXISTS remains demanded; source/constant errors can precede
frame validation. Missing/distinct extra keys, unsupported carriers and invalid
offset expressions retain full PostgreSQL English/SQLSTATE/caret diagnostics.


Explicit GROUPS requires effective ORDER BY for every clause, including unused
named definitions. Bigint assignment-coerced offsets select complete peer groups,
not rows/key distances; all admitted order keys and NULL placements define peers.
For keys1,1,2,3,3, preceding1 counts2,2,3,3,3 and preceding-only1 counts0,0,2,1,1.
Partition boundaries and MaxInt64 offsets clip safely; reversed same-category
bounds may count0. Start NULL/negative checks precede end evaluation. GROUPS
negative offsets raise22013 `frame starting offset must not be negative` (or
`ending`) even on empty/all-NULL demanded input, unlike RANGE's support demand.
Missing ORDER retains42P20 `GROUPS mode requires an ORDER BY clause`; invalid
types/variables/aggregate/grouping/window offsets retain full English/carets.
Prepared inputs are fresh; same named frame evaluates once, distinct named
volatile frames remain distinct through captured views and native Dump/Restore.
Feature smoke: `make check RUN='^TestReferenceQueryWindowGroupsFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryWindowGroupsFeatureSmoke$'`.

Explicit ROWS selects physical positions in each partition. No window ORDER is
required; CURRENT ROW selects only the current occurrence, not all peers.
Three ordered rows with `BETWEEN 1 PRECEDING AND CURRENT ROW` count1,2,2.
Bounds are inclusive/clipped; opposite numeric sizes within a bound category may
produce empty frames/count0. Offset typing and demand follow GROUPS bigint
assignment rules, including numeric rounding and start-before-end validation,
but ROWS never needs peer boundaries or arithmetic support for order-key types.
Prepared arguments, named volatile identity, result metadata, authority and
native Dump/Restore use the existing window pipeline.
Feature smoke: `make check RUN='^TestReferenceQueryWindowRowsFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryWindowRowsFeatureSmoke$'`.

Only SELECT and outer ORDER allow calls. Nested windows, windows inside ordinary
aggregates, illegal clauses and undefined/duplicate names retain PostgreSQL
SQLSTATE/English/character positions. Active windows reject FOR UPDATE; unused
WINDOW syntax alone does not. Referenced key columns require SELECT authority,
including hidden keys, unused definitions and empty inputs.

Frame exclusions, other providers, count(expr) window execution and
window aggregate FILTER remain native0A000 capability boundaries. Ordinary aggregate
modifiers are unchanged. See [query semantics](QUERIES.md#selected-windows--change-note-changelog).

