# PostgreSQL 18 index capability inquiries

## Public contract

The selected functions are STABLE and strict, returning nullable BOOL:

- `pg_indexam_has_property(oid, text)`
- `pg_index_has_property(regclass, text)`
- `pg_index_column_has_property(regclass, integer, text)`

Property names are ASCII case-insensitive, without whitespace trimming. Unknown
names, wrong property levels, invalid numeric identities, non-index relations and
invalid column ordinals return SQL NULL, not false. Missing named REGCLASS inputs
instead fail during normal input resolution. Every argument still undergoes normal
signature and literal analysis when another argument is NULL. A constant strict
NULL can eliminate runtime evaluation, including volatile siblings.

All results use the existing BOOL codec and metadata. Calls compose in existing
query, predicate, aggregate, conditional, view and CHECK expression consumers.
Native DEFAULT function admission remains unchanged: these calls are unsupported
there. STABLE inquiries cannot be used as index or generated expressions.
Only the three exact names join the existing selected-call singleton repertoire;
this does not admit arbitrary no-FROM expressions.
The lexer promotes inquiry calls through the existing selected-function token
and scalar operand/CHECK/index/generated routing. Case-insensitive lexical admission preserves
quoted AST spelling for normal signature errors. A bounded `pg_index_` prefix
also admits unresolved family names for diagnostics, like the existing `pg_get_`
rule; this grants no builtin implementation or additional singleton support.
Both column DEFAULT constraint actions reject selected inquiry occurrences
structurally before publishing the utility AST, including wrapped/named defaults.
The existing qualification diagnostic runs first. This preserves native
`0A000`/`functions are unsupported`, rune positions and prepared-Prepare versus
direct-Execute failure stages; binder/renderer guards remain defensive owners.

## Definition capabilities

The AM inquiry recognizes the six fixed PostgreSQL 18 builtin index AM identities.
It does not create an AM catalog or enable non-B-tree index DDL.

| AM / OID | can_order | can_unique | can_multi_col | can_exclude | can_include |
| --- | --- | --- | --- | --- | --- |
| btree / 403 | true | true | true | true | true |
| hash / 405 | false | false | false | true | false |
| gist / 783 | false | false | true | true | true |
| gin / 2742 | false | false | true | false | false |
| spgist / 4000 | false | false | false | true | true |
| brin / 3580 | false | false | true | false | false |

For valid existing B-tree index definitions, `clusterable`, `index_scan`,
`bitmap_scan` and `backward_scan` are true. These describe access-method
capabilities, not a query's chosen plan or native implementation of CLUSTER,
exclusion constraints or physical index-only scans.

Column numbers identify index positions, not table attribute numbers. Ordered
simple, expression and repeated keys precede INCLUDE payloads. UNIQUE backing
indexes follow the same rules.

| Property | Key | INCLUDE |
| --- | --- | --- |
| asc | inverse of DESC | NULL |
| desc | stored DESC | NULL |
| nulls_first | stored NULLS FIRST | NULL |
| nulls_last | inverse of NULLS FIRST | NULL |
| orderable | true | false |
| distance_orderable | false | false |
| returnable | true | true |
| search_array | true | NULL |
| search_nulls | true | NULL |

ASC defaults to NULLS LAST and DESC to NULLS FIRST; explicit clauses override the
default. No capability flags are persisted independently of the index definition.

## Typed binding and ownership

One fixed-size expression node holds kind, at most three children and the bound
REGCLASS-to-OID normalization plan. Binding transforms children before signature
and namespace diagnostics, resolves unknown operands against the documented
signature and merges STABLE with every child's dependence. Evaluation uses inline
values, evaluates each demanded child once and reads the immutable statement
catalog snapshot already shared with comment/deparse consumers. It never searches
physical index entries or locks a fresh live catalog per result row.

Only direct implicit input edges necessary for REGCLASS signatures are added:
OID, INT2, INT4, INT8, TEXT and VARCHAR to REGCLASS. Narrow integers retain unsigned
OID bit semantics; INT8 is checked in 0..4294967295. TEXT/VARCHAR input is STABLE and
uses the captured authorized REGCLASS resolver. Domains flatten through the
existing coercion machinery. Explicit cast admission and storage admission are
not broadened. Grouping, simplification, preflight, rendering, view literal
capture/remapping and destination type reownership carry the same children.
The legacy REGCLASS annotation/sequence provider binds children contextually but
defers the generic assignment step. It then admits only already-REGCLASS base
types (normalizing same-base domains), alongside its existing unknown
literal/NULL/parameter inference. New inquiry input edges cannot leak into that
provider: resolved TEXT columns under `::regclass` or `CAST` retain native
`42804`/`incompatible source and destination types` at Describe/direct Execute.
These pre-existing public DELETE RETURNING boundaries are shared reference
scenarios, including row preservation after every native diagnostic.
Stored-expression validation checks signature, descriptors, dependence and the
retained normalization plan; evaluation verifies normalized OID ownership without
replanning the conversion per row.

Existing canonical index, view and CHECK serialization owns Dump/Restore behavior.
Renames render current object definitions; restore resolves destination identities.
Control witnesses use existing parent-table and key-column rename operations;
they retain the index's name, rather than introducing unsupported ALTER INDEX.
Domain flattening witnesses use contextual stored-domain input, not new explicit
domain cast admission. Predicate witnesses use the existing `IS TRUE` grammar.
Ordinary indexes now participate in the canonical definition dependency graph,
with an exact parent predecessor. Dependent view and external CHECK definitions
therefore replay only after the index exists, including remapped destination OIDs.
UNIQUE backing-index dependencies use the reciprocal parent table, which creates
them inline. Actual index dependencies are retained, not ignored. Real cycles,
including a parent CHECK requiring its own ordinary index, still fail Dump.
This supersedes the prerequisite B-tree document's historical post-data index
emission order: parent definitions precede indexes topologically, and the existing
row phase follows definition creation. No new constraint-deferral mechanism is
introduced.
Stateless builtins require no separate serialized state. Unsupported AM DDL,
extension handlers, new catalogs, optimizer behavior and partial/partition indexes
remain outside this change.

## Independent reference coverage and feature smoke

The shared `TestReferenceIndexProperties` family covers all documented property
matrices, definition ordering/INCLUDE, valid input coercions, strict NULLs,
diagnostics and prepare/execute stages, metadata, expression consumers, rename and
native controls. Expected values are independent PostgreSQL contracts, not native
output. The temporary oracle-discovery filter has been removed.

The compact smoke creates an index on `(a DESC NULLS LAST,b ASC NULLS FIRST)` with
`INCLUDE(p)`. It asserts eleven BOOL values:
`[true,true,true,true,true,true,true,false,NULL,NULL,NULL]`, then prepared true,
false and NULL inquiries, native Reset/Restore and independent destination Restore.

Run each selective invocation with a 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexProperties' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexProperties' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(SequenceRegclassInput|DeparseRelations|VisibilityInquiry(FixedAndTyped|PreparedInputsAndCancellation|ChecksAndViews)|ArrayRegclassDomains)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(SequenceRegclassInput|DeparseRelations|VisibilityInquiry(FixedAndTyped|PreparedInputsAndCancellation|ChecksAndViews)|ArrayRegclassDomains)' VERBOSE=0
```

These supplement ordinary quickcheck and the root's post-merge fullcheck. This
implementation document records no test execution result.

## Authority

- [PostgreSQL 18 manual, catalog inquiries, Tables 9.76–9.79](https://www.postgresql.org/docs/18/functions-info.html#FUNCTIONS-INFO-CATALOG-TABLE)
- [PostgreSQL REL_18_0 amutils.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/amutils.c)
- [Independent release regression expectations](https://github.com/postgres/postgres/blob/REL_18_0/src/test/regress/expected/amutils.out)

The runtime oracle is the repository fixture's pinned PostgreSQL 18.0 UTF8/C
image, not local development PostgreSQL sources or a newer server release.
