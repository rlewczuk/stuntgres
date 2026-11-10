# proc — immutable routine definitions and private values

Reusable lower-level routine owner. It imports parser and scalar facilities,
never catalog or core. It owns immutable declarations/programs and invocation-
private frames, not namespace selection, permissions or publication.

`Definition` retains original body form/source, positioned syntax, formal
modes/names/usages/defaults and checked result structure. SQL-standard recipe
metadata is distinct from quoted source and executable checked programs.
Invocation kind is explicit: ordinary, DML-trigger and event-trigger entry are
not inferred from body shape or public spelling.

Inputs use declaration-owned typed slots. Written expressions evaluate once in
source order and validated assembly maps them, hidden defaults and variadics to
private formals. Result plans validate actual scalar/member conversions through
shared tuple adaptation; nominal identity alone does not prove member generation
or set cardinality. Failed conversion publishes no partial public result.

Each invocation owns its Frame and local values. No shared Definition/PLProgram
retains a host, context, activation, record shape from another invocation or
cached authority. Core owns balanced admitted lexical activation, routine depth,
current settings/effective user and session-private analysis specialization.

## Statement walk

`PLRun` walks one checked program through a `PLHost`. Host methods take explicit
values and run with nothing held; core implements actual expression/SQL/cursor/
assignment and transaction capabilities. Lexical slots and RECORD shapes belong
to the current activation.

Control transfers use non-error flow. Exception marks and source diagnostics
retain genuine command/checkpoint ownership; success releases marks and errors
restore/format through the ordinary owner. Static/dynamic SQL uses the admitted
statement and restores lexical query/parameter/snapshot state on every exit.
Sequential assignment and captured row/cardinality semantics stay explicit in
the walk, not hidden inside an alternative executor.

## Testing

Pure declaration/frame/result/PL-walk units live here. Core tests cover live
routine, checkpoint and invocation ownership; public SQL belongs to reference
routine packages under [docs/TESTING.md](../../../docs/TESTING.md).
