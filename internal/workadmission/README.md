# internal/workadmission

This standard-library-only leaf package owns cooperative request workload-credit
admission. `With` attaches an inert policy using ordinary context values; nil
shadows and disables an inherited policy. Only a known-unlocked runtime owner may
`Activate` a local execution context. Operation/command and binder contexts stay
inert; the exact caller source remains cancellation-classification authority,
never an admission site. `Rebase` transfers only an active workload callable
when the executor selects a different effective runtime context: the selected
context alone owns cancellation, deadline, cause and unrelated values.
`Suspend` shadows only activation at an existing nested binding boundary, leaving
the policy available for a later local runtime activation. `Request` gives the
unlocked runtime owner the inert application policy for its external-work handoff
and post-grant readmission, without an intermediate context or per-quantum allocation.
Active capability identity is a private pointer, never equality on arbitrary
caller context interfaces, which may have uncomparable dynamic types.
`ActivateRuntime` installs the core owner's callable separately from the original
inert request policy. Suspending a supplied callback context for a new operation
therefore cannot resurrect a completed scope's runtime wrapper.

`Admit` checks the effective context before calling the policy and after a nil
grant. A refusal is returned unchanged. It does not manufacture SQL diagnostics,
intercept `Err`, recreate cancellation or grant SQL/catalog authority. Without a
policy activation and admission allocate nothing. Policies must honor the supplied
context and support concurrent execution. Each grant is consumed for one quantum,
not held across nested providers. Synchronous reuse of the busy connection is
forbidden; independent connections remain available.

The runtime hands off any replacement reservation before external admission and
retains the ordinary immutable input/transaction/publication fences. No callback
runs under engine, session, transaction-state, settings, store, replacement or
lock-manager mutexes. Physical transaction command admission remains owned, as for
other external provider work. Binding and locked codecs/publication never activate
policies. Plans, descriptions and immutable values do not own policies.

## Semantic quanta

Classes describe selected executed work, not exact callback counts, uniform CPU,
memory or time units, or complete query accounting. Cached work need not consume
credit. Provider owners preserve cancellation and lazy/error/NULL precedence.

- Pattern normalize/match: at most 4096 character/matcher loop iterations.
- JSON array count, object expand, strip nulls and record expand: scanner token
  steps; strip-null copying additionally uses bounded byte chunks.
- JSONB containment: traversal frame steps; object/array expansion: one present
  member/element; existence: search candidates; path delete: path/copy steps;
  pretty: structural node and bounded copy steps; record populate: field/frame
  steps. A structural step does not bound scalar string length.
- Array transform: one element after child arguments have completed.
- Calendar arithmetic: one nonzero month/day stage after existing precedence.
- Encoding conversion: at most 4096 bytes per pass.
- Password bcrypt: one fixed-state round; password SHA: bounded hash-byte writes,
  not a variable-length complete round.
- Aggregate input: one support/input initialization before child materialization.
- Recursive union: one producer row/round step.
- TSVector conversion: one token dispatch after immutable catalog capture, not
  document parsing or universal full-text CPU accounting.

## Testing

Private units assert attachment/activation separation, effective context authority,
refusal identity, terminal-grant rejection, nested disabling, replacement context
rebasing and the allocation-free disabled path. Run the family with
`make check RUN='^TestWorkAdmission'`. Public SQL lifecycle and real credit-wait
witnesses belong in the dual-mode reference packages, not here.
