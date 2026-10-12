# reference/fixture

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Fixture reset isolation coverage

`TestReferenceFixtureResetIsolation` and
`TestReferenceFixtureSessionAuthorizationReset` independently cover this boundary:

```sh
make check RUN='^TestReferenceFixture'
make check REFERENCE=1 RUN='^TestReferenceFixture'
```

Schema-owner introspection through `pg_namespace` is PostgreSQL-only. Native
reset isolation independently asserts bootstrap principals, fresh session
settings, an empty public `Dump`, and same-name empty-table recreation. Its
native-only public `Reset` phase removes actual inserted data/catalog state,
preserves bootstrap principals, and again permits empty-table recreation;
it does not add a native `pg_namespace` compatibility surface.

### Held plain-method consumers

The shared fixture.HeldStatement retains its three-field layout and adds actual
plain DB/Tx Prepare/Exec/Query consumers alongside existing Context consumers.
Its matched query uses the genuinely held statement; unmatched queries use the
underlying plain DB/Tx, never a Context stand-in. This central fixture change also
requires supplemental identical all-reference risk selection (each900 seconds):

```sh
make check RUN='^TestReference'
make check REFERENCE=1 RUN='^TestReference'
```

### Session authorization reset coverage

`TestReferenceFixtureSessionAuthorizationReset` retains its schema-owner and
empty-public assertions and now deliberately contaminates authorization, SET
ROLE, settings, TEMP, named PREPARE, advisory state and an aborted transaction
before both reset boundaries. The same definition runs without Docker, proving
fresh wrapped/native control settings, identities, prepared consumers and case
catalogs; admin-only sanitation checks run in pinned mode. It does not assert
incidental backend PIDs, reconnection counts or timing. Focused gates, each with
a 900-second deadline:
`make check RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`
and
`make check REFERENCE=1 RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`.

### Fixture ownership regressions

Fixture ownership regressions live beside the common exported helpers in
`reference/fixture/fixture_native_isolation_test.go` and
`reference/fixture/fixture_notice_ownership_test.go`. These identical definitions
run in both modes (900 seconds per invocation):

```sh
make check RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
make check REFERENCE=1 RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
```

These selectors do not replace whole-project Docker-free `make quickcheck`
verification or post-merge `make finalcheck`. Use `RACE=1` on a selected
`make check` command for bounded race proof, and `REFERENCE=1 RACE=1` for the
same definitions against pinned PostgreSQL. Reduced engine retention is an
ownership correction, not evidence of a measured performance improvement.
