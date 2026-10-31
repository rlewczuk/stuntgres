# Hash raw-page inspection

## Decision and scope

The user authorized **four decoders** under the historical five-name catalog
record: `hash_page_type(bytea)`, `hash_page_stats(bytea)`,
`hash_page_items(bytea)` and `hash_metapage_info(bytea)`. They append four
immutable declaration slots to the existing eleven pageinspect 1.13 members.
Existing slots retain their identities. There are no new operators or aliases.

`hash_bitmap_info(regclass,bigint)` is **physical acquisition, non-engine**.
Its released body opens an actual hash index, obtains the physical block count,
reads its metapage and bitmap buffers, and maps a physical overflow block to a
bitmap bit. Logical embedded rows cannot supply those buffers. This decision
neither registers that function nor introduces a bytea overload, error-only
surrogate, physical attachment API, page synthesis or successful no-op.

## Authority

The [PostgreSQL 18 manual](https://www.postgresql.org/docs/18/pageinspect.html#PAGEINSPECT-HASH-FUNCS)
defines the public purposes and superuser boundary. Installed ABI follows
[REL_18_6 pageinspect--1.5--1.6.sql](https://github.com/postgres/postgres/blob/REL_18_6/contrib/pageinspect/pageinspect--1.5--1.6.sql),
including **scalar** RECORD stats (despite the manual's SETOF prose), SETOF
items, sixteen metadata OUT fields, C/STRICT/VOLATILE/PARALLEL SAFE declarations,
cost 1 and items rows 1000. Runtime validation and diagnostics follow
[REL_18_6 hashfuncs.c](https://github.com/postgres/postgres/blob/REL_18_6/contrib/pageinspect/hashfuncs.c).
The existing pinned 18.6 oracle remains unchanged; development source trees
are research aids only.

## Ownership and execution

The existing installed provider, binder, stored identity codec, current routine
ACL and body-superuser capture own all four routines. Scalars expand through the
shared scalar source; only items is set-returning. STRICT NULL avoids the body,
with one all-null expanded stats/meta record versus zero items. A non-NULL new
page is different: type is unused, while stats/items/meta reject the required
type. Decoder work occurs outside engine locks.
Existing scalar constant-NULL planning remains unchanged: an erased
`hash_page_type(NULL::bytea)` need not reach EXECUTE admission, even though
the declaration is VOLATILE. Runtime stored NULL columns retain the selected
call and still enforce EXECUTE; RECORD and SETOF calls are not that scalar
folding case. Permission fixtures distinguish these paths explicitly.

`sqltype.HashPage` borrows immutable bytea backing without an 8 KiB copy.
Fixed fields use the shared little-endian native readers. Stats counts every
non-DEAD line pointer, including UNUSED/REDIRECT. Items includes DEAD and uses
actual tuple bytes rather than declared tuple length; the NULL mask alone shifts
the hash-key offset. Unsigned opaque/hash fields widen to INT8, procid remains a
genuine unsigned OID, ntuples preserves float bits, and both metadata arrays
retain their full released dimensions (98 and 1024, lower bound 1).

Validation order is length, new-page short circuit, encoded special size,
opaque ID, sane low type, required type, metadata magic, metadata version.
Low header version and bounded unusual offsets are not new corruption gates.
Upstream does not validate arbitrary tuple offsets before dereferencing them;
out-of-allocation reads are undefined there. Native access fences return the
existing ordinary unsafe-native-layout error, never an invented SQLSTATE.
Item loops poll cancellation and discard partially decoded output.

## Proof ownership

Independent public byte builders and literal outcomes live in
`reference/fixture/pageinspect_hash.go`. Dual-mode extension scenarios cover the
four declarations, direct/prepared/held input changes, fields, arrays, diagnostics,
NULL cardinality, current permissions, metadata lifetime and stored identities.
The existing ownership survivor scenario includes detached/foreign hash members
and explicitly distinguishes new-page type success from required-type errors.
Native-only phases exercise Dump/Reset/Restore, fresh-driver symbolic remapping,
retained old results and atomic failed/canceled restore. Native replay uses
Restore's bounded admission for canonical catalog ACL repairs; arbitrary SQL
catalog mutation is not a decoder capability. In reference mode the actual
emitted dump SQL is separately executed unchanged on pinned PostgreSQL.
SQL-independent native units separately prove borrowed backing, float bits,
all array elements, bounds safety and cancellation without sending undefined
C layouts to PostgreSQL. Verification is performed by the integration owner;
this decision document is not a claim that any check has run.
