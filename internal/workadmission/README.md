# internal/workadmission

This standard-library-only leaf package owns cooperative request workload-credit
admission. `With` attaches an inert policy using ordinary context values; nil
shadows and disables an inherited policy. Only a known-unlocked runtime owner may
`Activate` a local execution context. Operation/command and binder contexts stay
inert; the exact caller source remains cancellation-classification authority,
never an admission site. `Rebase` carries an active callable to the executor's
selected context without replacing its cancellation, deadline, cause or values.
`Suspend` disables activation at nested binding boundaries while leaving the
inert policy available for later runtime work. `Request` exposes that policy to
the unlocked runtime owner; `ActivateRuntime` installs its runtime callable
separately, so a completed scope cannot be resurrected through a borrowed context.

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

## Testing

Private units assert attachment/activation separation, effective context authority,
refusal identity, terminal-grant rejection, nested disabling, replacement context
rebasing and the allocation-free disabled path. Run the family with
`make check RUN='^TestWorkAdmission'`. Public SQL lifecycle and real credit-wait
witnesses belong in the dual-mode reference packages, not here.
