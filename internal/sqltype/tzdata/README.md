# Embedded PostgreSQL 18 timezone data

The executable rule authority is the immutable PostgreSQL 18.0 UTF8/C oracle image:

```
postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854
```

The image uses `--with-system-tzdata=/usr/share/zoneinfo` and Debian tzdata
`2025b-0+deb12u2` (IANA 2025b, including backzone). `zones.zip` retains its exact
TZif bytes for Zone/Link names from `tzdata.zi`, not a source-tree timezone build.

`Default`, `Australia` and `India` are installed abbreviation snapshots from
`/usr/share/postgresql/18/timezonesets/` in that image. Their INCLUDE/OVERRIDE
directives build immutable regional tables; runtime never loads custom files.

The maintained `generate` command accepts an acquired directory containing `zoneinfo/`, `Default`, `Australia`, `India`, and `tzdata-copyright`:

```
go run ./internal/sqltype/tzdata/generate -source /absolute/path/to/acquired-evidence -output internal/sqltype/tzdata
```

It sorts names, uses uncompressed ZIP entries with fixed 1980 timestamps and read-only permissions, preserves TZif bytes, and emits `SHA256SUMS`. Generation reads only the explicitly supplied acquisition directory; runtime uses the embedded archive and abbreviation snapshot, never the host timezone database, network, or operating-system local timezone.

`tzdata-copyright` preserves the image's Debian/IANA license and provenance notices. `PostgreSQL-COPYRIGHT` preserves the PostgreSQL license applicable to the three abbreviation snapshots and release-source-derived timezone algorithms. See [PostgreSQL REL_18_0 COPYRIGHT](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/COPYRIGHT).

Inventory uses canonical embedded archive names, including aliases, not a
request/POSIX lookup cache or host filesystem enumeration. Zone transitions and
abbreviation meanings are version-sensitive to these assets.
