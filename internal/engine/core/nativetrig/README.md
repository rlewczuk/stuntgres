# internal/engine/core/nativetrig

The native DML trigger bodies: the installed `tcn`
(`triggered_change_notification`), `refint` (`check_primary_key`,
`check_foreign_key`), `autoinc`, `insert_username`, `moddatetime` and `lo`
(`lo_manage`) providers, and the builtin `suppress_redundant_updates_trigger`,
`tsvector_update_trigger` and `tsvector_update_trigger_column`. It is a leaf
below core (ENGINE-SPLIT §3, L14.5); provider identity is
[`engine/extension`](../../extension/README.md) and the text-search capture
is [`core/tsearch`](../tsearch/README.md).

## Layering

- Imports only packages below core and `core/tsearch` (the rule-6 arrow
  `tsearch` → `nativetrig`): `catalog`, `dml`, `exec`, `extension`, `fts`,
  `functions`, `ids`, `parser`, `regobj`, `session`, `stored`, `sqlstate`,
  `sqltype`. It never imports core, a family or the root.
- Core reaches it only through `Invoke`, from `invokeNativeTrigger`
  (`core/native_trigger.go`), which immediate BEFORE/AFTER row firing and the
  queued AFTER dispatch call after admitting the installed function.
  `nativetrig` keeps no state.

## Contract

- `Invoke` runs one admitted native trigger body inside the body boundary
  core entered for the invocation: routine and trigger depth, the routine
  environment (SECURITY DEFINER and SET), the command policy and function
  statistics stay core's. It dispatches by the procedure's provider identity,
  checks the provider's firing protocol, and returns the replacement row and
  whether the row proceeds. NEW and OLD are borrowed for the call only.

## Host and authority

- `Host[A]` is the statement a body runs in; core implements it on
  `statementHost` (the shared leaf method set). It embeds `tsearch.Host[A]`,
  over which the tsvector trigger walks and accepts its `tsearch.Capture`,
  and which gives the operation, the I/O environments, work admission, the
  Engine.Mu pair and the private catalog over the pending journal. It adds
  the execution context, the namespace authorization, the regconfig input
  over it, autoinc's legacy `nextval(text)`, lo_manage's direct large-object
  unlink, the current user's identity value, tcn's notification queue entry,
  the atomic invocation barrier, and refint's parameterized nested SQL
  (`PrepareTriggerSQL`, `ExecuteTriggerSQL`).
- The authority is `tsearch.Authority[A]`, held by value; the package is
  generic over it, as `core/tsearch` is, so nothing is boxed per row. Core's
  `AuthorizationView` implements it.

## Locks

- Bodies are entered with no lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it around the tcn
  primary-key read, the tsvector authorization and configuration captures
  and refint's private catalog read; the host methods named …Locked run
  inside those windows.
- Parsing, lexization, sequence and large-object calls, identity capture,
  notices and the generated refint SQL run with nothing held.

## Behaviour

Suppression uses cancellable
typed payload comparison, not SQL equality. Vector bodies capture
one immutable current FTS generation per invocation, resolving explicit names
eagerly and row OIDs at the first non-NULL source. Parser, dictionary and notice
callbacks run unlocked. Separate sources share a caller-owned position counter;
non-rebuilding UPDATEs still validate and parse but retain no output vector.
Argument lookup retains catalog system-attribute existence and type authority
without treating negative attributes as dense slots. BEFORE NEW is a transient
tuple: its tableoid is InvalidOid, not the producer relation's identity. Vector
replacement preserves the destination's owned logical descriptor using the
binary-compatible carrier, without introducing SQL assignment or fresh domain
input checks. Ordinary input and subsequent table constraints retain their owners.
The tcn body reads valid primary-index keys by stable attribute in key order,
uses ordinary type output with exact payload quoting, and passes its channel and
composed payload to the statement's single validating notification queue, which
owns the 22023 empty/length checks. UPDATE and DELETE
borrow OLD; INSERT borrows NEW. Commit, rollback, deduplication and savepoints
remain notification-owner responsibilities. Ordinary SQL helper calls retain
installed routine authority but never borrow an enclosing trigger activation.
Routine environments, trigger depth and statement rollback retain their existing
owners. C TriggerData/native ABI, heap/TOAST layout, WAL and process loading are
outside this process-local logical execution and control boundary.
Autoinc borrows the incoming tuple for every literal column/sequence pair and
clones output only at its first replacement. Only actual INT4 columns qualify;
NULL/zero demands late textual relation lookup and the ordinary sequence host.
Each allocated int64 narrows to int32; narrowed zero retries exactly once.
Sequence identity is captured before its allocation lock wait, and allocation,
ACL/read-only checks, session memory and rollback behavior remain sequence-owned.
Logical replay stores current helper identity and unchanged argument text,
never early-bound sequence/column identities or activation state.

Installed refint procedures join this checked native trigger boundary using
their immutable provider symbols. `check_primary_key` probes ordinary SQL for
the current referencing values; `check_foreign_key` executes restrict, cascade
or setnull against OLD keys, preparing every listed reference before unchanged
UPDATE detection. Any NULL local key short-circuits according to the stock
protocol. Arguments naming local attributes use exact catalog spelling; remote
relation/column arguments remain SQL text, including required identifier quotes.
These are not dependency-bound relation names and renaming a table does not
rewrite them. The generated SQL parser and typed binder share the same closed
positional parameter count and local key type declarations; outer statement
parameters never supply or bound a nested helper's frame.
Generated SQL uses the ordinary nested command owner, invoker
rights, RLS, inheritance and genuine child triggers. Effects share the outer
statement journal and roll back atomically, while already delivered action
notices remain observable. Per-invocation cascade UPDATE literals use current
NEW output values with SQL quoting and explicit NULL, without a plan cache.
Generated-SQL failures retain SPI-style internal positions/query or nested SQL
contexts; helper-owned violations use 09000 rather than declarative-FK 23503.
Native controls replay installed identities and postdata triggers, never frames
or cached invocation values. PostgreSQL's SPI/fmgr C ABI and shared-library
loading are external mechanisms, not exemptions from these logical SQL effects.

The stock moddatetime provider replaces one exact builtin timestamp/timestamptz
NEW field from the transaction start and current session zone, using unsized
precision rather than reapplying the column typmod. Literal arguments name
case-sensitive user columns; domains are not temporal helper destinations.
Ordinary SQL invocation is an unfolded evaluation-time protocol error.
BEFORE/AFTER STATEMENT, including TRUNCATE, and AFTER ROW enter the shared
native boundary and receive the helper's row-before-timing protocol checks.
Arbitrary shared-library loading and general PostgreSQL SPI are not engine paths.

`trigger_lo.go` consumes authentic lo_manage dispatch through that same boundary.
It borrows OLD/current NEW and stable target attribute identities, using inline
unsigned OID/domain comparison on the common path and shared scalar output for
other representations. The direct unlink host call enforces existence/ownership
in the existing statement catalog journal, without another function EXECUTE
check or undo store. Trigger lookup errors occur only when fired; extra literal
arguments are ignored. No timing restriction, automatic attachment, reference
counting or TRUNCATE/DROP cleanup is added.

The insert_username body borrows producer NEW, resolves its literal target
against the admitted relation on each firing, requires exact declared TEXT and
changes only that cell. Current-user capture occurs inside the routine
environment; no PL frame, target-slot cache or retained borrowed row is created.

## Testing

- The tcn payload quoting and lo_manage decision units are internal tests
  here (`RUN='^Test(TcnPayload|LoManage)'`); the lo_manage units instantiate
  the bodies with a stub authority and a nil host, so they cover only the
  decisions that return before reaching the host.
- The bodies' engine tests stay in core until their census owners move them
  (`RUN='^Test(TriggerBuiltins|TcnTrigger|AutoincTriggerFrames|InsertUsername|ModdatetimeTrigger|RefintInvocation)'`
  over `./internal/engine/core`; core's README lists the per-family
  selectors).
- Public scenarios are
  `RUN='^TestReference(Autoinc|InsertUsername|Moddatetime|Refint|Tcn|LoManage)'`;
  shared lo_manage SQL, diagnostics and native-control witnesses live in
  `reference/extension_types`.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/nativetrig/enginetests`.
