# PG18-ARCH-AUTHORIZATION — Authorization identities, ACLs, and enforcement boundaries

## 1. Decision status, scope, and evidence

This is a **future design contract**, not implemented authorization or passing-test evidence. It consumes only [SESSION](PG18-ARCH-SESSION.md) and [CATALOG-IDENTITY](PG18-ARCH-CATALOG-IDENTITY.md); other architecture documents are not authoritative for this revision. Proposed interfaces extend the existing engine, not a new subsystem.

Authorization composes the DSN Engine's cluster principal authority, the selected `DatabaseTarget`'s catalog `ObjectRef` authority, and one physical session's validated `RoleState`. It owns privilege evaluation, delegation semantics, and scoped effective identity. It does **not** own another catalog/store, role-command parser, expression evaluator, transaction visibility algorithm, lock manager, or routine frame stack. Unsupported classes/commands remain unsupported; registering masks is not implementing their substantive effects.

### Grounding and reusable seams

Current code determines extension points; PostgreSQL 18 manuals and release C determine SQL semantics.

| Evidence | Observed seam / decision consumed |
|---|---|
| `driver.go:17-58` | Exact opaque DSN maps to one Engine inside one Driver; Connect currently creates `conn{engine:e}`. Preserve this lifetime/isolation; never parse the DSN as credentials. |
| `conn.go:12-117`; `stmt.go:11-48` | Prepare uses Describe; direct execution and both prepared methods share `conn.execute`. These are the one operation attachment and reevaluation boundary. |
| `internal/engine/engine.go` Engine/describe/Execute; `catalog.go` CatalogStage, `internal/engine/catalog` Read | Existing descriptor authority, identity-keyed storage, shared binder, staged INSERT and retained snapshots. Extend these paths with authorization; string-keyed Engine.tables is already gone. |
| `internal/parser/ast.go:3-78`; `grammar.y:1-85`; `lexer.go:17-64` | Positioned closed statement set, whole-input parsing, unsupported registries, identifier folding/truncation. Command owners extend these paths, never SQL-substring recognition. |
| `internal/engine/control.go:14-44,128-171`; root `control.go:11-65` | Reset/replacement and isolated Restore staging use native host authority; adapter releases borrowed connection before external I/O. No authorization-free alternate SQL executor. |
| `internal/sqlstate/error.go` Error; `engine.go` sqlError | Complete structured error fields already exist. sqlError still attaches public when table is nonempty; authorization must not copy that assumption into new denials. |
| `reference/fixture/fixture.go` fixture.Diagnostic/fixture.AssertDiagnostic/fixture.Prepared | Structured diagnostic assertions already exist. Add authorization fixtures and held physical connection/statement transitions; per-step prepared cases do not prove invalidation. |
| `PG18-ARCH-SESSION.md` §§2–3 | Reuse PrincipalRef, RoleState, AuthorizationAttachment, SessionResource, DatabaseTarget and pointer-owned `Acquire(ctx) (*Operation,error)`. |
| `PG18-ARCH-CATALOG-IDENTITY.md` §§2–7 | Reuse provenance-bearing object/column identity, CatalogRead, NamespaceAccess, CatalogStage and indivisible publication/invalidation; SESSION adds selected-database qualification. Current refs do not yet implement the prerequisite's owner token. |

Release behavior is grounded in PostgreSQL 18 manuals: [privileges §5.8 and Tables 5.1–5.2](https://www.postgresql.org/docs/18/ddl-priv.html), [membership §21.3](https://www.postgresql.org/docs/18/role-membership.html), [GRANT](https://www.postgresql.org/docs/18/sql-grant.html), [REVOKE](https://www.postgresql.org/docs/18/sql-revoke.html), [ALTER DEFAULT PRIVILEGES](https://www.postgresql.org/docs/18/sql-alterdefaultprivileges.html), [SET ROLE](https://www.postgresql.org/docs/18/sql-set-role.html), [SET SESSION AUTHORIZATION](https://www.postgresql.org/docs/18/sql-set-session-authorization.html), [RLS §5.9](https://www.postgresql.org/docs/18/ddl-rowsecurity.html), [CREATE POLICY and its command matrix](https://www.postgresql.org/docs/18/sql-createpolicy.html), [routine security](https://www.postgresql.org/docs/18/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY), [schemata](https://www.postgresql.org/docs/18/infoschema-schemata.html), [tables](https://www.postgresql.org/docs/18/infoschema-tables.html), [columns](https://www.postgresql.org/docs/18/infoschema-columns.html), [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html), and [SQLSTATE Appendix A](https://www.postgresql.org/docs/18/errcodes-appendix.html).

Release source anchors (all [`REL_18_0`](https://github.com/postgres/postgres/tree/REL_18_0), matching the reference minor):
- [`acl.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/acl.c): `aclmask`, `roles_is_member_of`, `has_privs_of_role`, `member_can_set_role`, `is_admin_of_role`, `select_best_grantor` establish owner options, separate closures and grantor selection.
- [`aclchk.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/aclchk.c): `restrict_and_check_grant`, `ExecGrant_Relation`, `SetDefaultACL`, `get_user_default_acl`, `aclcheck_error` establish warnings, relation/column grants, creator templates and ordinary diagnostics.
- [`information_schema.sql`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/information_schema.sql): `columns`, `schemata`, `tables` WHERE clauses establish §6 masks and exclusions; no extra schema-USAGE predicate is present for tables/columns.
- [`rowsecurity.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/rewrite/rowsecurity.c): `get_row_security_policies`, `add_security_quals`, `add_with_check_options` establish command placement, omitted-expression handling and default deny.
- [`rls.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/misc/rls.c): `check_enable_rls` establishes environment-dependent bypass, FORCE and row_security=off error, including when policy predicates would allow every row.

Manual `/docs/18/` identifies **18.6**; preserve reference **18.0**, UTF8/C, `server_version_num=180000`, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Minor-specific differences belong to PG18-REF-ORACLE-18-6; local 20devel is research only. Document author ran no SQL/verification. Root's targeted pinned-oracle observation established W13 direct SELECT and SQL PREPARE/EXECUTE stages; it is not database/sql unnamed-Parse parity or full diagnostic capture. Historical W14 observations were likewise limited. Baseline gates cannot prove future authorization.

## 2. Identity domains and immutable access interfaces

### Authority and ownership

- `PrincipalRef` is an opaque, nonzero engine-owned handle into the DSN Engine's cluster role authority. Names and raw numeric IDs are not capabilities. Database-local owners refer to this shared authority, never per-database role copies.
- Reuse `RoleState{Authenticated,SessionUser,CurrentUser,ResetRole}`. Authenticated is immutable initial identity; SessionUser controls SET ROLE eligibility; CurrentUser is the ordinary effective principal. ResetRole is the connection-time configured role, with zero meaning SessionUser. Attachment initialization/updates validate every reference; callers cannot inject arbitrary RoleState.
- Owner is a `PrincipalRef` in the authoritative class descriptor, not the object's identity and not an ACL bit. `ObjectRef{Epoch,Address}` remains selected-database scoped. Rename/move preserve it; drop/recreate does not. Stable positive `Address.SubID` identifies a column independently of projection offsets and names.
- Cluster roles/membership remain SESSION's cluster authority; object/column ACLs, creator templates and policies are feature payloads in selected-database catalog authority. Catalog shared-owner hooks supply role dependencies. Role drop cannot leave dangling owners, grantors, grantees or policy-role references; REASSIGN/DROP OWNED and dependency checks belong to their command/catalog owners.
- Ownership alteration/destruction rights and implicit grant options cannot be revoked as ordinary privileges. Ordinary owner use rights **can** be revoked. Inherited owning-role authority and actual effective-principal superuser authority are distinct evaluation inputs.

### Proposed vocabulary

Reuse prerequisite types rather than defining replacements. Successive privilege bits start at `1 << 0` in the order below; membership and metadata enum values start at zero in their listed order. These internal encodings are not PostgreSQL catalog encodings.

```go
type PrivilegeMask uint32 // Select, Insert, Update, Delete, Truncate,
// References, Trigger, Create, Connect, Temporary, Execute, Usage, Set, AlterSystem, Maintain.
type MembershipMode uint8 // Member, Inherit, SetRole

type ACLGrantee struct { Public bool; Role PrincipalRef }
type ACLEntry struct {
    Grantor PrincipalRef
    Grantee ACLGrantee
    Privileges, GrantOptions PrivilegeMask
}
type ACLValue struct { Explicit bool; Entries []ACLEntry }
type MembershipEdge struct {
    Role, Member, Grantor PrincipalRef
    Admin, Inherit, Set bool
}
type DefaultACLKind uint8 // Tables, Sequences, Routines, Types, Schemas, LargeObjects
type DefaultACLKey struct {
    Creator PrincipalRef
    Namespace ObjectRef // zero means selected-database-global template
    Kind DefaultACLKind // default-privilege category, not catalog ClassID
}
type AccessRequirement struct {
    Object ObjectRef // whole object or stable column SubID
    Privileges PrivilegeMask
    Owner bool // non-grantable ownership requirement
    GrantOptions bool
    Position parser.Position
}
type AuthorizationStamp struct { Roles, Membership, ACL, Owners, Policies uint64 }
type MetadataSurface uint8 // InfoSchemata, InfoTables, InfoColumns

type AuthorizationView interface {
    NamespaceAccess
    Roles() RoleState
    Stamp() AuthorizationStamp
    HasRole(PrincipalRef, PrincipalRef, MembershipMode) bool
    CanAdmin(PrincipalRef, PrincipalRef) bool
    Effective(ObjectRef, bool) PrivilegeMask // bool selects grant options
    Check(AccessRequirement) error
    Visible(MetadataSurface, ObjectRef) bool
    ForRoutine(ObjectRef, bool) (AuthorizationView, error) // SECURITY DEFINER flag
    RowSecurity(ObjectRef, PolicyUse) (PolicySelection, error)
}
func (op *Operation) Authorization() AuthorizationView
```

The implemented view and checks: [`internal/engine/access`](../../internal/engine/access/README.md).

`HasRole(member,role,mode)` includes self, then traverses unconditional, all-INHERIT or all-SET paths. `CanAdmin(actor,role)` applies ADMIN/grantor rules, not ordinary transitive ACL rights or implicit self-ADMIN. SUPERUSER/LOGIN/CREATEDB/CREATEROLE/BYPASSRLS come from the effective principal, never ordinary inheritance. Membership mutation evaluates provenance/dependencies as well as reachability.

The engine binds one immutable view to the operation's coherent catalog/auth read and target: `Operation.Authorization()` equals `Operation.Resolution().Access`. One Authorization attachment owns validated session state; EndSession releases it, not shared roles or SQL effects. Borrowed slices/descriptors cannot outlive their operation/frame.

`ACLGrantee{Public:true, Role:zero}` is PUBLIC. A real grantee has Public=false and a nonzero role. Reject other combinations. PUBLIC is neither a principal nor a membership endpoint. Grant-option bits must be a subset of ordinary privilege bits; PUBLIC cannot receive grant options. Explicit=false carries no user entries; explicit empty and absent ACL remain distinguishable. Foreign engine/target, zero role handles, stale refs or invalid lifecycle are internal errors at validated interfaces, never implicit owner/PUBLIC/superuser fallbacks. Missing-name SQL diagnostics come from the resolver before access evaluation. Predicate-only interfaces (`CanUse`, `Visible`, `HasRole`) operate on validated inputs, not untrusted handles with silent default allow.

`Effective(ref,false)` unions ordinary rights for CurrentUser; true selects grant options. For columns it includes corresponding table and column rights; for whole relations it reports table ACL rights, not an accidental union over all columns. `Check` requires **all** requested bits and ownership when Owner=true; owner-only requirements have zero mask and GrantOptions=false. Non-owner empty masks are invalid. GrantOptions=true checks delegation rights rather than use. Owner checks accept effective owning-role authority or superuser, but never ordinary ALL alone. Class handlers add their separate restrictions.

Columnless SELECT and row-locking need an **any-live-column** privilege test, not an empty manifest or permission on every column. The binder records that operation-specific quantifier with its bound use and evaluates table rights OR an appropriate live column right using this same view before access; the ordinary per-column `AccessRequirement` remains an all-bits check. `Effective` must not globally redefine relation SELECT to mean any-column, since other checks and metadata need table-specific masks. The query owner supplies these exceptional quantifiers when those SQL forms are implemented.

`ForRoutine`'s flag is taken from the coherent routine descriptor, not client-controlled authority. The routine consumer validates descriptor/ref/flag agreement. Scoped view details are in §7; no public default-allow constructor is permitted.

## 3. ACL evaluation and defaults

PostgreSQL 18 class masks are central authorization inputs, validated against the owning descriptor class:

| Class | Applicable privileges | Built-in PUBLIC grants |
|---|---|---|
| Tables / table-like objects | SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN | None |
| Columns | SELECT, INSERT, UPDATE, REFERENCES | None |
| Sequences | SELECT, UPDATE, USAGE | None |
| Databases | CREATE, CONNECT, TEMPORARY | CONNECT, TEMPORARY |
| Schemas | CREATE, USAGE | None |
| Functions / procedures | EXECUTE | EXECUTE |
| Types / domains / languages | USAGE | USAGE |
| Large objects | SELECT, UPDATE | None |
| Parameters | SET, ALTER SYSTEM | None |
| Tablespaces | CREATE | None |
| Foreign wrappers / servers | USAGE | None |

ALL expands only the target class mask. Table grants do not grant rights on related sequences; sequence/function calls have independent checks. Dormant classes do not get pretend successful operations. The public schema's bootstrap ACL is initialization-specific, not the generic new-schema default.

Evaluation order: validate domain/ref/class; identify effective principal and inherited-role closure; apply actual superuser bypass independently; derive ACL value; union direct, INHERIT-reachable and PUBLIC entries for ordinary use. Column rights union the matching table bits and stable-column entries. There are no negative grants. Grant-option evaluation uses the same supported provenance and owner's implicit options, without treating PUBLIC as grant-option-bearing. Keep grantor/grantee entries and independent paths: flattening to one user mask loses revocation/delegation semantics.

`ACLValue.Explicit=false` means built-in class defaults, not explicit empty ACL and not a live creator template. Built-in defaults give owner initial class rights plus listed PUBLIC grants. First GRANT/REVOKE materializes that state. Explicit=true with zero entries gives no ordinary rights even to owner (superuser remains a separate bypass); owner still holds implicit grant options and ownership authority. For columns, absent column ACL does not create independent owner grants that would defeat revocation of table SELECT; column use includes table defaults/ACL plus only actual column grants.

Creator default ACL templates are applied **once at creation**, under CurrentUser, into the object's initial ACL. They never retroactively affect existing ACLs. Inherited group templates are not creator templates unless the group is CurrentUser. Global templates can override built-in defaults; per-schema templates add to global defaults, never subtract them. A creation whose templates alter defaults materializes an explicit ACL. Descriptor owner, initial ACL and role-dependency payloads are staged and published together.

User decision **2026-10-04, “Use PostgreSQL object categories”** supersedes only the former `DefaultACLKey.Class OID` rule: templates key by creator, namespace/global scope and bounded DefaultACLKind. Tables and sequences share pg_class yet have separate defaults; routines share one category (functions/procedures cannot have separate defaults), and Types includes domains. Catalog ClassID remains object identity, not this category. Only the six supported categories are accepted; no new catalog/kind registry. The key is future vocabulary, not a migration of an implemented template map.

## 4. State transitions, atomicity, and invalidation

### Mutation protocol

Resolve/preflight the complete target set and delegation dependencies; compute immutable replacements/touched revisions; validate the stage; co-publish data, descriptors, ACLs, dependencies and invalidation. Failure/cancellation/invalid targets/RESTRICT/revision exhaustion publishes **nothing and no committed event**. Warning-bearing eligible-mask grants are successful PostgreSQL results, not partial failures.

ACL/membership payloads own grant provenance; catalog owns dependency indexes/shared-owner hooks. No new store, TX visibility, locking, retry or MVCC algorithm is introduced.

| Transition | Authority and resulting state |
|---|---|
| New membership | ADMIN=false, SET=true, INHERIT from member role attribute unless specified. Reject cycles and PUBLIC endpoints before publication. Record grantor and ADMIN support. |
| Existing membership options | Preserve omitted options. Revoking INHERIT/SET affects that option; revoking ADMIN can invalidate dependent grants. RESTRICT fails unchanged; CASCADE removes unsupported grants only, preserving independent support. |
| Object GRANT | Add eligible use/options without erasing independent grants. Object GRANTED BY must identify CurrentUser; actual recorded grantor can be the owning/grant-option-holding inherited role. Superuser object grant is attributed to owner. |
| Membership GRANT | Distinct ADMIN delegation/GRANTED BY rules: actor must possess attributed grantor's authority; recorded non-bootstrap grantor must hold target ADMIN and remains a dependency. Superuser membership grant is attributed to bootstrap grantor. |
| Object REVOKE | Remove only applicable grantor-attributed bits. GRANT OPTION FOR retains ordinary use; table revoke also removes corresponding column grants from that grantor; column revoke cannot subtract table rights. RESTRICT is default. CASCADE recomputes independent support instead of deleting every downstream entry indiscriminately. |
| Partial nonowner grant/revoke | No object privilege at all can cause outright denial. Otherwise only eligible grant-option masks change; emit PostgreSQL warnings for requested unsupported masks as specified for ALL versus explicit privileges. Never silently grant everything, discard warnings, or turn a warning into failed Exec. |
| ALTER DEFAULT PRIVILEGES | Change creator/category/global-or-namespace template. Another creator requires CurrentUser's self/INHERIT-effective creator authority or superuser (`has_privs_of_role`), not MEMBER or SET eligibility alone. Existing ACLs unchanged; schema defaults add, never subtract global/built-in grants. IN SCHEMA is invalid for SCHEMAS/LARGE OBJECTS; column defaults are invalid. |
| Ownership transfer | Inherited current-owner authority plus SET eligibility for new owner, or superuser, with class-specific requirements from the handler. Preserve ObjectRef; transfer owner ACL rights and dependencies atomically. ALL is not ownership. |
| SET ROLE role | Check SessionUser's all-SET path or SessionUser superuser status, not CurrentUser. Change CurrentUser only; advance session environment revision; do not reload target login defaults or rewrite ResetRole. |
| SET ROLE NONE / RESET ROLE | NONE selects SessionUser. RESET selects configured connection-time ResetRole, otherwise SessionUser. Restore baseline rather than last switch; peer sessions are unchanged. |
| SET SESSION AUTHORIZATION | Check initial Authenticated superuser eligibility; otherwise only Authenticated itself may be selected. Success sets SessionUser and CurrentUser, preserving Authenticated and ResetRole. DEFAULT/RESET restores both mutable identities to Authenticated. |

Role/session-authorization transitions fail in a security-definer context. Failure leaves all identities and revisions unchanged. SESSION/LOCAL restoration is consumed by settings/TX owners; this design adds no authorization transaction engine. Where multiple object grantors qualify and PostgreSQL leaves selection unspecified, no portable selection is promised: witnesses use SET ROLE to select one explicit grantor.

### Coherent revisions and prepared execution

```go
type AuthorizationChange struct {
    Roles []PrincipalRef
    Objects []ObjectRef
    Defaults []DefaultACLKey
    Membership bool
}
```

Publish this immutable, deduplicated payload alongside corresponding catalog changes. `AuthorizationStamp` tracks visible role, membership, ACL, owner and policy revisions. Revisions never wrap; exhaustion fails before mutation. Defaults changes invalidate future-creation template decisions, not rewrite existing object ACLs. Role changes also invalidate special-attribute/role-name decisions; membership changes invalidate inherited privilege and policy selection closures.

Cache keys include Engine, DatabaseTarget/incarnation, catalog epoch, effective identity, relevant descriptor/auth revisions, and resolution environment. A cache may be conservative, but a global generation alone must not make unrelated staged changes conflict; use catalog's touched preconditions. TX selects visible versions and publication timing. Retained operations/Rows keep their coherent read; new operations see changes at TX-defined boundaries. Private/discarded changes emit no committed invalidation.

Preparation retains resolved refs and bound requirements, **never permission tokens**. Describe binds/validates its coherent view and performs applicable lookup/RLS-setting preflight, but must not eagerly reject ordinary relation ACLs that PostgreSQL defers to execution. Every Execute validates plan/ref validity and reevaluates object/column rights before row evaluation/access/publication, rebuilding role-dependent RLS selection when needed (§5 precedence). Role/membership/ACL/owner/policy/identity changes cannot retain stale allowance. Replacement invalidates old refs by epoch; revocation does not erase retained Rows.

Namespace USAGE is a lookup privilege: preserve PostgreSQL's documented previously-looked-up statement caveat. Do not promise fresh name lookup on every prepared execution or that schema USAGE revoke alone guarantees inaccessibility. This is distinct from mandatory reevaluation of relation/column ACLs at execution.

### Transactional object and default privileges

Supported object GRANT/REVOKE and ALTER DEFAULT PRIVILEGES stage into the
canonical statement-owned catalog/role pair. Finish merges one journal result;
commit publishes ACL descriptors, defaults, dependencies, created metadata and
rows together. Private creation reads the actual creator's private defaults.
`Journal.RecordCandidateDefaults` omits already-private outputs from committed
input fences. Savepoint marks restore the same pair and lock checkpoint;
discarded work never emits committed namespace invalidation.

Named relation/sequence grants take real AccessShare admission before ACL
candidate planning. A wait may re-resolve a replacement only before a candidate
exists. Staged identities and epochs remain strict. Existing lock-manager
tickets also own bounded catalog-object/default-key writer claims, with normal
wait, cancel, savepoint, end and replacement cleanup; these are not relation AX,
advisory or invented system-row locks. Wait projection uses actual transaction
dependencies. Pinned relation/default writer witnesses preserve XX000 concurrent
tuple failure and absent-default 23505 uniqueness separately; no generic
catalog-conflict-to-SQLSTATE translator or statement retry is introduced.

`access.RoleRecord.Advance` preserves an immutable normalized authority origin.
Actual role-root changes establish a new origin. Actual-role and mixed journals
retain the canonical three-way root merge with principal/member/precondition
checks. Completed ACL-only journals use unchanged authority as a fast path;
otherwise their zero actual principal delta goes through the same explicit
merge. Retained descriptor/default references validate against current merged
authority, while touched descriptors and default predecessors remain exact.
Read refresh retains the exact private role record/base when the external
principal graph is unchanged. A compatible actual-root refresh rebases the
private record to current revision plus the original logical ACL delta, without
reserving allocator space. Early publication preparation keeps that original
delta intact. Final installation uses the maximum of the checked current
revision plus logical delta and the canonical allocator frontier.
Mixed private-root ACL tails reserve their fresh revision
through that same allocator at staging. General authorization/cache stamps stay
exact. Existing detached namespace publication owns the single schema-ACL
generation advance, including overflow rejection.
Selected ACL/default statements capture both private and published immutable role
records in the initial coherent window for the Execute-to-Finish authority fence:
revision-only peer ACL work is compatible, but an actual role-root change is not,
even if an earlier completed ACL left a private role view. This statement
authority expires at successful Finish; it is not a completed outer-commit
fence or a reason to reauthorize/replay an already completed grant.
Other catalog statements retain their
exact numeric role-revision fence. Session authorization, database predecessor
and consulted target/provider identities remain strict in both paths.

Preparation of these utilities is syntax-only and effect-free; Execute owns
read-only/aborted-state precedence and current authority. These directives do not
widen existing role lifecycle/membership or ownership policies, enable ALTER
SYSTEM or language execution/ABI capabilities, or admit SQL PREPARE of these
utilities. Catalog/default writer claims remain database-local; shared principal
dependency/retirement claims use the cluster's stable role domain. Dependency
admission releases Engine.mu for waits and revalidates before retaining results.

## 5. Checkpoints and diagnostic ownership

Extend the shared binder to emit deduplicated `[]AccessRequirement` for actual uses, retaining positions. Combine only equivalent ref/mode requirements; keep ownership and distinct columns separate. Describe/Execute follow §4; §9 removes alternate/default-allow paths.

| Checkpoint | Required decision before effect |
|---|---|
| Admission | Database CONNECT once at startup. Native admission validates principal/target through SESSION; no DSN credential parser or emulated pg_hba/network server. |
| Namespace reference | Qualified lookup checks explicit schema existence/USAGE; unqualified lookup omits missing/unusable schemas. CanUse is a predicate; resolver constructs explicit namespace denial. Finding an object does not authorize using it. |
| CREATE | Qualified destination checks CREATE, not generic USAGE. Unqualified creation chooses first explicitly usable schema then checks CREATE, with no fallthrough after denial. Type/language dependencies require USAGE; temporary creation requires database TEMPORARY through its owner. |
| ALTER / DROP / rename / move | Resolve all targets, then ownership/class-specific requirements. Rename additionally needs containing-schema CREATE; move handler supplies destination requirements. Preflight the entire batch before stage publication. |
| SELECT | SELECT for every referenced column, including predicates, sort, expressions and RETURNING; star expands all query columns. Columnless relation reads require table-or-any-live-column SELECT, not an empty requirement list. |
| INSERT | INSERT on assigned targets, with omission/default target semantics supplied by INSERT owner. Independently check invoked sequences/functions. |
| UPDATE | UPDATE on written columns and SELECT on read value/predicate/RETURNING columns. Row locking additionally requires the documented table-or-any-column UPDATE test. |
| DELETE | Table DELETE and SELECT on referenced read columns. |
| TRUNCATE | Distinct table TRUNCATE right; no RLS row filtering. |
| Callable routine / operator | Caller EXECUTE before routine entry/body effect; implementing function EXECUTE for operators. Existing resolver/signature binder chooses the routine. |
| Policy / RLS | Owner authority for flags/policy DDL. Applicability/setting preflight may precede ordinary ACL rejection; row access/modification requires ordinary privileges and applicable RLS. REFERENCES/integrity exemptions affect RLS only, not ACL. |
| Metadata | Per-surface visibility over the same catalog/auth view. Exposure is never a DML authorization token. |

Authorization constructs ordinary `42501 insufficient_privilege` denials with class-specific primary wording. Representative simple-relation denial: **`permission denied for table secret`**; schema: **`permission denied for schema auth_s`**; ownership: **`must be owner of table secret`**. A column-only denial may still report relation-level wording; do not invent a Column field because a column mask failed. No Schema/Table/Column fields are synthesized merely from refs. Primary messages are PostgreSQL English without the driver's `stuntgres: ` Error() prefix.

| Owner | Diagnostic responsibility |
|---|---|
| Parser | Malformed/unsupported syntax and positions from complete SQL input |
| Resolver | Missing names, qualification and lookup precedence |
| Authorization | Ordinary use/owner denial, delegation eligibility and grant warnings |
| Feature command handler | Command-specific restrictions and scope rules |
| RLS consumer | Applicability/row_security setting diagnostics at PostgreSQL's preflight phase; policy violations during authorized row enforcement |
| PG18-DRIVER-ERROR-FIELDS | Structured severity, primary/detail/hint, positions, optional fields and notice transport; remove unconditional Schema=public |
| Adapter/context/I/O/internal domain | Ordinary Go errors, not invented SQLSTATE |

Appendix A establishes `42501`, `0LP01 invalid_grant_operation` and `2B000 dependent_privilege_descriptors_still_exist`; it does not establish every command's precedence or optional fields. Full English primary/detail/hint/severity/position/object-field presence and absence must be captured independently against pinned 18.0 by each consuming command before exact implementation acceptance. Exact unobserved membership/cascade/RLS/warning wording is **not** claimed here; local 20devel is not a fallback oracle. Warnings require diagnostic-owner notice transport, not silent discard or failed Exec.

User decision **2026-10-04, “Follow PostgreSQL phase precedence”** supersedes unconditional ACL-before-RLS ordering in §§5/8/W13 and §4's eager Describe ordinary-relation ACL rejection. RLS never grants ordinary privileges; selection/preparation is not an access token. PostgreSQL rewrite [`fireRIRrules`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/rewrite/rewriteHandler.c) calls `get_row_security_policies`/`check_enable_rls` before executor [`InitPlan`/`ExecCheckPermissions`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execMain.c). Applicability/setting preflight may therefore reject during preparation before ordinary ACL checks at execution. Preserve these phases using the same coherent view, not a second binder/planner; ordinary requirements remain mandatory before row evaluation/access/modification.

## 6. Metadata visibility versus lookup/access

`Visible(surface,ref)` consumes the operation's coherent catalog/auth view. Provider projects authoritative descriptors and supplies actual kind/temp/dropped-column scope; the authorization predicate is surface-specific:

| Surface | Visibility predicate |
|---|---|
| InfoSchemata | Effective access to owning role (self/INHERIT authority, including superuser) OR schema CREATE/USAGE |
| InfoTables | Effective owner-role access OR any table SELECT/INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER OR any column SELECT/INSERT/UPDATE/REFERENCES |
| InfoColumns | Effective owner-role access OR SELECT/INSERT/UPDATE/REFERENCES on that column, including table-level rights |

MAINTAIN alone does **not** satisfy the release information_schema.tables mask. DELETE-only can expose the table without exposing columns. Table/column providers exclude other sessions' temp schemas; columns exclude system/dropped attributes. Supported relation-kind projection follows release view predicates and the owning provider, not pretend unsupported descriptors. Schemata uses its own release predicate, not blanket table-temp filtering.

Do not additionally require schema USAGE for information_schema table/column exposure: these release predicates intentionally differ. A column grant can expose metadata while qualified SELECT still fails schema USAGE. Schema visibility alone never exposes protected relation/column rows. Search-path visibility, metadata exposure, object existence and access are four different questions. pg_catalog name discovery is not universally privilege-hidden; protected catalog surfaces specify their own actual restrictions. Never turn catalog existence into permission or hide all catalogs under information-schema rules.

Providers retain the same `catalog.Read` and `AuthorizationView`; no mutable current ACL map escapes and no separately writable metadata tables exist. Owner ordinary SELECT revocation does not itself hide owner metadata, because ownership and ordinary ACL use remain distinct.

## 7. Security-definer boundary

`ForRoutine(ref,definer)` checks caller EXECUTE before body effect using the coherent descriptor (§2 validates the flag). Invoker retains caller authority; definer returns an immutable view with **current routine owner** as CurrentUser and unchanged Authenticated/SessionUser/ResetRole. Stored session state is untouched; owner/ref revisions invalidate stale authority.

The future routine-frame owner installs/restores the exact caller view on normal/nested return, error and cancellation and prevents escape. Nested invokers inherit scoped identity and definer restrictions; authorization rejects role/session-authorization switches there. No frame stack or dispatcher is designed here.

Frame/settings owners save/restore settings and search_path. Safe examples use trusted schemas then explicit pg_temp; never rewrite arbitrary paths or promise security. PUBLIC EXECUTE persists until revoked. Policy subqueries use invoking authority except explicit definer calls; other view semantics remain feature-owned.

## 8. RLS selection and expression/enforcement boundaries

```go
type PolicyCommand uint8 // PolicySelect, PolicyInsert, PolicyUpdate, PolicyDelete
type PolicyUse struct {
    Command PolicyCommand
    NeedsSelect bool
    RowSecurityOff bool
}
type PolicyGroup struct {
    Command PolicyCommand
    Permissive, Restrictive []ObjectRef
}
type PolicySelection struct {
    Bypass bool
    DefaultDeny bool
    Using, Check []PolicyGroup
}
```

Policy descriptors hold their table ref, command (including ALL), roles/PUBLIC, permissive flag, and shared bound Boolean USING/WITH CHECK expressions. Persist enabled and forced table flags separately from definitions. DISABLE preserves definitions. Flag/policy DDL requires owner authority. Policy refs and bound dependencies remain catalog-owned.

`RowSecurity` returns immutable selection references, not evaluated rows or expressions, and may perform applicability/setting preflight before ordinary ACL rejection (§5). Consumers must pass ordinary ACL requirements before row evaluation/access/modification; selection cannot grant privileges. Disabled RLS, actual effective superuser/BYPASSRLS, or owner authority without FORCE bypass; FORCE removes ordinary owner bypass, never superuser/BYPASSRLS. `row_security=off` errors when applicable RLS would be applied, regardless of whether predicates would allow all rows.

Select ALL/matching-command policies for PUBLIC or INHERIT-applicable roles. Per group, combine `(OR permissive) AND (AND restrictive)`; conjoin different command groups. Omitted expressions contribute nothing: without a permissive expression for the required use, record DefaultDeny/false, even if policy descriptors exist. Restrictive-only cannot grant. ALL/UPDATE omitted WITH CHECK uses USING; both omitted therefore deny. Explicit `true` grants. INSERT forbids USING; SELECT/DELETE forbid WITH CHECK. Preserve groups for shared expression evaluation.

USING true allows existing row; false/NULL silently suppresses it. WITH CHECK true permits proposed row; false/NULL aborts the **whole command**. Shared expression binder/evaluator supplies Boolean/NULL semantics, dependencies and errors; reject aggregate/window constructs. No authorization-local evaluator is allowed.

### Command placement contract

The query/DML owner determines required classes from actual uses, not only the statement keyword:

| Use | Existing-row groups (`Using`) | Proposed-row groups (`Check`) |
|---|---|---|
| SELECT | SELECT | None |
| SELECT FOR UPDATE/SHARE | SELECT and UPDATE | None |
| INSERT | None | INSERT, plus SELECT when read/RETURNING requires it |
| UPDATE | UPDATE, plus SELECT when reads require it | UPDATE, plus SELECT when reads require it |
| DELETE | DELETE, plus SELECT when reads require it | None |

SELECT's USING expression serves as a proposed-row check in read-required modifying commands; it is not silently applied as output filtering to rejected RETURNING rows. NeedsSelect encodes these ordinary cross-command requirements. Row-locking consumers request SELECT and UPDATE groups explicitly. Later ON CONFLICT/MERGE consumers compose this same selection primitive at each action according to the [release command matrix](https://www.postgresql.org/docs/18/sql-createpolicy.html#SQL-CREATEPOLICY-SUMMARY), including error rather than silent suppression for conflict/action checks; this design does not implement those adjacent commands.

RLS filters precede non-leakproof user predicates; query/expression owners handle leakproof ordering. BEFORE-row transformations precede proposed-row checks; constraints and actual modification/publication follow. Multirow failure/cancellation leaves committed state unchanged. TRUNCATE, REFERENCES and integrity checks retain documented RLS exemptions **without** becoming ordinary ACL exemptions. Selection caches depend on effective identity, membership, flags/policy/owner revisions and relevant environment. TX/query/locking owners define snapshots and race handling, not authorization. Missing expression/TX/locking/routine artifacts do not authorize invented algorithms or new adjacent designs.

## 9. Clean cutover and consumer-sized backlog

No production caller changes in this design session. Future implementations migrate all affected callers and remove old overloads/bypass helpers without compatibility shims:

| Existing/future caller | Cutover responsibility |
|---|---|
| `driver.go` connector.Connect | SESSION initializes validated admission, selected database, CONNECT and one Authorization attachment. Preserve exact DSN/Driver isolation. |
| `conn.go` Close/Ping/PrepareContext/execute and legacy adapters | Consume pointer-owned Operation acquire/release and terminal lifecycle. Pass the same `*Operation` to Describe/Execute; release on all error/cancel paths. Session role transitions never mutate peers. |
| `stmt.go` ExecContext/QueryContext and adapters | Stay on conn.execute; retain refs/requirements under prepared owner, never allow tokens. Reevaluate on held physical connection. |
| `internal/engine/engine.go` Describe/describe/Execute; `catalog.go` descriptors/stages/providers | Extend current binder with masks, positions and any-column quantifiers; stage owner/default ACL with CREATE and enforce before effect. Preserve INSERT atomicity and retained SELECT snapshots. Extend the existing catalog authority, not obsolete string tables. |
| `internal/parser/ast.go`, `grammar.y`, generated `grammar_gen.go`, `lexer.go` keywords/unsupported registries | Each command owner adds positioned grammar/types and removes only syntax it genuinely implements. Full-input rejection, identifier normalization and unsupported behavior remain. No substring parser. |
| DDL/DML/query handlers as introduced | Consume central owner/use requirements and RLS hooks; no parallel privilege-operation capability. |
| CatalogProvider metadata readers | Apply per-surface predicate to coherent authoritative projections. Do not create writable catalog mirrors. |
| Routine frame consumers | Install/restore ForRoutine view at existing invocation boundary under routine-execution owner. No fabricated baseline routine files. |
| Row-access/proposed-row consumers | Apply selected policy groups through shared expression contract at §8 order. These paths are future consumers, not currently implemented code. |
| `internal/engine/control.go` Reset/Restore replacement and stage.Execute; root `control.go` withEngine/Reset/Dump/Restore | Migrate with SESSION/CATALOG signatures and supported serializers; retain native host authority and release-before-I/O. Stage uses ordinary engine machinery, never an authorization-free SQL executor. Replacement validates supported persistent ACL dependencies in the same authority. |
| Direct internal engine test helpers | Migrate with owning signature changes; no default-allow helpers or test-only binder. Tests are not edited here. |

Implementation-time inventory searches: `\.Describe\(|\.Execute\(|resolveRelation|namespaceForName|CatalogStage|ReplaceCatalog` across adapter/control/internal engine; `unsupportedWords|unsupportedIdentifiers|statement:` across parser. Host controls do not fabricate SQL authentication; serializers must preserve supported ACL/default/policy state in complete round trips.

The frozen research maps bounded catalog records to the following consumer capabilities; the parent owns final scheduling/partitioning. Each row is a bounded future session exit, not permission to implement adjacent work now. W numbers refer to §10.

| Capability / backlog symbol | Consumer-ready responsibility | Independent exit witness |
|---|---|---|
| PG18-ROLE-CORE | Distinct principals, validated minimum session authorization, owner/ordinary-check foundation and dependency-aware role removal | W1, W5, W7; owner/nonowner public operations, no dangling refs |
| PG18-ROLE-ATTRIBUTES | Effective-principal special attributes, including BYPASSRLS | W6, W13; inherited ordinary rights do not inherit special attributes |
| PG18-ROLE-MEMBERSHIP | Options/grantors, distinct closures, cycles and ADMIN dependency transitions | W6, W8; complete graph state after RESTRICT/CASCADE |
| PG18-ROLE-SWITCH | SET/NONE/RESET eligibility, configured reset baseline and isolated session views | W6–W7, W11; identities and held-plan decisions |
| PG18-ROLE-GRANT / PG18-ROLE-GRANT-OBJECTS | Table/column and non-table masks, defaults materialization, PUBLIC/delegation and actual use | W2–W4, W8; supported class positive/negative operations |
| PG18-ROLE-REVOKE / PG18-ROLE-REVOKE-OBJECTS | Grantor-specific removal, option-only revoke, dependencies/independent sources | W2, W4, W8, W11; exact surviving rights and warnings |
| PG18-ROLE-DEFAULT-PRIVILEGES | Creator/category/global/schema templates, future-only and additive behavior | W9; old/new/later objects, creator switching and table/sequence template separation |
| PG18-ROLE-OWNERSHIP | Transfer/REASSIGN/DROP OWNED via catalog dependency authority | W5; stable identity, transferred ACLs, atomic denied transfer |
| Existing DDL/DML/query symbols | Central checkpoint consumption, not a new parallel privilege subsystem | W10; per-operation semantic masks and atomicity |
| PG18-INFO-SCHEMATA / PG18-INFO-TABLES / PG18-INFO-COLUMNS | Each exact authoritative projection and visibility predicate | W1–W3; exact exposed rows/column types |
| PG18-ROUTINE-ATTRIBUTES with PG18-ARCH-ROUTINE-EXECUTION | EXECUTE gate and frame-owned invoker/definer scoped restoration | W12; nesting/error/cancel and owner-change validity |
| PG18-ROLE-RLS-BASE | Enable/force/bypass/default deny and ACL prerequisite | W13; expected rows and no invented bypass |
| PG18-ROLE-RLS-POLICY with PG18-EXPR-CORE | Owner-only definitions, Boolean binding/dependencies and invalid syntax | W14; policy refs and shared evaluator truth/NULL cases |
| PG18-ROLE-RLS-COMBINATION | Role/command selection, permissive/restrictive and cross-command grouping | W14; unit groups and independent query results |
| PG18-ROLE-RLS-FILTER | USING ordering, silent existing-row filtering/counts | W14; rows/count zero, unsafe predicate boundary |
| PG18-ROLE-RLS-CHECK | WITH CHECK fallback/order and whole-command failure | W14; before/after transform and sentinel atomicity |
| PG18-DRIVER-ERROR-FIELDS | Full structured diagnostics/notices, no fabricated optional fields | W1, W8, W10, W12–W14; independent English/field captures |

## 10. Independently executable future witnesses

These are **expected future fixtures**, not observed test results. Run each backend independently against specified results before parity. Privileged setup uses the unwrapped oracle admin; feature assertions use deliberately unprivileged roles on held physical `sql.Conn`, not a pool that might switch identities. Exercise direct and prepared public database/sql for supported SQL. Future SESSION/native admission supplies the stuntgres test identity; PostgreSQL may use distinct role connections or controlled session authorization, but initial-authentication tests require genuinely appropriate authenticated roles. No DSN parsing is introduced for stuntgres.

Use unique case roles/schema names, exact rows/types/counts, English diagnostics including optional field absence/presence and positions, and unit tests for semantic masks/graph state rather than wiring/call counts. Compare unordered results as bags with multiplicity; order where asserted. Close statements/connections/rows before reset; remove dependent objects before shared role cleanup. Keep reference serialized, pinned 18.0 UTF8/C; Docker/image failures are nonpassing, never skips/mocks. Existing per-step prepared fixture cannot prove retained-plan invalidation and must be extended by its owner.

### W1 — Unauthorized relation and filtered metadata

Admin creates `owner_role`, `reader_role` and `auth_s` owned by owner_role. Owner creates `auth_s.secret(id integer, confidential text)` and inserts `(1,'x')`; reader receives only schema USAGE. Reader `SELECT id FROM auth_s.secret` fails **42501 / `permission denied for table secret`**, with no mutation. Reader metadata queries:

```sql
SELECT table_name FROM information_schema.tables
 WHERE table_schema = 'auth_s' AND table_name = 'secret';
SELECT column_name FROM information_schema.columns
 WHERE table_schema = 'auth_s' AND table_name = 'secret'
 ORDER BY ordinal_position;
```

Both return `[]`. Owner metadata returns `[('secret')]` and `[('id'),('confidential')]`; owner `SELECT id, confidential FROM auth_s.secret` returns `[(1,'x')]`, types INT4/TEXT. pg_catalog may reveal the name without granting SELECT. Unit checks distinguish missing object from denied existing object, owner from ordinary rights, foreign domains and no default allow.

### W2 — Column rights and table/column union

Owner `GRANT SELECT(id) ON auth_s.secret TO reader_role`. Reader `SELECT id` returns `[(1)]`, INT4; tables exposes secret and ordered columns exposes only id. `SELECT confidential`, `SELECT *`, `SELECT id ... WHERE confidential='x'`, and `SELECT id ... ORDER BY confidential` deny 42501 (simple relation path primary `permission denied for table secret`). Table SELECT exposes both columns and allows both values; column-only revoke cannot subtract it. Table SELECT revoke also removes the same grantor's corresponding column grants. DELETE-only exposes table but not columns; MAINTAIN-only exposes neither under these release masks. Unit requirements cover projection/predicate/sort and stable SubID across rename/drop-recreate, not mutable offsets.

### W3 — Schema visibility is distinct

With no schema rights/owner authority, schemata query for auth_s returns `[]`; grant USAGE or CREATE independently and it returns `[('auth_s')]`. Schema visibility alone exposes no protected table/columns. With reader column SELECT(id) but no schema USAGE, information-schema table/id metadata remains visible, while qualified `SELECT id FROM auth_s.secret` fails **42501 / `permission denied for schema auth_s`**. Explicit schema creation may check CREATE without generic lookup USAGE. Unit cases use the surface masks, not one universal hide rule.

### W4 — PUBLIC and independent paths

Grant PUBLIC SELECT: reader and a role created afterward read `[(1)]`. Add then revoke direct reader SELECT while PUBLIC remains: success still `[(1)]`. Revoke the last PUBLIC source: 42501 denial. Repeat with inherited group and an independent explicit grantor chain: removing one source preserves use until its last support is removed. Unit graph/ACL expectations assert provenance and no negative grants.

### W5 — Owner and ownership transfer

Owner revokes own ordinary SELECT; absent another source its read denies 42501, but ownership ALTER/DROP and implicit grant options remain. Regrant restores `[(1)]`. Nonowner with ALL ordinary table rights fails ALTER/DROP with 42501 / `must be owner of table secret`. Valid transfer needs inherited current-owner authority and new-owner SET eligibility, preserves ObjectRef, and transfers associated owner rights/dependencies. Denied transfer leaves owner/ACL/ref/revisions unchanged. Role removal with unresolved ownership fails without dangling refs; REASSIGN/DROP OWNED outcomes are asserted by their owner.

### W6 — Membership, options and isolated role switching

Use the official graph: admin→joe INHERIT=true; wheel→admin INHERIT=false; island→joe INHERIT=true, SET=false. Grant each role SELECT on a distinct one-row relation representing its rights, with appropriate shared schema access. Joe initially reads joe/admin/island rows, not wheel. SET ROLE admin loses joe/island rights; SET ROLE wheel succeeds through SessionUser's SET chain, despite stopped inheritance. SET ROLE island fails and leaves all identities/revisions unchanged. session_user remains joe; NONE restores joe. Separate connection-time ResetRole fixture proves RESET selects configured baseline, never the last switch. Created objects are owned by CurrentUser. A peer physical session remains joe with its prior rights. Unit cases assert ADMIN=false/SET=true/member-INHERIT defaults, omitted-option preservation, cycle/PUBLIC rejection and distinct closures. Special attributes do not follow ordinary inheritance.

### W7 — Session authorization

Authenticated superuser selects reader: session_user/current_user both become reader; DEFAULT/RESET restores both to Authenticated. Authenticated nonsuperuser cannot select an unrelated role even after permitted CurrentUser changes. Failure leaves identities/revisions unchanged. Settings/TX consumer separately proves SESSION/LOCAL restoration. Use actual authenticated nonsuperuser identity, not only admin SET ROLE, to avoid a false positive.

### W8 — Grant delegation and membership dependency changes

Owner gives reader SELECT WITH GRANT OPTION; reader grants downstream under an explicit chosen grantor. REVOKE GRANT OPTION FOR SELECT RESTRICT refuses with dependency state unchanged; CASCADE retains reader use and removes unsupported downstream use. Independent downstream support survives. Assert PUBLIC grant-option rejection, grantor-specific revocation and eligible-mask warning behavior separately, capturing full pinned-oracle errors/notices (do not assume illustrative error codes establish all precedence). ADMIN grant chains receive equivalent RESTRICT/CASCADE graph assertions; revoking only INHERIT/SET does not masquerade as ADMIN removal.

### W9 — Creator default ACLs

Owner creates old_t with row `(1)` before a schema default template grant. Apply creator/schema SELECT template; create new_t with `(2)`: reader old_t denies, new_t returns `[(2)]`. Revoke template; create later_t with `(3)`: new_t remains readable, later_t denies. Member creation without SET ROLE does not use group's templates; creation as group does. Schema-only routine default revoke cannot subtract global PUBLIC EXECUTE. Invalid scope/unauthorized template changes leave templates/revisions unchanged. Unit values distinguish absent object ACL, explicit empty and materialized creator effects.

Category witness: for the same creator/schema, `ALTER DEFAULT PRIVILEGES ... GRANT SELECT ON TABLES TO reader_role` makes a later table readable but a later sequence's `has_sequence_privilege(...,'SELECT')` false. Separately grant default SELECT ON SEQUENCES: only later sequences gain it; revoke table default and verify later tables deny while later sequences still allow. Assert earlier objects unchanged and unit keys unequal despite common pg_class ClassID. Test all six category masks/scopes; functions/procedures share Routines, domains use Types. A SET-only/non-INHERIT member cannot change the creator's defaults until becoming that creator; INHERIT authority can. Denials preserve templates/revisions.

### W10 — Operation checkpoints and atomicity

Use separate grants to isolate CREATE, owner ALTER/DROP, SELECT projection/read columns, INSERT assigned targets, UPDATE written/read columns, DELETE read columns and TRUNCATE. Unauthorized unqualified CREATE fails at selected schema without publishing or falling through; failing multi-target DROP leaves all targets. Unit manifests assert exact masks and any-column quantifiers, including columnless reads/row locking. Sequence/function use needs its own grant despite table INSERT. A denied multirow command/cancellation leaves sentinel rows and descriptor/auth revisions unchanged. Capture each command's error precedence/English fields independently rather than weakening fixture requirements.

### W11 — Prepared/interleaving and retained rows

Hold reader physical connection and prepared `SELECT id FROM auth_s.secret` across changes. Initial granted execution returns `[(1)]`. Peer revokes and commits; next execution at TX-defined visibility boundary fails 42501, not cached allow. Peer regrants: held statement succeeds again. Repeat membership option, role switch, owner and policy revision changes. Existing Rows remain usable under their retained view. Schema USAGE revoke is a separate prior-lookup-caveat witness, not a promise of re-resolution. Unit stamps cover private/discarded changes with no committed event and foreign-target/stale-epoch rejection. Cancellation cannot install partial grants or emit committed invalidation.

### W12 — Security definer

Owner's definer routine with trusted path followed by pg_temp reads secret. Caller has routine EXECUTE but no table SELECT: routine returns expected `[(1,'x')]`, direct SELECT denies. Inside, current_user is owner and session_user is caller. Exact caller authority restores after normal return, nested invoker/definer calls, SQL error and cancellation. Revoke EXECUTE: invocation denies before a body-side sentinel mutation. Change routine owner: next call uses new owner's rights, not retained old authority. SET ROLE and SET SESSION AUTHORIZATION inside definer reject. Frame consumer captures exact command diagnostics and proves scoped-view lifetime on every exit.

### W13 — RLS bypass, flags and default deny

Table r(id integer) contains `(1),(2)`; reader has ordinary SELECT. ENABLE RLS with no policies gives reader `[]`, not ACL denial. Owner sees ordered `[(1),(2)]` unless FORCE; forced nonsuper owner sees `[]`. Actual superuser/BYPASSRLS still sees both. DISABLE restores ACL-visible rows without deleting policies. row_security=off errors when policy application is required, never grants bypass. TRUNCATE/REFERENCES/integrity exemptions do not override their ordinary ACL requirements. Unit flags/attributes distinguish inherited owner access from actual special attributes.

Root **observed on pinned PostgreSQL 18.0 UTF8/C**: with no reader SELECT on `auth_s.r`, schema USAGE, reader non-owner/non-superuser/non-BYPASSRLS and RLS enabled, `row_security=on` direct `SELECT id FROM auth_s.r` rejects **42501 / `permission denied for table r`**; SQL `PREPARE denied_on AS SELECT id FROM auth_s.r` succeeds, then `EXECUTE denied_on` rejects with that same ordinary denial. With `row_security=off`, direct SELECT and SQL PREPARE both reject **42501 / `query would be affected by row-level security policy for table "r"`** during applicability preflight. These observed SQL stages corroborate release source; no stuntgres authorization was exercised.

Future independent fixtures must reproduce those stages and unchanged data/identity/ACL/policy/revision state, then separately capture public direct/prepared database/sql behavior and full diagnostic fields against both backends. Do not infer unnamed Parse or database/sql preparation behavior from SQL PREPARE alone. Repeat owner/FORCE and actual-superuser/BYPASSRLS cases without treating RLS bypass as an ACL grant; no protected row evaluation/access is allowed without ordinary rights.

### W14 — Policy selection, composition, filters and proposed checks

With ordinary rights and two rows, permissive SELECT USING(id=1) plus USING(id=2), and restrictive USING(id=2), produces ordered `[(2)]`; restrictive-only produces `[]`. Separately, an applicable permissive SELECT policy with omitted USING yields `[]`, while the same policy with explicit USING(true) yields `[(1),(2)]`; unit selection treats omitted expression as no predicate/DefaultDeny, not true. For INSERT, a permissive INSERT policy with omitted WITH CHECK rejects both `(1)` and `(3)` with **42501 / `new row violates row-level security policy for table "insert_omitted"`**; explicit WITH CHECK(true) accepts them. Reader must be non-owner and non-superuser with ordinary SELECT/INSERT/UPDATE grants, so these are policy outcomes rather than ACL failures.

For `ALL USING(id=1)` with omitted WITH CHECK, SELECT returns `[(1)]`, INSERT `(1)` succeeds and `(3)` fails **42501**; explicit `WITH CHECK(true)` instead permits either proposed value. For `UPDATE USING(id=1)` with omitted WITH CHECK, updating visible id 1 to id 3 fails **42501** and leaves rows unchanged, proving the check fallback uses USING; explicit `WITH CHECK(true)` would permit the proposed row. Omitted UPDATE USING itself contributes no existing-row predicate: UPDATE affects **0** rows, unlike explicit USING(true). USING false/NULL silently hides rows; standalone UPDATE/DELETE on suppressed rows affects **0**, not a policy error. Unit groups assert command/ALL and PUBLIC/INHERIT applicability, omitted expressions as absent predicates, no permissive default allow, and AND across command groups.

INSERT WITH CHECK(id=1) accepts 1 with affected count **1**. Multirow `(1),(2)` fails wholly; sentinel contents unchanged; false/NULL check rejects. ALL/UPDATE omitted check reuses USING, not implicit true. UPDATE with read access satisfies both SELECT and UPDATE groups on existing rows and applicable proposed-row checks; RETURNING SELECT checks fail rather than silently hide a newly modified row. BEFORE transformation is supplied by trigger owner before check, constraints afterward. Shared expression tests evaluate true/false/NULL and reject aggregate/window policy expressions; query owner witnesses RLS-before-unsafe-predicate ordering. Capture full exact RLS diagnostic text/fields from pinned 18.0 rather than copying illustrative manual WITH CHECK OPTION wording.

## 11. Acceptance trace and limits

| Selected requirement | Concrete decision / future witness |
|---|---|
| Login/session/current-role identity and membership/owner invariants | §2 interfaces/domain rules; §4 transitions; W5–W7 |
| Object/column ACL, options/delegation, ownership, PUBLIC/defaults | §3 masks/evaluation; §4 atomic changes; W2, W4–W5, W8–W9 |
| Namespace/object/DDL/DML/routine checkpoints and diagnostics | §5 checkpoint/ownership tables; §7 entry gate; W1, W3, W10, W12 |
| Metadata visibility versus existence/access | §6 exact per-surface masks; W1–W3 |
| Definer identity/context and restoration | §7 immutable scoped view/frame boundary; W12 |
| RLS command/role selection, bypass, USING/check, OR/AND and expression contract | §8 interfaces/matrix/order; W13–W14 |
| Session/catalog attachment and invalidation/interleaving | §2 one view/attachment; §4 co-publication/revisions; W11 |
| Clean-cutover production inventory and bounded backlog | §9 caller inventory/symbols with consumer exits; no parallel paths |
| Independent positive/denial/transition scenarios | §10 W1–W14, each specified before parity; no runtime claim |

This artifact specifies future consumer work, not working SQL. Exact unobserved diagnostics and expression/TX/locking/routine algorithms remain with the named owners. Production/tests/prerequisites remain unchanged; the living plan preserves prior rationale and records this revision. Root must run fresh full production gates and independent review; there is no implemented authorization feature smoke in this design-only revision.
