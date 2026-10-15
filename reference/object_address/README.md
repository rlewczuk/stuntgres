# reference/object_address

## Testing

Follow the [shared testing guide](../../docs/TESTING.md). The package uses only
public driver/error/native-control APIs and the shared pinned fixture lifecycle.

### Object identity and address selected proof

`TestReferenceObjectAddress...` covers the four PostgreSQL 18 object-address
functions through public direct/prepared SQL, typed records/arrays and metadata.
Independent released fixtures exercise all 621 builtin type identities, 3397
routine identities, 799 operator identities, 70 supported cast relationships,
58 operator classes and every referenced operator family. Dynamic object OIDs
are checked against their existing catalog/alias owner, not cross-server
allocator equality. Owned relation kinds, columns/system attributes, constraints,
defaults/generation recipes, rules/policies, roles, namespaces, current database,
collations/conversions and installed extension members have separate witnesses.
This does not add any adjacent object provider or SQL catalog projection.

Partitioned-index creation remains a separate native DDL limitation: its exact
unsupported diagnostic and nonpublication are asserted natively, while the
PostgreSQL branch retains the positive address witness. Native TEMP coverage
uses supported temporary sequences. Transaction-local identity uses supported
`ALTER TABLE ... DROP COLUMN`: the private catalog exposes the dropped-column
tombstone, and rollback restores the same live numeric identity.

TEXT metadata reports the existing unbounded `MaxInt64` length with known=true
in both modes; OID/raw-scan and other accessor distinctions remain explicit.
Native Reset/Restore retains already returned cursor values and metadata, but
pre-replacement prepared provenance remains fenced with the ordinary
catalog-replaced error and no rows. A fresh prepare resolves restored authority.

Pinned PostgreSQL distinguishes SQL type spelling from catalog-name quoting:
JSON's identify name is `"json"`, but its machine identity/arguments are `json`
and `json[]`. All unary operator numeric identities remain covered; their
argument arrays omit the absent operand. An explicit two-argument `NONE` inverse
input retains PostgreSQL's 42601 syntax diagnostic and invalid-type Context,
rather than pretending every symbolic form roundtrips.
The stale numeric routine fallback is output-only: inverse `routine` is
unrecognized, while `role membership` is recognized but unsupported. Both
produce their distinct PostgreSQL 22023 diagnostics, not fabricated addresses.

Keep expected results independent of native output. New or changed scenarios
must establish uncertain outcomes on pinned PostgreSQL first, then exercise the
same completed definitions in native mode. Moving scenarios to another package
also requires fresh selected proof in both modes.

Review regressions separately exercise persisted DEFAULT and CHECK demand for
all four operations through standalone direct/prepared INSERT, false-CHECK
UPDATE diagnostics and atomicity, followed by
native Reset/Restore and independent-driver replay consumption. Strict-NULL
planning distinguishes scalar describe with a literal NULL (no volatile sibling
effect) from RECORD calls and nonconstant NULL (reached sibling effects).
The FROM regression covers every NULL slot, aliases, one-row typed NULL
expansion, ROWS FROM and ordinality through direct/fresh/held execution.
Type-argument arrays preserve sequential syntax-versus-NULL error precedence,
including reversed order and the cast name/argument boundary. The parameter
matrix covers every slot, numeric range/import and malformed TEXT[] errors,
NULL plus invalid import siblings, individual bound NULLs, imported NULL
elements, per-kind list lengths, missing names/types and type-argument syntax.
Whole-NULL inputs suppress body errors but not import errors; complete body
diagnostics retain only their own type-parser Context, not portal import Context.
Every failure is followed by valid recovery through direct, freshly prepared
and held execution.

Run these bounded groups in both modes with an external 900-second deadline
(add `REFERENCE=1` for the pinned PostgreSQL 18.0 UTF8/C oracle):

```sh
make check RUN='^TestReferenceObjectAddress(Signatures|RoundTrips|Nulls|Metadata|StrictEvaluation|StoredBoundaries)$'
make check RUN='^TestReferenceObjectAddress(AuthorityRelations|AuthorityTypes|AuthorityDefinitions|AuthorityBuiltins)$'
make check RUN='^TestReferenceObjectAddressAuthority(Routines|Operators)$'
make check RUN='^TestReferenceObjectAddressAuthority(Casts|IndexClasses)$'
make check RUN='^TestReferenceObjectAddress(Diagnostics|DiagnosticParameters|Arrays|MissingNames|QuotingVisibility|Stale)$'
make check RUN='^TestReferenceObjectAddress(HeldExecution|Transactions|Lifecycle|Cancellation)$'
make check RUN='^TestReferenceObjectAddress(StoredDemand|StrictConstantNull|StrictConstantNullFrom|TypeArgumentOrder|DiagnosticParameterMatrix)$'
make check RACE=1 RUN='^TestReferenceObjectAddress(HeldExecution|Transactions|Lifecycle|Cancellation)$'
make check RUN='^TestObjectAddress(Authority|Identity|Lookup|StoredRecipes|Snapshot|Metadata)$'
make check RUN='^Test(IndexFamilyMetadataAuthority|ObjectAddressGrammar)$'
```

Actual public feature smoke:

```sh
make check REFERENCE=1 RUN='^TestReferenceObjectAddressFeatureSmoke$'
make check RUN='^TestReferenceObjectAddressFeatureSmoke$'
```

Expected smoke observations are a quoted column identity and raw name array,
same address after inverse roundtrip and rename, changed display, symbolic-view
native Dump/Reset/Restore, and standalone direct/prepared INSERT consumption of
persisted DEFAULT/CHECK inquiries before and after native replay. Then assert
exact stale numeric results and missing symbolic diagnostics after drop.
Native controls are explicit native-only phases; their
SQL outcomes share the same body with PostgreSQL. Whole-project quickcheck and
finalcheck do not replace selected dual-mode proof.

