# reference/roles

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Session role switching

Session role switching uses the shared direct/prepared scenarios and complete
identity/diagnostic expectations in `reference/roles/role_switch_test.go`.
Capture the completed definitions against pinned PostgreSQL before implementing
a compatibility change, then run the same definitions natively to establish red.
Any temporary oracle-discovery backend filter must be removed before native red,
final dual-mode proof and review. The configured-reset supplement uses a genuine
PostgreSQL startup options connection and its own middleware recorder; native
configured defaults are internal-unit evidence, not a public connector capability.
Pre-canceled owner preservation is a native adapter guarantee, not a portable pq
guarantee: fast pq utilities may finish and retire the owner asynchronously.

### Ownership proofs

Ownership proofs use complete direct/prepared scenarios in `reference/roles/`
and native-only canonical descriptor/publication units. The new supported
families and six migrated table-owner callers require selected checks in both
modes; selected race proof supplements, never replaces, real PostgreSQL:

```sh
make check RUN='^TestReferenceRoleOwnership(Families|Authority|Associations|SameOwner|DiagnosticsAndFreshness)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(Families|Authority|Associations|SameOwner|DiagnosticsAndFreshness)$'
make check RUN='^TestReferenceRoleOwnership(ReassignAuthority|ReassignFamilies|ReassignPreservation|ReassignClassEligibility|DefaultsBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(ReassignAuthority|ReassignFamilies|ReassignPreservation|ReassignClassEligibility|DefaultsBoundary)$'
make check RUN='^TestReferenceRoleOwnership(RestrictAtomicity|RestrictSchemaUnion|Cascade|Cleanup|DropRole)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(RestrictAtomicity|RestrictSchemaUnion|Cascade|Cleanup|DropRole)$'
make check RUN='^TestReferenceRoleOwnership(Transactions|Lifecycle|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(Transactions|Lifecycle|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceRoleOwnership(Database|SharedDatabase|DatabaseCleanup)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(Database|SharedDatabase|DatabaseCleanup)$'
make check RUN='^TestReferenceRoleOwnership(ExtensionReassign|ExtensionDrop|ProviderControls)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(ExtensionReassign|ExtensionDrop|ProviderControls)$'
make check RUN='^TestReferenceRoleOwnership(TypedTables|TypedDependencyCleanup)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(TypedTables|TypedDependencyCleanup)$'
make check RUN='^TestReferenceRoleOwnership(InheritedGrantSupport|DeleteGroupCounts)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(InheritedGrantSupport|DeleteGroupCounts)$'
make check RUN='^TestReferenceRoleOwnership(DefaultCleanup|DefaultRestrictAtomicity)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(DefaultCleanup|DefaultRestrictAtomicity)$'
make check RUN='^TestReferenceRoleOwnership(DefaultSchemaCleanup|DefaultGrantSupport|DefaultDatabaseAuthority)$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnership(DefaultSchemaCleanup|DefaultGrantSupport|DefaultDatabaseAuthority)$'
make check RUN='^TestReferenceRoleOwnershipDefaultCreatorRecipientUnion$'
make check REFERENCE=1 RUN='^TestReferenceRoleOwnershipDefaultCreatorRecipientUnion$'
make check RUN='^TestReferenceAlterAddConstraint(ExistingData|Provenance|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(ExistingData|Provenance|FeatureSmoke)$'
make check RUN='^(TestDropOwnedDefaultCanonicalCleanup|TestRoleOwnershipDefaultMessageDatabaseFence)$'
make check RACE=1 RUN='^(TestDropOwnedDefaultCanonicalCleanup|TestRoleOwnershipDefaultMessageDatabaseFence)$'
make check RUN='^TestRoleOwnershipObjectGrantMessageDatabaseFence$'
make check RACE=1 RUN='^TestRoleOwnershipObjectGrantMessageDatabaseFence$'
make check RUN='^TestReferenceRoleGrant(InheritedOwner|SourceProvenance|Transactions|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRoleGrant(InheritedOwner|SourceProvenance|Transactions|FeatureSmoke)$'
make check RUN='^TestRoleOwnershipPartitionConstraintDatabaseFence$'
make check RACE=1 RUN='^TestRoleOwnershipPartitionConstraintDatabaseFence$'
make check RUN='^(TestRoleOwnershipSequenceDatabaseFence|TestRoleOwnershipManualUtilityDatabaseFence|TestRoleOwnershipSequenceOwnedCreationDatabaseFence)$'
make check RACE=1 RUN='^(TestRoleOwnershipSequenceDatabaseFence|TestRoleOwnershipManualUtilityDatabaseFence|TestRoleOwnershipSequenceOwnedCreationDatabaseFence)$'
make check RUN='^TestReferenceSequence(CreateRestart|TransactionalRestartDDL|PreparedDropRecreate|OwnedTableLock)$'
make check REFERENCE=1 RUN='^TestReferenceSequence(CreateRestart|TransactionalRestartDDL|PreparedDropRecreate|OwnedTableLock)$'
make check RUN='^TestReferenceEnum(NamespaceOwnership|PreparedLifecycle|AlterSavepointProvenance)$'
make check REFERENCE=1 RUN='^TestReferenceEnum(NamespaceOwnership|PreparedLifecycle|AlterSavepointProvenance)$'
make check RUN='^TestReferenceSequenceNativeDumpPostgresRestore$'
make check REFERENCE=1 RUN='^TestReferenceSequenceNativeDumpPostgresRestore$'
make check RUN='^TestReferencePartitionConstraints(Drops|Transactions|Cancellation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePartitionConstraints(Drops|Transactions|Cancellation|FeatureSmoke)$'
make check RUN='^(TestRoleOwnership|TestReassignOwned|TestDropOwned|TestDropRole)'
make check RUN='^(TestRoleOwnershipMaterializedGrammar|TestDropOwnedCollectedCleanupPrimary|TestDropOwnedCleanupPolicyDeletionUnion)$'
make check RUN='^(TestDropOwnedACLGrantorSupport|TestRoleOwnershipDatabaseDiagnosticNouns)$'
make check RUN='^(TestObjectGrantSourceNearestFullMatch|TestDropOwnedSQLGrantorProvenance)$'
make check RACE=1 RUN='^(TestRoleOwnershipPublication|TestRoleOwnershipDatabase|TestDropOwnedUnion|TestReassignOwnedPublication)'
```

Use external900-second deadlines. Shared schema/type assertions use actual
successor authority and data/control outcomes; absent native pg_namespace/pg_type
SQL providers are not invented. PG supplements and native descriptor units
prove reciprocal associations independently. Schema ACL Dump guard coverage is
an ordinary Go error with zero writer calls. Preserve sequence OWNER transaction
coverage and explicit native utility boundaries. Remove discovery filters before
native red, final proof and review; whole-project Docker-free proof is quickcheck.
Attached-database and registered extension proofs use actual canonical descriptors
and independently owned members. Foreign-extension member refusal is isolated
per member, not pinned to incidental catalog-index tie order. Dependency Detail
assertions retain complete independently specified line multiplicities where
PostgreSQL supplies no independent-dependent tie ordering contract; every other
diagnostic field and notice list remains exact. Internal database tests cover
immutable role/catalog pairing and committed/private predecessor fences.
The materialized-owner parser unit covers the integrated ALTER VIEW AST,
conditional heads, keyword/literal role identity and operand positions without
a competing ownership grammar. Collected-cleanup units distinguish genuine
membership/whole-policy deletions from partial policy and ACL edits, including
policies on already doomed tables, and retain every catalog/role/storage
predecessor after RESTRICT refusal. Standalone smoke uses supported owner,
SET and denied membership-GRANT operations rather than absent native catalog
inquiries; its expected outcome is quiet exit zero, not verification evidence.
The additional grant-support unit independently covers inherited owner versus
SET-only authority, partial independent grant options, received-first cleanup
with outgoing-first ACL order, and unsupported provenance after membership loss.
Database diagnostics independently distinguish raw owner nouns from quoted
privilege nouns, including an opaque name containing literal quotes. Sequence
collection uses actual source ownership, without changing mutation roots.
Grantor selection units cover current/nearer full options versus inherited owner,
same-source table/column option merging, partial fallback and owner/superuser fast
paths. The engine-private SQL seam inspects actual GRANT-origin ACL entries,
their immutable cleanup replacement and the complete retained-grantor role-drop
refusal. It complements the independent public PostgreSQL witness rather than
substituting a constructed ACL fixture for SQL-created provenance.

The retained-default message regression exercises the actual utility path with
paired private policy/role/catalog roots. An owner-only committed DB replacement
must veto final publication without a policy, role, catalog or storage prefix;
cancellation is independent and retained authority roots remain immutable.
The ObjectGrant message regression executes a real SELECT WITH GRANT OPTION
candidate under ordinary pg_database_owner-derived table authority. DB-only
replacement after utility statement completion must veto outer ACL/role
publication, leaving committed reader access denied and retained private access
valid. Distinct private DB authority, unchanged-predecessor success and independent
cancellation retain data, ACLs, constraints and index/storage roots.
The partition-constraint predecessor regression drives the actual early-return
handler using canonical pg_database_owner-derived ownership. It replaces only
the committed DB descriptor at post-staging validation and after the retained
statement boundary, preserving role/session revisions and target identities.
Distinct private DB authority and healthy unchanged predecessors are separate
cases; vetoes retain recursive constraints, backing indexes, independent local
indexes, rows and all catalog/index/storage roots without a publication prefix.
The actual sequence OWNER and manual-utility regressions exercise sequence
CREATE/DROP/COMMENT and enum CREATE through their real AST handlers, alongside
the retained ordinary GRANT/REVOKE witness above. Each distinguishes committed
from honest distinct-private DB authority, tests replacement before Statement
Finish and after retained Finish, and includes healthy actual-effect controls.
Creation uses a pg_database_owner-owned namespace with no PUBLIC CREATE.
Vetoes retain old descriptors/ACLs, enum/array and name roots, rows/index/storage
roots and genuine nontransactional generator/cache/currval state, without the
retained INSERT prefix or a new descriptor/generator prefix.
The OWNED BY creation regression admits a real table reader before upgrading its
ticket to a blocker AX, observes the CREATE worker's pending request on that
exact table, replaces only the DB descriptor and then releases/joins the worker.
The actual helper's fresh table-owner checks still allow its valid candidate,
while fresh namespace CREATE is denied after committed-owner loss. Publication
must therefore fence the original pre-wait CREATE authority, not recapture the
refreshed role/catalog view. Healthy and private wait cases remain independent.
Selected shared sequence/enum definitions above preserve existing direct/prepared
DDL, transaction, dependency, enum safety and native-control/replay expectations;
both modes supplement, rather than replace, the deterministic private race proof.
The existing ADD CONSTRAINT definitions retain their full23503 expectations:
candidate catalog substitution must also pair its captured RoleRead, including
pre-wait admission, post-wait name revalidation and historical FK disclosure.

`DefaultCreatorRecipientUnion` independently checks complete source unions:
recipient-first baseline removal and owner-first residual-policy cleanup succeed
in both modes. The owner-first baseline-only case records pinned18.0's actual
XX000 with the queried real creator-record OID and full diagnostics, then proves
statement rollback of IDs, owners, rows, policies, memberships and generators.
Native instead performs complete atomic cleanup; no fabricated OID/default inquiry
provider, generic unsupported boundary or imitation of stale internal addresses.
Witness-specific cleanup restores DB authority/closes held handles and performs
real creator-only DROP OWNED phases before the unchanged aggregate helper, including
fatal paths. This prevents the release defect from masking assertion failures;
it does not serialize or weaken extension/member or default union acceptance.

### Selected role switching proofs

Selected role proofs (external deadline900s each):

```sh
make check RUN='^TestReferenceRoleSwitch'
make check REFERENCE=1 RUN='^TestReferenceRoleSwitch'
make check RUN='^(TestReferenceRoleExcludedCommands|TestReferenceSessionIdentityFormsAndFreshness|TestReferenceSQLCursorScopePhases)$'
make check REFERENCE=1 RUN='^(TestReferenceRoleExcludedCommands|TestReferenceSessionIdentityFormsAndFreshness|TestReferenceSQLCursorScopePhases)$'
make check RUN='^(TestRoleSwitch|TestRoleSelectionBoundaries|TestRoleGrammar|TestRoleLocalSettingBoundary|TestSessionIdentity|TestSavepoints)'
make check RACE=1 RUN='^(TestRoleSwitch|TestSessionIdentity|TestSavepoints)'
make check RUN='^TestReferenceRoleSwitchFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRoleSwitchFeatureSmoke$'
```

The feature smoke additionally has a standalone public Go program, materialized
outside the checkout from the implementation session's `local://role-switch-smoke.go`.
Run `go run "$SESSION_TMP/role-switch-smoke.go"` from the checkout with an external
900-second deadline. It independently checks utility affected0, NAME metadata,
stable login identity, owner sentinel7, SESSION/LOCAL/savepoint/commit, NONE/reset,
outside-block warning25P01 and denied42501. Parameterless direct multi-command
execution follows [the direct-message contract](../sessions/README.md#direct-message-selected-proof);
prepared/parameterized multiple commands reject with42601, while unsupported
utilities retain their specific capability diagnostics. Malformed scripts still
parse completely before effects.

### Object grants

The `TestReferenceRoleGrant` family covers grants, origin-specific delegation,
table/column revoke independence, warnings, atomicity, utility transaction
boundaries and native control lifetimes. Its independent expectations require
the pinned PostgreSQL oracle before native implementation. Select it with
`make check REFERENCE=1 RUN='^TestReferenceRoleGrant'`
and `make check RUN='^TestReferenceRoleGrant'`
(900 seconds per invocation). Migrated consumer families must also be selected
in both modes; this new family does not replace their complete assertion unions.
`TestReferenceRoleGrantColumnNoticeOrder` covers reversed/duplicate/mixed
requests, dropped attribute gaps and renamed-column diagnostic traversal.
`TestReferenceRoleGrantColumnFullMaskNotices` compares an enumerated merged
four-privilege column mask with ALL under partial and zero grant options.
Both assert complete ordered notices, quiet Prepare and real allowed/denied
consumers through direct and prepared routes. Focused gates (900 seconds each):
`make check RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`
and
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`.
`TestReferenceRoleGrantFeatureSmoke` exercises the complete public grant path.
The additional root-owned standalone public Go source is retained in the OMP
session, temporarily materialized as `./.role-grant-feature-smoke/main.go`, run
with `go run ./.role-grant-feature-smoke`, and removed after recording its result.
No permanent executable or native-only SQL witness belongs in the repository.
Each invocation has a 900-second deadline:
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`
and
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.
Source-provenance units complement, never replace, public delegation/revoke
witnesses. Keep upstream table-option revocation with surviving distinct-origin
column rights separate from a same-column chain's RESTRICT failure. Warning
assertions retain the complete release system/live-attribute traversal, without
claiming native system-column query support.

### Object revocation

`TestReferenceRoleRevoke` adds option-only/PUBLIC/owner boundaries, selective
recursive CASCADE with alternate grant-option support, atomic RESTRICT,
origin-specific table/column selection, complete warnings and held-query/control
lifecycles. Each definition runs direct and prepared public routes. Selected
gates (external 900-second deadline each; run every line in native mode and again
with `REFERENCE=1` for the pinned PostgreSQL oracle):

```sh
make check RUN='^TestReferenceRoleRevoke(OptionOnly|PublicOwner|TableLike|Cascade|AlternateSupport|SelectiveBits|IndependentAccess)$'
make check RUN='^TestReferenceRoleRevoke(Columns|Provenance|Diagnostics|Notices|RestrictAtomicity)$'
make check RUN='^TestReferenceRoleRevoke(Lifecycle|Transactions|FeatureSmoke)$'
```

Oracle-first discovery may select `/(direct|prepared)/postgres` after the
top-level family expression with `REFERENCE=1`. Do not put `$` immediately
before `/` in Make's RUN value: Make consumes that pair and can silently select
no tests. Discovery is not complete dual-mode proof. Native pre-canceled utility
execution is an explicit adapter guarantee; lib/pq can complete a fast utility
before observing cancellation. The shared SQL lifecycle assertions remain the
same in both modes.

The additional root-owned revoke smoke source stays in the OMP session, is
temporarily materialized as `./.role-revoke-feature-smoke/main.go`, run with
`go run ./.role-revoke-feature-smoke` (900 seconds), and removed after recording
the observations. Its shared reference counterpart is selected with
`make check RUN='^TestReferenceRoleRevokeFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceRoleRevokeFeatureSmoke$'`.

### Default privileges

`TestReferenceRoleDefaultPrivileges` has 21 independently specified shared
direct/prepared definitions covering grammar/all stored classes, target authority
and current-role selection, every supported creation seam, additive scoped
precedence, grant options/ordinary owner use, exact ACL-origin exposure/metadata,
atomicity/dependencies/lifecycles and native transaction/control boundaries.
No routine/large-object creation providers, adjacent direct non-table grants or
generic information-schema domains are substituted for the selected witnesses.
Private namespace rights use ownership or SCHEMAS defaults, not native schema
GRANT. All changed definitions require both complete native and pinned real modes.
Projection witnesses use the uncast five NAME and three VARCHAR columns, with
unchanged independently specified ACL values and exact raw transport/metadata.
Controls use uncast current_user/session_user NAME identities. No adjacent cast
provider is required. Enum-array creation preserves PostgreSQL's real success
or element-ACL denial and native's positioned 0A000 storage boundary; private
ArrayAuthority units exercise the actual descriptor-level element redirect.
Creation and Schemas also assert granted TEMP reads after same-connection actor
switches; Creation retains table-ACL revoke denial and a peer's missing relation.
TemporaryNamespace units cover exact physical token ordinary rights, peer/private/
foreign isolation, unchanged ownership/delegation and independent table ACLs.
Creation preserves inherited child ACL use and independently asserts parent
rows7,9 on both backends using the integrated recursive scan provider. A separate
ordinary source explicitly populated7,9 drives CTAS and SELECT INTO, each
affecting2 and exposing ordered7,9 to the default-granted reader. This part adds
no recursive scan provider. Unit table construction owns Engine.mu and
reads its immutable stage-base carrier without reentering CatalogRead.
Diagnostics independently covers both schema-list orders for prohibited classes,
PUBLIC grant options and canonical TEMP/TEMPORARY errors, with complete records
and Execute-stage utility failures. Atomicity checks subsequent denied tables
after late-schema/creator and ordered PUBLIC-option failures. Private Atomicity
retains untouched catalog/role roots on error; Revision checks real multi-schema
updates plus repeated operands publish once, canonical no-ops publish nothing,
and reversal/overflow remain atomic.
`TestReferenceRoleDefaultPrivilegesDiagnostics` also checks quoted lowercase
`"select"` through a future-table reader result7, reversal and retention of the
earlier grant. Quoted `"SELECT"`, `"Select"`, `"Usage"`, `"TEMP"` and `"TEMPORARY"`
each have exact original-spelling `42601` records at Execute on both shared
direct/prepared routes. Future-table reader42501 after each rejection and an
actual future enum dependency CREATE denial after rejected `"Usage"` ON TYPES
prove no policy publication; revoked PUBLIC TYPES defaults prevent baseline
USAGE from masking the latter. The existing unquoted TEMP/TEMPORARY cases keep
their canonical `0LP01` TEMP spelling.
Creation also covers permanent/TEMP CREATE TABLE OF old/new/reversed defaults,
owner INSERT7/9, reader old/next denial and new read9, plus exact SELECT origins.
Types proves source TYPES denial despite the other creator's TABLES policy,
later scoped-source authorization, creator INSERT/read13, recipient read13 and
the table creator's SELECT origin; an earlier denied source stays denied.
Controls asserts rejected native Restore through the SQLSTATE interface and
retained data/default-policy consumers, not its evolving utility whitelist text.
Dump refusal remains an ordinary non-SQL error before any output for stored
defaults and materialized ACLs.
Selected gates have an external 900-second deadline each; run the first five
commands natively and again with `REFERENCE=1`:

```sh
make check RUN='^TestReferenceRoleDefaultPrivileges(Grammar|Diagnostics|StoredClasses|Authority|CurrentRole)$'
make check RUN='^TestReferenceRoleDefaultPrivileges(Tables|Creation|OwnerOptions|Precedence)$'
make check RUN='^TestReferenceRoleDefaultPrivileges(Sequences|Types|Schemas)$'
make check RUN='^TestReferenceRoleDefaultPrivileges(Exposure|ExposureVisibility|Metadata)$'
make check RUN='^TestReferenceRoleDefaultPrivileges(Atomicity|Dependencies|Lifecycle|Transactions|Controls|FeatureSmoke)$'
make check RUN='^TestParseAlterDefaultPrivileges'
make check RUN='^TestDefaultPrivileges(Masks|Baselines|Storage|Merge|CreatorSelection|CloneStage|RetainedMessage|Replacement|Dependencies|SchemaLifecycle|Controls|Projection|ArrayAuthority|TemporaryNamespace|Atomicity|Cancellation|PublicationFence|Revision|CreationFence)'
make check RACE=1 RUN='^TestDefaultPrivileges(Atomicity|Cancellation|PublicationFence|CreationFence|RetainedMessage|Replacement|TemporaryNamespace)'
make check RUN='^TestReference(SerialSequenceAuthority|CompositeAuthority|DomainDependencies|RoleRevokeTransactions|RoleRevokeLifecycle)$'
make check REFERENCE=1 RUN='^TestReference(SerialSequenceAuthority|CompositeAuthority|DomainDependencies|RoleRevokeTransactions|RoleRevokeLifecycle)$'
make check RUN='^TestReference(TemporaryViewUnauthorized|SequenceTemporaryTableCapability|SequenceTempCreationLifetime|TypedTableTemporaryControls|TypedTableTemporaryReaderFence)$'
make check REFERENCE=1 RUN='^TestReference(TemporaryViewUnauthorized|SequenceTemporaryTableCapability|SequenceTempCreationLifetime|TypedTableTemporaryControls|TypedTableTemporaryReaderFence)$'
```

Oracle-first discovery appends `/(direct|prepared)/postgres` to a family
expression without `$` before `/`; it must reach the actual postgres subtests.
There are no permanent discovery-only backend filters. Discovery/native red
evidence does not replace complete dual-mode proof. Exact structured diagnostics
and independent semantic/metadata values precede backend parity. PostgreSQL18.0
global-empty creation normalization is independently distinguished from actual
object REVOKE ALL, and nonempty scoped-only creation proves stored empty matters.
NAME uses existing unknown Length; privilege_type has native MaxInt64/true versus
pq -5/true unsized VARCHAR Length, while is_grantable/with_hierarchy both have
VARCHAR(3) Length3/true. No generic adapter or domain support is inferred.

Private units complement public SQL with immutable map/tombstone/cache/remap
invariants, hardwired dependent rowtype/array baselines, exact consumed-key fences
including absence, deterministic paused creation, terminal cancellation/session
fences, revision/no-op/overflow and final dependency retirement. Whole-project
Docker-free verification remains `make quickcheck`, not a selected-proof alias.
`TestMaterializedAlterOwnerAttachments` preserves coherent ownership and
descriptor/storage/index roots, independently checks new-owner owner/PUBLIC USAGE
origins on both dependent types, excludes former-owner retirement dependencies
and retains immutable old type ACL snapshots. It does not repin an incidental
implicit-versus-materialized ACL flag.
`TestReferenceRoleDefaultPrivilegesLifecycle` creates one constant MV7 under an
otherwise unencumbered ordinary creator's TABLES defaults, reverses that policy,
transfers ownership and restores public-schema ownership before retiring the
former creator while the MV remains live. The distinct default-granted reader
retains7 before and after the new owner's actual REFRESH/read7. Shared
direct/prepared native and pinned modes exercise the complete definition;
cleanup skips retired recipients rather than recreating them. No TYPES-default
reapplication, suppressed dependencies or adjacent schema GRANT is substituted.
The root-owned standalone public Go source stays in the OMP session, is
temporarily materialized as `./.default-privileges-feature-smoke/main.go`, run
with `go run ./.default-privileges-feature-smoke` (external 900 seconds), then
removed. Expected exit0 with no output requires actual old/new/held table values,
scoped addition/reversal, current creator versus inherited defaults, independent
SERIAL sequence authority and exact eight-column origins/carriers.
The executable also uses quoted lowercase `"select"` for the new-table grant
and requires quoted uppercase `"SELECT"` to fail with42601 before a subsequent
table remains unreadable to that recipient.
Its ownership-lifecycle footer grants creator dp_retired TABLES SELECT defaults
to dp_reader, temporarily transfers public-schema ownership for ordinary creation
of the constant MV dp_transferred(x=7), then reverses the directive, transfers the
MV to dp_owner and restores public ownership to postgres. DROP dp_retired must
succeed while the MV remains live; dp_reader reads7, dp_owner performs REFRESH0
and reads7, and dp_reader still reads7 afterward. These are executable assertions,
not an execution claim; the root must record the changed source's actual quiet
exit0 after correction.
Its shared counterpart is `make check RUN='^TestReferenceRoleDefaultPrivilegesFeatureSmoke$'`
and the same command with `REFERENCE=1`; neither substitutes for the executable.

### Role smoke migration and attribute findings

The removed `smoke/TestRoleSmoke` assertion union is preserved by
`TestReferenceRoleCreation` (aliases/login/counts/duplicate/drop),
`TestReferenceRoleAuthorization` (direct/held identities, NOLOGIN switches,
table ACL/no mutation and owner dependencies),
`TestReferenceRoleAuthenticatedSessions` (self/default/reset/peer isolation),
`TestReferenceRoleTrustedConnectorIsolationAndReplacement` and
`TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation`
(authentication, reconnect and Driver/opaque-key isolation), and
`TestReferenceRoleMigratedSmokeControls` (changed-session pool reuse, option
boundary, reader-authorized Dump/Reset/Restore, exact owner directive,
failed Restore/sentinel preservation, legacy stream and role cleanup).
`TestReferenceControlTransactionsHostAuthorization` additionally preserves
reader identity/settings and denied table access across host replacement.
Run the completed remedy/migration definitions in both modes:

```sh
make check RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)'
make check REFERENCE=1 RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)'
```

Role-attribute oracle findings are encoded in the untagged shared
`TestReferenceRoleAttributes...` scenarios; discovery filters/unresolved reports
are removed. Root-observed selected native and pinned gates passed before this
documentation cutover. A separate actual public Go smoke reached creator ADMIN,
ALTER USER omission, expired trusted admission, physical limit release, NOLOGIN
and held authority refresh. Those observations do not replace the remaining
full quickcheck, fresh affected selectors, review or post-merge finalcheck.

Run the exact changed reference definitions in both modes before review, each
with a900-second deadline:

```sh
make check RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$'
make check REFERENCE=1 RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$'
make check RUN='^Test(Role(Attributes(DefaultsAndRetention|Authority|MembershipNonInheritanceAndAdminTraversal|ConnectionLimitLifecycle|ConnectionLimitRaces|PasswordStorage|PasswordSASLprep|ValidityInput|PublicationAndSnapshots|ControlsRetention)|AuthorityAndSurface)|FormatEncodingCurrentRoleAndResetAll)$'
```

The format fixture uses the canonical attribute executor with a real captured
StatementScope. Trusted connector NOLOGIN specifically asserts FATAL28000 with
complete diagnostics; missing/miscased native names remain ordinary host errors.
All other opaque-key, Driver isolation, authenticator and replacement witnesses
remain selected by the broader role family above. Public pg_roles observations
now have independent dual-mode coverage; protected pg_authid probes remain
PostgreSQL-only. Membership authority witnesses run on native too. Native controls and explicit-transaction exclusions are
independent capability phases, never discovery filters.

Each invocation has a900-second deadline. Existing Array/Domain/Enum,
SearchPathStoredViewRuntime, StringCoreReviewRestore and
TableCoreAuthorizationAndOwnershipCleanup reference definitions also exercise
the corrected catalog allocator/ownership seam without changed expectations.

### Role membership oracle-first definitions

The shared `TestReferenceRoleMembership` family includes the migrated dependency
orders, option defaults and omission updates, inherited owner access, grantor
provenance, ADMIN delegation and support cycles, initial memberships, aliases,
dependent revocation, diagnostics and notices, prepared permission freshness,
transaction boundaries and native host-control phases. PostgreSQL catalog
inspection supplements shared SQL witnesses; it does not replace them.

Initial PostgreSQL18.0 oracle discovery passed before production changes.
The temporary discovery selector is removed; completed quick and real-mode
checks execute the same shared scenario bodies. Discovery observations do not
replace native behavioral-red or final dual-mode validation.

Run every invocation under its own 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceRoleMembership'
make check RUN='^TestReferenceRoleMembership'
make check RUN='^TestReferenceRoleMembershipFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRoleMembershipFeatureSmoke$'
make check RUN='^Test(Membership|Role|Authorization|SessionIdentity|ViewReplace)'
```

The feature smoke requires inherited owner row7 with INT4 metadata versus
42501 denial, ADMIN delegation versus denial, atomic RESTRICT and recursive
CASCADE, combined initial clauses, ALTER GROUP add/drop, disabled-option cycle
rejection and PUBLIC lookup rejection. Explicit native transaction/control
phases document actual API boundaries rather than skipping shared SQL behavior.

Inherited schema ownership is established by shared privileged
`CREATE SCHEMA ... AUTHORIZATION owner`, not a native database-GRANT API.
Functional failure paths remove the nonpublic fixture schema before roles.
`SchemaOwnerSequenceDrop` additionally distinguishes inherited schema DROP
authority over another role's sequence from sequence-owner-only ALTER rights.
Type ownership uses supported DROP DOMAIN; excluded DROP/ALTER TYPE is not
enabled merely to provide a membership witness.
Keyword-promotion regression checks retain the existing CHECK/NOT NULL
`NO INHERIT` consumers and genuinely unsupported role options, not selected attributes.
Class-mask units stage immutable type ACL descriptors through catalog validation,
as existing domain fixtures do, without extending the production ACL setter;
they include invalid-mask nonpublication and explicit-empty inherited-owner
grant-option assertions.
Internal planner units supplement public SQL with immutable edge copies,
exact option flags, selected-origin CASCADE, invalid identity/revision guards,
cancelled candidates, concurrent cycle publication, inherited class masks,
explicit-empty owner use versus implicit options and exact column ACL unions.
The shared `Batch` regressions additionally cover GRANT current-candidate
grantor freshness and rollback, all-recipient GRANT sanity before ADMIN/notices,
and forward/reverse RESTRICT warning order. REVOKE authority uses statement-entry
visibility: its successful batch removes p, warns for q's missing attributed row,
then a separate q revoke denies ADMIN authority. Repeated REVOKE targets that
change the same original tuple fail XX000 `tuple already updated by self`,
emit no missing-row warning, and preserve the original membership and owner row7;
a later single-target revoke succeeds. These corrected contracts were observed
on pinned PostgreSQL18.0 before the native remedy; final remedy verification
remains root-owned and is not implied by those discovery passes.
Positive post-failure delegation, normal notices and CASCADE witnesses ensure
errors neither suppress legitimate messages nor alter the original graph.
Internal ordered-candidate units check GRANT grantor selection, nonpublication
and immutable reads. Entry-authority units check the real explicit-grantor
REVOKE row, one publication and next-statement denial. Indexed-action units
cover recursively deleted later recipients, within-target duplicate matches,
cross-target tuple-action overlap, quiet ADMIN FALSE no-ops, repeated absent
warnings and independent origins. INHERIT/SET actions remain updates even when
already false; do not incorrectly treat them as ADMIN-style no-ops.

```sh
make check RUN='^TestReferenceRoleMembershipBatch'
make check REFERENCE=1 RUN='^TestReferenceRoleMembershipBatch'
```
The full-role consumer selector must also run in both reference modes:

```sh
make check RUN='^TestReference(Role|SessionIdentity|ViewReplace)'
make check REFERENCE=1 RUN='^TestReference(Role|SessionIdentity|ViewReplace)'
```

### Role attribute coverage and feature smoke

The `TestReferenceRoleAttributes` R1–R7 family covers CREATE/ALTER ROLE and
USER defaults/aliases, direct attribute authority, replication-target and
special-attribute matrices, diagnostics/atomicity, physical admission,
password verifier mathematics/SASLprep and expiry metadata, omission/control
retention and a composed feature smoke. Shared public membership actions show
special attributes are not inherited, transitive ADMIN works independently of
INHERIT/SET, and changing member INHERIT only changes future default edges.
Public pg_roles scenarios supplement shared native/reference coverage; privileged
PostgreSQL pg_authid/pg_auth_members probes remain oracle-only without adding
those native providers. Internal units independently check complete descriptors,
immutable snapshots, verifier keys and lifecycle races.
`TestReferenceRoleAttributesContextualMembershipNames` additionally covers all
five contextual payload words in initial and ALTER GROUP comma-list positions.
`TestReferenceRoleAttributesSelfPasswordCancellation` exercises ordinary effective
self-PASSWORD with a valid high-iteration verifier: PostgreSQL statement timeout
reports57014, native returns its context deadline, both recover the same owner
and release the limit-one physical slot. Native units interrupt after a known
derivation step and assert unchanged roots/allocator plus RequestEnd completion.

The current `RUN='^TestReferenceRole'` selector covers every affected role
definition, including the two review regressions and trusted-connector boundary;
run it in both native and pinned modes before review. Each selected check has a
900-second deadline. Finalcheck remains the post-merge gate under one
1800-second deadline.

```sh
make check RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'
make check REFERENCE=1 RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'
```

Expected smoke: a CREATEROLE actor creates and alters its target through automatic
creator ADMIN; ALTER USER retains omitted attributes/password/expiry. Limit1
admits one physical handle, denies the second FATAL53300 and physical close permits
a replacement. Unrelated ALTER fails42501 and held ALTER immediately loses authority
after CREATEROLE revocation. NOLOGIN denies new physical admission FATAL28000 without
terminating the incumbent. Native trusted expired-password admission is independent
of PostgreSQL password authentication; it is not a password handshake parity claim.
Limit0/-1, idle/reuse/original-authenticator accounting and superuser exemption
are covered by admission/lifecycle scenarios, not inferred solely from smoke.
Role rename/settings, password handshake and catalogs remain adjacent omissions;
native explicit transactions and existing DROP/COMMENT gates remain bounded.

### Predefined role oracle-first contracts

The untagged `reference/roles/predefined_*_test.go` definitions exercise all
sixteen documented identities, implicit database-owner membership, restrictions
and pinned diagnostics, real read/write/schema/sequence consumers, REINDEX and
transactional lock exclusion, monitoring settings, lifecycle controls and explicit
absent-provider boundaries. Every supported shared operation runs direct and
prepared routes with independently specified rows, metadata, counts and complete
diagnostics. The safe `pg_roles` projection, including all sixteen predefined
identities, is supported and tested on both backends. Privileged PostgreSQL
`pg_authid`, hidden-setting/statistics and policy-less RLS supplements remain
explicitly separate from native unsupported providers.

Oracle-first discovery uses these small exact groups with `REFERENCE=1`, followed
by native red before production changes. The temporary PostgreSQL-only discovery
guard has been removed; integrated definitions always discover both backends.
All four groups, including every new definition and all three test smokes, must
subsequently pass both modes:

```sh
make check REFERENCE=1 RUN='^TestReferencePredefined(RoleIdentities|DatabaseOwner|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(AllData|Sequences|DataFeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(Maintain|MaintainFeatureSmoke|MonitoringSettings)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(UnavailableCapabilities|RLSBoundary|BoundaryFeatureSmoke|Lifecycle)$'
```

The review-remedy acceptance definitions add actual automatically updatable view
INSERT/UPDATE/DELETE, owner-versus-invoker source authority and held owner revoke;
safe catalog OIDs/direct flags/password mask/typed NULLs observed by an ordinary
actor; and parameter/NULL table and sequence state with held revoke/regrant.
Existing definitions remain intact. Discover these independent expectations on
the pinned oracle first, then use the matching native selector:

```sh
make check REFERENCE=1 RUN='^TestReferencePredefined(ViewWrites|SafeCatalog|ParametersAndNulls)$'
make check RUN='^TestReferencePredefined(ViewWrites|SafeCatalog|ParametersAndNulls)$'
```

Each definition has `prepared=false` and `prepared=true` subtests; held statements
add execution-time authority rechecks in both routes. Supported updatable views
are not an unavailable-provider boundary: materialized and DISTINCT view write
errors are separately asserted, with unchanged physical data.

Use the same four selectors without `REFERENCE=1` for native proof, with an
external 900-second deadline per command. These are definitions, not verification
claims. Native host-control phases have no invented PostgreSQL Reset/Restore API.
Fresh native engines remap builtin ownership; actual dump replay is additionally
consumed by PostgreSQL whenever that backend is enabled.
The safe PostgreSQL subscription witness uses `connect=false`, `slot_name=NONE`
and a harmless conninfo password; even disconnected creation validates password
presence for a nonsuperuser. Cleanup is registered before scenario creation and
unconditionally drops the scenario subscription and revokes its introduced
database CREATE privilege before role teardown, including assertion-failure paths.

PostgreSQL COPY denial is exercised directly. Its outside-transaction prepared
route also asserts lib/pq's exact ordinary driver error, not a fabricated SQLSTATE.
Lifecycle pre-canceled admission is deterministic native-only evidence; the
PostgreSQL branch independently grants inside a transaction, rolls back, and
asserts no authorization publication. This rollback witness is not claimed as
PostgreSQL cancellation proof.

Selected absent file/signal/scan calls independently expect native0A000
`functions are unsupported`, position8, with no invented function Hint.
PostgreSQL missing-function42883 diagnostics are unchanged. This preserves the
existing parser exclusion instead of implementing absent functions to match a
proposed golden. Native DROP likewise retains the prerequisite actual-SUPERUSER
gate: creator ADMIN/CREATEROLE still deny42501 before current/session checks.
PostgreSQL creator current/session55006 remains a backend-specific witness;
privileged bootstrap current-user55006 and builtin pins/missing-name preflight
remain directly and prepared SQL-exercised on both backends.

All-data INSERT RETURNING independently preserves table42501 on both backends.
Read-dependent UPDATE/DELETE denial and constant writer effects remain separate
assertions; global write rights never imply RETURNING SELECT rights.
The independent-driver lifecycle witness restores through a host-control pool,
then observes rows, types and affected counts through the existing recorded
notice-pool wrapper for that same connector. PostgreSQL dump replay resets
imported session authorization before cleanup, without changing replay results.


Separate root-owned non-test executable smoke evidence must use public driver,
authentication and database/sql APIs, without test fixtures or internal hooks.
Create its source ephemerally in the assigned worktree, execute it under an
external 900-second deadline, and retain the source and actual argv/exit/stdout/
stderr in the part verification evidence. Remove the ephemeral source afterward;
durable repository reproduction commands are the reference selectors above.
It independently asserts public grants/revokes and held prepared authorization,
data rows/counts and sequence10/11, REINDEX INDEX/TABLE, ACCESS EXCLUSIVE NOWAIT
exclusion/release and complete truthful unavailable diagnostics. Test-smoke exits
do not replace this executable evidence. No executable native RLS, OS-program,
filesystem, physical-checkpoint or replication-provider parity is claimed.

Function-level initialization, allocation/exhaustion/publication fences, implicit
MEMBER/INHERIT/SET (without ADMIN), capability/column/protected-class masks, real
maintenance roots, immutable snapshots, principal remapping and control retention
are independently asserted without adding a second public SQL test surface:

```sh
make check RUN='^TestPredefined(RoleInitialization|RoleAllocation|ImplicitDatabaseOwner|MembershipRestrictions|CapabilityMasks|CapabilityGrantSource|MaintainRebuild|RoleSnapshots|RoleControls|NoBypassRLS|UnavailableSyntax)$'
```

The grant-source proof checks actual no-op table/column GRANT WITH GRANT OPTION
warnings, unchanged catalog/role authority, and independently absent recipient
rights, rather than treating global use rights as grant options. Existing
semantic split/reordered public ACL and renamed-public lifecycle/control
regressions remain substantive behavioral roundtrips:

```sh
make check RUN='^Test(AuthorizationDumpRejectsChangedNamespaceState|SchemaPublicLifecycleControls)$'
make check RUN='^TestReferenceRoleAttributesRetentionAndControls$'
make check REFERENCE=1 RUN='^TestReferenceRoleAttributesRetentionAndControls$'
```


Canonical public ownership also changes the existing bootstrap schema replay
definition. Its original owner is now pg_database_owner; ordinary recreated
schemas still retain their real creator/selected owner:

```sh
make check RUN='^TestReferenceSchemaPublicLifecycleControls$'
make check REFERENCE=1 RUN='^TestReferenceSchemaPublicLifecycleControls$'
```

Run every command with an external 900-second deadline. These selected proofs
supplement, rather than replace, the root-owned whole-project Docker-free
quickcheck and final verification.

### Row-security policy definitions

Public direct/prepared witnesses are the untagged `reference/roles/rls_policy*`
families. They independently specify grammar, retained ALTER fields, ownership,
role multiplicity, exact diagnostics/notices, provider/column dependencies,
native controls, lock/cancellation lifecycle and unchanged native rows.
Conditional rename witnesses retain release18's accepted-but-nonskipping
IF EXISTS behavior; do not reuse DROP IF EXISTS notice expectations.
Internal `TestRLSPolicy` units exercise real capture/bind/render/validation,
catalog remapping and canceled publication, supplementing public witnesses.

Root-selected verification, with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceRLSPolicy'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicy'
make check RUN='^TestRLSPolicy'
make check RACE=1 RUN='^TestReferenceRLSPolicy(Ownership|Controls|Lifecycle|ConditionalRename)$'
make check RACE=1 RUN='^TestRLSPolicy(Publication|Remap)'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyDefaultDenyPostgres$'
```

The original last selector is intentionally PostgreSQL-only: its applicable
USING(TRUE) pre-drop reader witness requires expression enforcement. Shared
`TestReferenceRLSBaseLastPolicyDrop` separately certifies native last-policy DROP,
retained enabled/forced flags and actual default deny with honest bypass and
role-inapplicability before DROP. Metadata-only policy conditions do not certify
FILTER/CHECK. Controls retain explicit native API phases; PostgreSQL independently
checks SQL and genuine emitted-stream replay, not an invented Restore API.
The commands specify required proof, not a claim that all selected checks passed.

### Row-security BASE oracle-first contracts

All BASE public scenarios are untagged, shared direct/prepared bodies under
`reference/roles/rls_base_*_test.go`, with independent schemas, rows, counts and
complete diagnostic expectations. Root runs these bounded groups once native
and once with `REFERENCE=1`, external900s each:

```sh
make check RUN='^TestReferenceRLSBase(FlagTransitions|Diagnostics|Transactions|LocksAndCancellation)$'
make check RUN='^TestReferenceRLSBase(DefaultDenyAndInputs|BypassAndACL|Applicability|Freshness)$'
make check RUN='^TestReferenceRLSBase(Writes|Principals)$'
make check RUN='^TestReferenceRLSBase(PolicyPreservation|PredefinedReadAllData|LastPolicyDrop|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceRLSBase(AuthorityWaits|ProposalPrecedence|ExpressionUses|ControlsReplay|Syntax)$'
make check RUN='^TestReferenceRLSBase(FreshAuthority|PredefinedNoInherit|InheritanceFlags|ProposedReadConjunction|DefaultAndCoercion|NestedWritablePrincipals)$'
make check RUN='^TestReferenceRLSBase(MixedAlterBoundary|EmptyExpressionBoundary|SQLPreparedExpressionBoundary|ReadBeforeProposalBoundary)$'
make check RUN='^TestReference(PredefinedRLSBoundary|PredefinedBoundaryFeatureSmoke|RLSPolicyDefaultDenyPostgres)$'
make check RUN='^TestRLSBase(Grammar|FlagsAndPublication|Decision|Controls|CancellationAndLocks|OwnerPublicationFreshness|DeniedTargetHistory)$'
make check RUN='^TestRLSBase(FlagsAndPublication|CancellationAndLocks|OwnerPublicationFreshness|DeniedTargetHistory)$' RACE=1
make check RUN='^TestReferenceRLSBase(AuthorityWaits|LocksAndCancellation|Freshness|Controls)$' RACE=1
```

No discovery skips remain in BASE definitions. Applicable-expression scenarios
assert native Execute0A000 `row-security policy expression enforcement is unsupported`
(ERROR severities, all optional fields empty), and independently specify pinned
PG outcomes. TRUE/FALSE/NULL are not native evaluator shortcuts. Restrictive-only,
omitted qualifiers and role/command-inapplicability really default-deny.
The review-boundary definitions independently retain successful mixed flag/column
metadata ALTER, applicable-expression rejection on physically empty input,
successful SQL PREPARE followed by the EXECUTE boundary, and source/target-read
enforcement preceding a default-denied INSERT proposal. Native full diagnostics
remain distinct from PostgreSQL's real query/proposal outcomes; failed writes
retain exact destination and source rows.
Virtual generated-column creation retains native Execute0A000; the PG virtual
proposal witness does not pretend native virtual columns exist. TABLE OWNER TO
retains native Prepare0A000, while a real private catalog-stage owner publication
unit checks fresh native owner authority.

Pinned PG18 already-denied held statements remain denied through the tested
BYPASSRLS/inherited-owner grants; actual unprepared/new handles use current grants.
Native held execution deliberately remains always fresh. PG active cursors block
same-session flag ALTER55006; native eager cursor captures remain immutable.
Do not fabricate matching lifetime/cache behavior. Actual unchanged native Dump
bytes must replay on pinned PG after production; independently authored flag SQL,
Dump string inspection or a skipped producer cannot replace that evidence.
These selectors and expectations describe required proof, not passing evidence.


Review-remedy witnesses independently cover semantic aggregate ownership and
signature/nesting precedence, REGTYPE-only provider RESTRICT/CASCADE and ALTER
retention, and cascade owner-lock wait/cancel/retry with mixed dependencies.
Provider-only TEMP controls exercise persistent-provider replacement refusal
and compatible TEMP collation/sequence/row-type retention in both clauses.
TEMP domain CREATE remains an explicit native capability boundary, not a
retention success. `TestReferenceRLSPolicyPostgresReplay` executes the actual
native Dump bytes unchanged on pinned PostgreSQL, with existing named roles,
correlated conditions and domain/collation/sequence/REGTYPE providers; it checks
metadata, identities, dependencies and rows independently.

```sh
make check RACE=1 RUN='^TestReferenceRLSPolicy(RegtypeDependencies|RegtypeTemporary|CascadeOwnerLock|AggregateOwner.*|ProviderControls|PostgresReplay)$'
make check RUN='^TestPGDependRegtypeValueReferences'
make check RACE=1 RUN='^TestRLSPolicy'
```

`TestReferenceRLSPolicyRestoreSelectors` executes the same named-session-owner
SET SESSION AUTHORIZATION / CREATE TABLE / CREATE POLICY TO CURRENT_USER,
CURRENT_ROLE, SESSION_USER input on PostgreSQL and through native Restore.
The independent expectation is three occurrences of that named owner, not
the private import-authority principal. PostgreSQL's actual policy OID vector
and native Dump's ordered TO identifier vector supplement full role-dependency
diagnostics: the owner plus three policy targets block DROP ROLE; ALTER TO
PUBLIC releases the targets but not table ownership; dropping the table releases
the owner. Native complete emitted bytes are reimported twice and, in real mode,
replayed unchanged on PostgreSQL with independently expected ownership/targets/rows.
SQL probes and dependency release run through both direct and prepared routes.

Root-owned oracle-first discovery and native-red commands (external900s each):

```sh
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyRestoreSelectors/^prepared=/^postgres$'
make check RUN='^TestReferenceRLSPolicyRestoreSelectors$'
```

After the source remedy, run the complete definition in both modes:

```sh
make check RUN='^TestReferenceRLSPolicyRestoreSelectors$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyRestoreSelectors$'
make check RUN='^TestRLSPolicyRestoreAuthorization$'
make check RACE=1 RUN='^TestRLSPolicyRestoreAuthorization$'
```

These specify intended proof, not observed results. Existing Controls retains
failed-Restore atomicity and trusted differently owned provider round trips;
PostgresReplay retains its independent named-target actual-byte replay.

`TestRLSPolicyRestoreAuthorization` supplements the public witness with real
locked authorization captures: ordinary semantic state equals session state,
trusted policy authority changes only its authorization actor, subsequent
session revisions produce fresh target captures, retained captures stay
immutable, and a live-engine importer flag cannot forge private authority.
The root-owned temporary public policy smoke also imports selector-bearing
SQL under a named stage owner while preserving a different named caller,
asserts the actual Dump's three named targets and reimports its complete bytes.
Its expected quiet stdout is
`policy DDL smoke: create/alter/rename/roundtrip/drop/restore-selectors OK`,
with empty stderr. The session smoke source is not a permanent deliverable.

### Policy frozen-expression renderer regression

Policy conditions use the stored-view expression renderer's analyzed literals,
casts, collations, provider identities and lexical column slots. Their outer
expression frame has no SELECT syntax. SELECT-only correlated-column display
rules must not run during policy validation or export; view display keeps its
existing simple-source spelling.

`TestRLSPolicyFrozenExpression` exercises actual capture, validation, rendering
and recapture, including typed temporal values, typed NULL, domain casts,
collation/sequence/regtype providers, identity expressions, nested correlation,
ANY/ALL, row comparison and nested aggregate/window queries. Rendered SQL must
bind again and redump unchanged. A row-dependent division-by-zero condition must
be stored without evaluation. `TestRLSPolicyRemap` checks the same frozen
provider/cast/literal/correlation context after actual catalog replacement and
retains the original immutable definition.

`TestReferenceRLSPolicyExpressionsRegtype` independently expects direct/prepared
policy DDL to accept domain and row-type REGTYPE constants, typed REGTYPE NULL
and the supported symbolic `CURRENT_USER IS NOT NULL` condition. A shared view
retains the renamed row-type identity rather than a new table with the old name;
its Boolean identity comparisons must return `(true,false)` and policy DDL must
leave both target rows unchanged. Native controls repeat exact Dump/Restore/Dump
equality three times, including policy symbolic identity, current qualified type
names and the view's live identity comparisons. Frozen non-NULL REGTYPE leaves
already carry their type annotation, so replay adds no redundant cast. Typed
NULL retains its explicit source cast. This adds no NAME-to-TEXT capability.

Root-selected proofs, each with an external 900-second deadline:

```sh
make check RUN='^TestRLSPolicy(FrozenExpression|Remap)$'
make check RUN='^TestReferenceRLSPolicy(FeatureSmoke|Expressions)$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicy(FeatureSmoke|Expressions)$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyExpressionsRegtype$'
make check RUN='^TestReferenceRLSPolicyExpressionsRegtype$'
```

The existing direct/prepared feature smoke is the public panic regression;
internal presentation tests supplement, not replace, its dual-mode proof.
These commands describe required verification, not observed passing outcomes.
