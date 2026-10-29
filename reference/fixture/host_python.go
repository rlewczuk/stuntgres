package fixture

import (
	"context"
	"fmt"
	"github.com/testcontainers/testcontainers-go/exec"
	"io"
	"os"
)

var hostPython bool

// WithHostPython provisions the real PL/Python provider in the existing pinned
// oracle only. Native engines acquire no language or conversion capability.
func WithHostPython() StartupOption {
	return func() { hostPython = true }
}

func provisionHostPython(ctx context.Context) error {
	code, reader, err := Container.Exec(ctx, []string{"sh", "-eu", "-c", hostPythonInstall}, exec.Multiplexed())
	if err != nil {
		return fmt.Errorf("PL/Python provisioning: %w", err)
	}
	output, readErr := io.ReadAll(reader)
	fmt.Fprintf(os.Stderr, "reference PL/Python provisioning container=%s image=%s:\n%s\n", ContainerID, referenceImage, output)
	if readErr != nil {
		return fmt.Errorf("PL/Python provisioning output: %w", readErr)
	}
	if code != 0 {
		return fmt.Errorf("PL/Python provisioning exited %d", code)
	}
	return nil
}

// apt verifies signed metadata and dependency archives. The exact old provider
// is independently pinned by HTTPS URL and release SHA256, not whatever version
// today's PGDG index happens to advertise. Audit before installing without any
// further downloads; never permit apt to replace the server.
const hostPythonInstall = `
export LC_ALL=C DEBIAN_FRONTEND=noninteractive
version=18.6-1.pgdg12+2
provider=postgresql-plpython3-18
url=https://apt.postgresql.org/pub/repos/apt/pool/main/p/postgresql-18/postgresql-plpython3-18_18.6-1.pgdg12+2_amd64.deb
hash=6171e4838831eec91dc54c145e759372d2ba2542c9213bafdd026f82b67997af
server=$(dpkg-query -W -f='${Version}' postgresql-18)
test "$server" = "$version"
test "$(dpkg --print-architecture)" = amd64
printf 'server-before=%s provider-url=%s provider-sha256=%s\n' "$server" "$url" "$hash"
# The pinned image supplies the Debian and PGDG signing keys and sources.
for source in /etc/apt/sources.list /etc/apt/sources.list.d/*; do
 test -f "$source" || continue
 while IFS= read -r line; do
  case "$line" in *trusted=yes*|*Trusted:\ yes*) echo "refusing unauthenticated source: $source" >&2; exit 1;; esac
 done < "$source"
done
apt-get -o APT::Update::Error-Mode=any -o Debug::Acquire::gpgv=true -o Acquire::AllowInsecureRepositories=false -o Acquire::AllowDowngradeToInsecureRepositories=false update
# The pinned image has no CA bundle. Bootstrap it from the already authenticated
# apt snapshot before contacting the provider's HTTPS URL; never weaken TLS.
# Resolve and pin the exact candidate from that snapshot, not a moving image.
apt-cache policy ca-certificates > /tmp/stuntgres-python-ca.policy
cat /tmp/stuntgres-python-ca.policy
ca_version=
while IFS= read -r line; do
 case "$line" in *Candidate:*) set -- $line; ca_version=$2;; esac
done < /tmp/stuntgres-python-ca.policy
test -n "$ca_version"
test "$ca_version" != '(none)'
apt-get -s --no-install-recommends --no-remove install "ca-certificates=$ca_version" > /tmp/stuntgres-python-ca.plan
cat /tmp/stuntgres-python-ca.plan
while IFS= read -r line; do
 case "$line" in
  Remv\ *) echo "refusing CA prerequisite removal: $line" >&2; exit 1;;
  Inst\ *) set -- $line; case "$2" in
   ca-certificates|openssl|libssl3) ;;
   *) echo "refusing unexpected CA prerequisite: $line" >&2; exit 1;;
  esac;;
 esac
done < /tmp/stuntgres-python-ca.plan
verify_archives() {
 for archive in /var/cache/apt/archives/*.deb; do
  test -f "$archive" || continue
  dpkg-deb -f "$archive" Package Version Architecture
  sha256sum "$archive"
  package=$(dpkg-deb -f "$archive" Package)
  package_version=$(dpkg-deb -f "$archive" Version)
  if test "$package" = "$provider"; then
   echo "$hash  $archive" | sha256sum -c -
   continue
  fi
  apt-cache show "$package=$package_version" > /tmp/stuntgres-python-dependency.metadata
  expected=
  while IFS= read -r line; do
   case "$line" in SHA256:\ *) expected=${line#SHA256: };; esac
  done < /tmp/stuntgres-python-dependency.metadata
  test -n "$expected"
  echo "$expected  $archive" | sha256sum -c -
 done
}
apt-get --download-only --yes --no-install-recommends --no-remove install "ca-certificates=$ca_version"
verify_archives
apt-get --no-download --yes --no-install-recommends --no-remove install "ca-certificates=$ca_version"
test "$(dpkg-query -W -f='${Version}' ca-certificates)" = "$ca_version"
test "$(dpkg-query -W -f='${Version}' postgresql-18)" = "$server"
test -s /etc/ssl/certs/ca-certificates.crt
dpkg-query -W -f='${Package} ${Version} ${Architecture}\n' ca-certificates openssl libssl3
# apt-helper verifies the independent hash before exposing the downloaded file.
/usr/lib/apt/apt-helper download-file "$url" /tmp/stuntgres-host-python.deb "SHA256:$hash"
test "$(dpkg-deb -f /tmp/stuntgres-host-python.deb Package)" = "$provider"
test "$(dpkg-deb -f /tmp/stuntgres-host-python.deb Version)" = "$version"
test "$(dpkg-deb -f /tmp/stuntgres-host-python.deb Architecture)" = amd64
dpkg-deb -f /tmp/stuntgres-host-python.deb Package Version Architecture Depends
# Audit the exact planned transaction. Only the provider and Bookworm Python
# runtime dependency family may change; no server, JIT, client or removal.
apt-get -s --no-install-recommends --no-remove install /tmp/stuntgres-host-python.deb python3.11 > /tmp/stuntgres-python-plan
cat /tmp/stuntgres-python-plan
while IFS= read -r line; do
 case "$line" in
  Remv\ *) echo "refusing removal: $line" >&2; exit 1;;
  Inst\ *) set -- $line; case "$2" in
   postgresql-plpython3-18|libpython3.11|libpython3.11-minimal|libpython3.11-stdlib|python3.11|python3.11-minimal|libexpat1|libsqlite3-0|media-types|mime-support|mailcap|libbz2-1.0|libcrypt1|libdb5.3|libffi8|liblzma5|libncursesw6|libnsl2|libreadline8|libssl3|libtinfo6|libtirpc3|libtirpc-common|libzstd1|zlib1g) ;;
   *) echo "refusing unexpected package: $line" >&2; exit 1;;
  esac;;
 esac
done < /tmp/stuntgres-python-plan
apt-get --download-only --yes --no-install-recommends --no-remove install /tmp/stuntgres-host-python.deb python3.11
# The CA and Python transactions share the same authenticated archive checks.
verify_archives
apt-get --no-download --yes --no-install-recommends --no-remove install /tmp/stuntgres-host-python.deb python3.11
test "$(dpkg-query -W -f='${Version}' postgresql-18)" = "$server"
test "$(dpkg-query -W -f='${Version}' "$provider")" = "$version"
dpkg-query -W -f='${Package} ${Version} ${Architecture}\n' postgresql-18 postgresql-plpython3-18 libpython3.11 libpython3.11-stdlib libpython3.11-minimal
/usr/bin/python3.11 --version
`
