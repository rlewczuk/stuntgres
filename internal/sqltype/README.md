# internal/sqltype

SQL type descriptors and immutable values, text/binary codecs, coercion,
comparison, operator resolution and type-owned evaluation plans. Catalog
namespaces, permissions and atomic publication belong to the engine; transport
framing belongs to `pgserver`, and FTS algebra to `internal/engine/fts`.

## Layering

Production dependencies are the standard library, `sqlstate`, `util`,
`engine/fts`, `internal/xml`, `internal/workadmission` and the local Unicode and
encoding providers. Generated Unicode tables are rebuilt by `make generate`.
Encoding and embedded time-zone provenance live in the
[pgencoding18](pgencoding18/README.md) and [tzdata](tzdata/README.md) READMEs.
Runtime behavior must not depend on host Unicode, codecs or time-zone data.

## Descriptors, values and ownership

- A `Descriptor` owns text input/output, SQL literal emission and binary
  receive/send. Driver, wire and COPY consumers reuse these codecs rather than
  maintaining adapter-local conversion registries.
- A `Usage` is descriptor, typmod and collation. A `Value` is an immutable datum;
  arrays, composites and other shared payloads never mutate after construction.
  Constructors either copy borrowed input or explicitly take exclusive ownership.
  NULL is framing, not empty text, empty bytes or the zero `Value`.
- `SameDatum` and `SamePayload` describe representation identity independently of
  SQL equality. Sharing a representation does not grant operators or casts.
- Capabilities come from captured descriptor providers, not an OID, spelling or
  scalar operator alone. Comparison, grouping, hashing, indexing, storage and
  binary I/O are separate admissions. Missing providers never fall back to text
  while claiming binary format; pseudo-type metadata grants no storage authority.
- Domains preserve logical identity, delegate physical codecs to the base and
  check fresh input through the supplied environment. Composite and array
  descriptors retain coherent member/element generations and complete usages.
- Builtins are cloned into independent descriptor generations. New descriptor
  pointer fields must participate in the template's pointer remapping. Equal OIDs
  do not authorize values or plans from a foreign generation.
- `Retag` preserves datum representation under compatible descriptor ownership;
  catalog-authorized import/remapping owns generation replacement. Identity ALTER
  does not re-input stored text. Retained results keep their original immutable
  descriptors while fresh metadata uses the current graph.

## Evaluation and borrowed authority

Family plans bind formal/result usages and exact provider generations before
execution. They retain immutable typed recipes, never contexts, sessions,
permission results, mutable scratch or catalog lookup callbacks. Per-call state
belongs to the invocation; source payloads remain immutable on every exit.

Input/output environments supply captured temporal, locale, catalog, domain and
transport policy. Installed callbacks borrow the current runtime boundary only
when the actual leaf is reached. Recursive containers carry that environment to
leaves; NULL handling does not fabricate a callback invocation. Provider identity
and generation remain distinct from execution-time authorization.

Client encoding uses a captured directional conversion pair. Native Go values
remain Unicode; byte-transport owners enable conversion explicitly. Binary
container framing and opaque payloads are not character data. Conversion and
append failure publish no partial output.

Long scans, copies and kernels honor cancellation before publication. Selected
kernels use [workadmission](../workadmission/README.md) and
`util.ExecutionCheckpoint` only in explicitly activated, unlocked runtime
contexts. Binding and metadata contexts stay inert. These policies are borrowed
request authority, not descriptor, value or plan state; no kernel acquires an
engine/session mutex or invokes application work under one.

## Coercion and resolution

- `PlanCoercion` selects direct providers, same-context array element conversion
  and eligible string-category I/O without a transitive cast search. Plans retain
  sizing, domain checks and typed NULL ownership and rebind coherently.
- `SelectCommonUsage` owns the common type/domain/typmod fold.
- `ResolveOperator` and `ResolveOperatorWithCoercion` share ranking. Exact
  signatures win; neither literal spelling nor caller identity special-cases a
  provider. Captured catalog feasibility is used during resolution, not retained
  as an evaluation callback.
- Binary coercion uses a checked representation mapping, not text/wire I/O as a
  memory layout. Catalog cast permission and publication remain engine-owned.
- Input producers mark only their own recoverable failures. Destination consumers
  decide recovery through `InputSoftError` or their explicitly separate error
  policy; cancellation and unrelated callback failures retain identity.

## Testing

Units call actual codec, value and plan functions directly. Public SQL/wire
behavior belongs in `reference/`; composed-engine tests use the existing
`enginetests/` owners. Follow [docs/TESTING.md](../../docs/TESTING.md).

Select existing family prefixes, such as `RUN='^TestComposite'` for descriptor
and payload ownership or `RUN='^TestPGRegexMatcher'` for matcher ownership. Add
`RACE=1` for concurrent/cancellation cases. Build inputs and plans outside
allocation measurements using `util.AllocationsPerCall`.

Some cancellation units inspect runtime frames. Before renaming a kernel, inspect
its tests for frame-name dependencies rather than weakening the cancellation
witness. Oracle expectations remain independent of candidate codec output.
