# reference/temporal_types

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for dual-mode placement,
fixture ownership and pinned PostgreSQL oracle policy.

### Timestamp diagnostics and atomicity

`TestReferenceTimestampDiagnosticsAndAtomicity` creates its fixture inside each
original `timestampBadInputs` input subtest, before backend work. The backend
child owns its assertion closure; no child calls a parent-bound Fatal/FailNow.
Each case retains UTC/ISO MDY initialization and the full original sequence:
é-prefixed typed-literal Prepare failure on an empty table, epoch insertion,
three literal forms through direct execution/fresh Prepare (SELECT also through
Exec), both parameter INSERT forms through direct/prepared execution, and the
epoch-only unchanged read after every original failure. SQLSTATE, English, Hint,
portal Context, rune-counted Position, stages and multirow atomicity remain exact.
No input is grouped away and no live context is refreshed; each independent
case owns the ordinary30s scenario bound.

```sh
make check RUN='^TestReferenceTimestampDiagnosticsAndAtomicity$' REFERENCE=1
make check RUN='^TestReferenceTimestampDiagnosticsAndAtomicity$'
make check RUN='^TestReferenceTimestampDiagnosticsAndAtomicity$' RACE=1
```

Use external900-second deadlines. Standalone public smoke should check actual
Unicode-position22007 diagnostics at Prepare/direct Query/Exec, parameter portal
Context and late-invalid multirow atomicity, with one unchanged1970-01-01 UTC
epoch row after every failure. These commands specify required proof, not
observed passing results.
