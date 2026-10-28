# Logical decoding: current-catalog boundary

## Decision

User authorization, verbatim: **`Authorize current-catalog boundary`**.

The four logical-slot get/peek functions expose the released PostgreSQL identities,
formal arguments, defaults and typed results. The native engine has no replication
slots. Consequently every otherwise valid demanded request ends with truthful
`42704`, `replication slot "NAME" does not exist`. No slot is invented to make a
physical-decoding rejection reachable, and no empty successful stream stands in
for decoding.

This is an explicitly authorized interpretation of the physical-boundary
requirement: a **physical-decoding `0A000` witness is publicly unreachable without
real slots**. There is no unused decoder/provider interface or unreachable
unsupported branch. Option-validation `0A000` errors are observable but are **not**
proof of rejection by a physical decoder. Future real-slot support must separately
resolve genuine decoder admission and physical rejection after successful lookup.

## Observable contract and ownership

Each identity accepts `(slot_name name, upto_lsn pg_lsn, upto_nchanges integer,
VARIADIC options text[] DEFAULT '{}')`, returning SETOF record with `lsn pg_lsn`,
`xid xid`, and `data text` or `data bytea`. Positional three-argument calls use the
empty-options default; expanded text arguments and explicit VARIADIC arrays use
the same identity. PostgreSQL 18.6 does not resolve named calls with omitted
variadic options: named success requires an explicit VARIADIC options array.

The non-strict typed source retains immutable operands in written order, their
formal mapping and result descriptor. All supplied operands, including NULL or
finite limits, evaluate once in that order before the live runtime bridge. Ordinary
routine EXECUTE admission remains separate from replication authorization. Metadata
and LIMIT 0 do not demand a source; their absence of rows is not decoding success.

After normal operand evaluation and routine admission, checks are:

1. Executing current role is superuser or has its own REPLICATION attribute;
   inherited membership alone does not suffice (`42501`, including Detail).
2. Slot name is not NULL (`22004`).
3. Options array is not NULL (`22004`).
4. Options rank is at most one (`0A000`).
5. No options element is NULL (`0A000`).
6. Options contain an even number of elements (`0A000`).
7. Slot is absent (`42704`). Decoded NAME coercion/truncation is reused; lookup
   does not impose slot-creation naming rules.

The statement host borrows live role authority under the engine lock; validation
then uses immutable values without external I/O. The bridge is used by every
demanded call, not a hypothetical successful slot provider. Empty/even options,
nonstandard array lower bounds, duplicate/empty/unknown keys and all integer count
values pass pure validation. Plugin-specific key policy is not invented.

There is no mutable feature state, cursor, advancement counter or serializer.
Get and peek failures preserve existing table/role/catalog state; installed builtin
metadata uses the existing declaration/default owners and native replay machinery.

## Stock semantics and unavailable physical work

[PostgreSQL 18 Table 9.101](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-REPLICATION-TABLE)
and [logical decoding examples](https://www.postgresql.org/docs/18/logicaldecoding-example.html)
define actual decoding behavior: get consumes changes; peek does not. NULL limits
are unrestricted. The LSN boundary concerns transactions committed before that
LSN; the count limit is checked after a transaction and can be exceeded by its
changes. Failover slots wait for synchronized physical standbys. These are stock
semantics, not operations performed by the native missing-slot path.

Physical WAL generation/replay, durable slot positions, output-plugin execution
and standby synchronization are unavailable here. No arbitrary plugin executable
is loaded, and no LSN, XID, decoded row, wait or consumption is fabricated.

The oracle needs `wal_level=logical` and positive `max_replication_slots`; stock
configuration errors otherwise precede NULL/options/lookup. The package fixture
sets logical WAL on its unchanged pinned PostgreSQL 18.6 image. Native code does
not invent corresponding physical settings.

Source precedence follows
[logicalfuncs.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/replication/logical/logicalfuncs.c),
[slot.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/replication/slot.c),
and [logical.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/replication/logical/logical.c).
Initialized empty-options defaults are installed by
[system_functions.sql](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/system_functions.sql),
not solely pg_proc.dat.
