# internal/engine/schemacmds

Schema-object and extension statement orchestration over lower DDL/feature
owners. Core owns runtime authority and publication; this family owns utility
stages and lock/callback windows, not a separate transaction path.

## Layering

Imports core, tablecmds' shared family hosts and decision packages, never the root.
Composition dispatches here. Core retains authorization capture, stage lifecycle,
statement lock admission, dependency controllers and final installation.

## Entry points

Unlocked dispatch enters with nothing held. Resolve and stage metadata under
Engine.Mu, release it for waits, provider validation and actual executable work,
then revalidate original object/namespace/principal predecessors. Locked
continuations enter with the target admitted and release Engine.Mu on return.

Schema elements and nested extension actions share the existing stage and
journal. Native Restore helpers require authentic isolated-import authority;
metadata directives or names cannot authorize arbitrary definitions or bodies.

## Hosts

`RoutineHost` and `SchemaHost` embed core's DDLHost with `{E, Op, CatalogStage}`
and implement focused lower-package contracts. `extensionHost` captures one view
and wraps the stage; it is not a type alias for CatalogStage. Compile-time
assertions protect the seams. Pre-stage decisions receive a host without a stage.

## Extensions

`extension` owns records, providers, staging decisions and immutable installation
identity. The executor owns readiness, operation context, stage cleanup and
unlocked provider/runtime validation. Script consumers bind through the same
unpublished stage; accepted definitions retain genuine provider/dependency
provenance. Membership/name changes do not synthesize executable capability.
Physical initialization belongs to the current session owner and is not rolled
back by pretending to reverse a catalog descriptor edit.

## Testing

External `schemacmds_test` tests use composed engines and `enginetest` fixtures.
Use genuine staged provider installations and admitted runtime contexts.
Test-only accessors stay beside their owner; public SQL lives in reference
schema, extension, routine and type packages under
[docs/TESTING.md](../../../docs/TESTING.md).
