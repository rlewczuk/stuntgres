# internal/sqltype

Descriptors own text input/output and binary receive/send. Engine consumers
supply the real captured temporal, locale, catalog, role and domain-check
environments. The server's Bind, row output and FunctionCall paths reuse these
methods; they do not add a conversion registry. The embedded `database/sql`
converter retains its existing Go-value contract rather than treating arbitrary
byte slices as wire binary input.

Domains delegate physical codecs while retaining logical identity and checking
fresh input. Admitted arrays and composites recursively retain their member
usages and NULL identities. NULL belongs to the caller's framing: it is not the
empty text/byte value or the zero `Value`.

A catalog OID alone does not grant an executable provider, cast, table/storage
admission or persistence capability. Lookup-only companion arrays remain
unavailable. Raw internal CHAR value codecs do not admit CHAR columns. Native
TID has text but no binary provider; its binary boundary is type-specific
`0A000`. ACLITEM and installed query_int have no binary functions and report
`42883` rather than fabricating bytes. Nonnull pg_node_tree input and anonymous
RECORD text input retain their existing explicit restrictions. See
[`docs/TYPES.md`](../../docs/TYPES.md#binary-stream-providers) for the full type
and storage distinctions.

`Descriptor.CheckBinaryInput` and `CheckBinaryOutput` share the actual
missing-provider boundary with binary receive/send dispatch, without invoking
conversion on a fabricated value. Domains delegate to the base owner; generic
array and composite send providers do not eagerly resolve their members. Existing
canonical nullable catalog carriers retain only their metadata/NULL capability,
not new nonnull value admission. Protocol binary COPY TO initializes these declared
providers after CopyOutResponse and before its signature or row demand, so a
missing send function still fails for an empty or all-NULL result.

`CheckBinaryInputDeclaration` resolves a declared receiver without preparing its
invocation. A domain always has the generic domain receiver; only actual input
invocation (`CheckBinaryInput` for NULL, the real converter otherwise) resolves
the base receiver before domain constraints. Binary COPY FROM uses declaration
setup for all live physical columns before CopyInResponse, even omitted columns.
Its domain NULL invocation remains row-owned after that response. These seams
share one absence authority; no fake datum, second codec inventory or eager
container-member initialization is involved.

Binary NULL arguments initialize receive availability before domain constraints;
nonnull arguments retain their actual converter. Ordinary row output initializes
send availability only on its first delivered tuple, including all-NULL tuples,
not for an empty result. FunctionCall NULL results remain a distinct framing
shortcut rather than ordinary row-destination initialization.

## Testing

Follow the [shared testing guide](../../docs/TESTING.md). These tests call codec
and value functions directly; public SQL and socket answers belong in
`reference/`.

`TestProtocolValueCodecs` exercises real unavailable receive/send diagnostics,
domain receiver declaration versus invocation/base-error precedence, the builtin
time_stamp domain's epoch representation and recursive domain-array
receive/send with independently specified network bytes and separate NULL
members. It does not substitute an OID whitelist or inventory-membership test
for consumer behavior. The engine's same-named mechanism test separately checks
borrowed output scratch, captured role formatting and retained anonymous RECORD
occurrence authority.

```sh
make check RUN='^TestProtocolValueCodecs$'
```

The public provider matrix, text/binary Bind and result metadata use the actual
server subprocess and the pinned PostgreSQL fixture:

```sh
make check RUN='^TestReferenceServerExtended(Codecs|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceServerExtended(Codecs|Metadata)$'
```

Those shared definitions assert independently expected values, bytes, type
metadata and explicit unavailable-provider boundaries. Their owning
[`reference/sessions` README](../../reference/sessions/README.md) contains the
complete protocol proof and actual pq/psql smoke instructions. Selected checks
do not replace the root's full-project verification.

`make check RUN='^TestSearchOccurrenceOrdering$'` checks occurrence RECORD and
record[] sorting through existing typed providers: numeric ordering, inner NULL
high, ancestry prefix, reached JSON demand before NULL, context cancellation and
OrderingBound versus authentic physical scalar opclass identity. General arrays,
named composites and occurrence-record physical indexes remain unadmitted.
`make check RUN='^TestOrderingBtreeIdentity$'` retains scalar identity/allocation
coverage. Public SEARCH witnesses remain in `reference/query_core`.
