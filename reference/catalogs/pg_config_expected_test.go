package reference_test

// Observed from the fixture's pinned PostgreSQL 18.6 Debian image. These are
// oracle installation/build values, never native implementation inputs.
func pgConfigPGValues() [][]any {
	return [][]any{
		{"BINDIR", "/usr/lib/postgresql/18/bin"},
		{"CC", "gcc"},
		{"CFLAGS", "-Wall -Wmissing-prototypes -Wpointer-arith -Wdeclaration-after-statement -Werror=vla -Wendif-labels -Wmissing-format-attribute -Wimplicit-fallthrough=3 -Wcast-function-type -Wshadow=compatible-local -Wformat-security -fno-strict-aliasing -fwrapv -fexcess-precision=standard -Wno-format-truncation -Wno-stringop-truncation -g -g -O2 -fstack-protector-strong -Wformat -Werror=format-security -fno-omit-frame-pointer"},
		{"CFLAGS_SL", "-fPIC"},
		{"CONFIGURE", ` '--build=x86_64-linux-gnu' '--prefix=/usr' '--includedir=${prefix}/include' '--mandir=${prefix}/share/man' '--infodir=${prefix}/share/info' '--sysconfdir=/etc' '--localstatedir=/var' '--disable-option-checking' '--disable-silent-rules' '--libdir=${prefix}/lib/x86_64-linux-gnu' '--runstatedir=/run' '--disable-maintainer-mode' '--disable-dependency-tracking' '--with-tcl' '--with-perl' '--with-python' '--with-pam' '--with-openssl' '--with-libxml' '--with-libxslt' '--mandir=/usr/share/postgresql/18/man' '--docdir=/usr/share/doc/postgresql-doc-18' '--sysconfdir=/etc/postgresql-common' '--datarootdir=/usr/share/' '--datadir=/usr/share/postgresql/18' '--bindir=/usr/lib/postgresql/18/bin' '--libdir=/usr/lib/x86_64-linux-gnu/' '--libexecdir=/usr/lib/postgresql/' '--includedir=/usr/include/postgresql/' '--with-extra-version= (Debian 18.6-1.pgdg12+2)' '--enable-nls' '--enable-thread-safety' '--enable-debug' '--disable-rpath' '--with-uuid=e2fs' '--with-gnu-ld' '--with-gssapi' '--with-ldap' '--with-pgport=5432' '--with-system-tzdata=/usr/share/zoneinfo' 'AWK=mawk' 'MKDIR_P=/bin/mkdir -p' 'PROVE=/usr/bin/prove' 'PYTHON=/usr/bin/python3' 'TAR=/bin/tar' 'XSLTPROC=xsltproc --nonet' 'CFLAGS=-g -O2 -fstack-protector-strong -Wformat -Werror=format-security -fno-omit-frame-pointer' 'LDFLAGS=-Wl,-z,relro -Wl,-z,now' '--enable-tap-tests' '--with-icu' '--with-llvm' 'LLVM_CONFIG=/usr/bin/llvm-config-19' 'CLANG=/usr/bin/clang-19' '--with-lz4' '--with-zstd' '--with-libcurl' '--with-libnuma' 'LIBNUMA_CFLAGS= ' 'LIBNUMA_LIBS=-lnuma' '--with-liburing' '--with-systemd' '--with-selinux' '--enable-dtrace' 'build_alias=x86_64-linux-gnu' 'CPPFLAGS=-Wdate-time -D_FORTIFY_SOURCE=2' 'CXXFLAGS=-g -O2 -fstack-protector-strong -Wformat -Werror=format-security'`},
		{"CPPFLAGS", "-Wdate-time -D_FORTIFY_SOURCE=2 -D_GNU_SOURCE -I/usr/include/libxml2"},
		{"DOCDIR", "/usr/share/doc/postgresql-doc-18"},
		{"HTMLDIR", "/usr/share/doc/postgresql-doc-18"},
		{"INCLUDEDIR", "/usr/include/postgresql"},
		{"INCLUDEDIR-SERVER", "/usr/include/postgresql/18/server"},
		{"LDFLAGS", "-Wl,-z,relro -Wl,-z,now -Wl,--as-needed"},
		{"LDFLAGS_EX", ""},
		{"LDFLAGS_SL", ""},
		{"LIBDIR", "/usr/lib/x86_64-linux-gnu"},
		{"LIBS", "-lpgcommon -lpgport -lselinux -lzstd -llz4 -lxslt -lxml2 -lpam -lssl -lcrypto -lgssapi_krb5 -lz -lreadline -lm -lnuma "},
		{"LOCALEDIR", "/usr/share/locale"},
		{"MANDIR", "/usr/share/postgresql/18/man"},
		{"PGXS", "/usr/lib/postgresql/18/lib/pgxs/src/makefiles/pgxs.mk"},
		{"PKGINCLUDEDIR", "/usr/include/postgresql"},
		{"PKGLIBDIR", "/usr/lib/postgresql/18/lib"},
		{"SHAREDIR", "/usr/share/postgresql/18"},
		{"SYSCONFDIR", "/etc/postgresql-common"},
		{"VERSION", "PostgreSQL 18.6 (Debian 18.6-1.pgdg12+2)"},
	}
}

const pgConfigViewDefinition = " SELECT name,\n    setting\n   FROM pg_config() pg_config(name, setting);"
