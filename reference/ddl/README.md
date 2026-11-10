# reference/ddl

Dual-mode scenarios for transactional catalog publication across DDL statements
and session-owned query-derived tables. Type lifecycle belongs to
[types](../types/README.md#testing); table actions to [alter](../alter/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main(m)`.
Direct and prepared routes share one backend body. Explicit native-driver phases
use public connectors and `database/sql`; native controls remain separate from
shared SQL assertions.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Catalog publication: `RUN='^TestReferenceEnumAlterFillReown'`
- Column-key publication: `RUN='^TestReferenceAlterAddColumnPrimaryKey'`
- Conditional table actions: `RUN='^TestReferenceAlterTableIfExists'`
- Session-owned CTAS: `RUN='^TestReferenceCreateTempTableAs'`
