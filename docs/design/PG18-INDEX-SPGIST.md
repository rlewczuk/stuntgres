# Logical SP-GiST indexes

## Identity and execution authority

SP-GiST has its own finite native access-method capability and the released
method4000/handler334 identity. SQL access-method aliases retain their own method,
family and class identities. The catalog remains the only class/default/member
selector. Released support declarations describe metadata, not callable physical
PostgreSQL callbacks or B-tree comparison grants.

The seven built-in classes are inet_ops, range_ops, box_ops, quad_point_ops,
kd_point_ops, text_ops and poly_ops. The quad point class is the point default;
kd is explicit. Range classes bind through existing anyrange support, including
custom ranges and domains. Directional binary compatibility admits CIDR for INET
and VARCHAR for TEXT; stored values retain their actual descriptor and payload.
Unsupported inputs, unrelated classes and options are rejected by existing DDL
admission and diagnostic owners.

## Exact immutable membership

GiST and SP-GiST share a logical key-extraction program and unsorted immutable
membership merge. SP-GiST is not dispatched as GiST. Entries keep original typed
values, NULLs, duplicates and actual row/version/physical positions. Polygon BOX
storage metadata never replaces polygon data with a bounding box. Predicates,
expressions and produced write outputs use the ordinary index program once;
publication shares the rows' Finish/commit frontier. Failed extraction, cancellation
and rollback cannot install a partial root or mutate a predecessor.

This representation needs no disk pages, split algorithm, signatures, hash buckets,
B-tree comparator, optimizer or second persistence format. Scalar predicate
providers and explicit query ordering remain the authority for results; SQL ORDER
BY alone is not evidence that an index was used.

## Capabilities and configuration

One search key and INCLUDE are admitted. UNIQUE, multikey search, ordinary key
ordering and explicit null ordering are not. Only explicit relation fillfactor
is retained, with lexical ordering and absent defaults preserved across metadata
edits, DefinitionSQL, Dump and replay. Built-in classes have no opclass options.

Distance capability requires a live ordering-purpose family member with the class
input type on either operand and a sortable result family. It does not require
GiST's left-input rule or distance-support slot. Point, box and polygon classes
have released distance members; text, network and range classes do not.
Returnability follows actual SP-GiST compression metadata; included columns remain
returnable. Scalar operations outside a selected family remain usable without
advertising that family's index ordering capability.

TEXT pattern comparisons use genuine TEXT-to-BOOL routine/operator associations
and the cancellable bytewise C comparison kernel, independently of input locale.
Prefix length, case and trailing spaces remain significant. BPCHAR providers are
not implied by these declarations.

## Lifecycle and constraints

Existing Journal, private catalog, savepoint, combined DDL/DML publisher,
maintenance, LIKE, partition and native replacement owners govern SP-GiST as an
ordinary index method. Replay resolves destination class/type/provider ownership
and rebuilds destination row memberships, never transplanting source callbacks or
roots. Exclusion uses commutativity and actual family membership admission followed
by the existing immediate/deferred conflict and concurrent-owner wait machinery.
This includes actual SQL replay of user-defined SP-GiST handler aliases, families
and classes: rediscovered destination identities, INCLUDE and explicit fillfactor
remain authoritative through subsequent DML, option changes and REINDEX. Native
replacement around retained TEMP relations remints colliding imported identities
and rebuilds original domain-typed values at destination row/version positions.
Required support1–5 is internally owned by the creating class or ALTER FAMILY
owner; optional support6/7 and operators retain loose dependencies.
