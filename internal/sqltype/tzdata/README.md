# Embedded PostgreSQL 18 timezone data

The executable rule authority is the immutable PostgreSQL 18.0 UTF8/C oracle image:

```
postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854
```

That image was observed to configure PostgreSQL with `--with-system-tzdata=/usr/share/zoneinfo` and to contain Debian tzdata `2025b-0+deb12u2` (IANA 2025b, including backzone). `zones.zip` contains the exact image TZif bytes for Zone and Link names listed in its `tzdata.zi`; it is not compiled from the different PostgreSQL source-tree timezone snapshot. For example, the image's Europe/Amsterdam history preserves the seconds-resolution offset +00:19:32 in 1800.

`Default` is the exact installed `/usr/share/postgresql/18/timezonesets/Default` snapshot. The root independently downloaded [the REL_18_0 source copy](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/timezone/tznames/Default) and observed byte-for-byte equality: both files were 27,214 bytes. `Australia` and `India` are installed snapshots acquired with docker create/cp from that same pinned image, at `/usr/share/postgresql/18/timezonesets/Australia` and `/usr/share/postgresql/18/timezonesets/India`. Their actual INCLUDE Default / OVERRIDE directives build immutable regional tables once. Runtime selects only these three stock sets; customized installation files and runtime filesystem loading are unavailable.

The maintained `generate` command accepts an acquired directory containing `zoneinfo/`, `Default`, `Australia`, `India`, and `tzdata-copyright`:

```
go run ./internal/sqltype/tzdata/generate -source /absolute/path/to/acquired-evidence -output internal/sqltype/tzdata
```

It sorts names, uses uncompressed ZIP entries with fixed 1980 timestamps and read-only permissions, preserves TZif bytes, and emits `SHA256SUMS`. Generation reads only the explicitly supplied acquisition directory; runtime uses the embedded archive and abbreviation snapshot, never the host timezone database, network, or operating-system local timezone. The root ran this command successfully for the pinned image acquisition.

`tzdata-copyright` preserves the image's Debian/IANA license and provenance notices. `PostgreSQL-COPYRIGHT` preserves the PostgreSQL license applicable to the three abbreviation snapshots and release-source-derived timezone algorithms. See [PostgreSQL REL_18_0 COPYRIGHT](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/COPYRIGHT).

Timezone lookup view enumeration uses the canonical member spellings in this
runtime-loaded archive, including aliases, not ResolveZone's request/POSIX cache.
The inventory is the supported installed Z/L set, not PostgreSQL's entire
filesystem enumeration. No unsupported names, offsets or daylight flags are
fabricated. Labels and flags come from actual TZif types at the captured
transaction-start second; stock abbreviations additionally retain their fixed D
flags and dynamic historical meanings. All inventory, alias and abbreviation
behavior is version-sensitive to these assets.
