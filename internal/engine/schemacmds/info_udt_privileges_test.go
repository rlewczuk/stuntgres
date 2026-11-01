package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine/pgcatalog"
)

// Both entrypoints exercise the same authority, domain and ACL invariants; the
// origin matrix independently distinguishes unconditional PUBLIC visibility.
func TestInfoUdtPrivilegesMetadata(t *testing.T) {
	testUdtPrivilegesMetadata(t, pgcatalog.InfoUdtPrivilegesOID, 13468, 13467)
}

func TestInfoUdtPrivilegesOrigins(t *testing.T) {
	testUdtPrivilegesOrigins(t, pgcatalog.InfoUdtPrivilegesOID)
}

func TestInfoUdtPrivilegesCapture(t *testing.T) {
	testUdtPrivilegesCapture(t, pgcatalog.InfoUdtPrivilegesOID)
}

func TestInfoUdtPrivilegesCaptureFrontier(t *testing.T) {
	testUdtPrivilegesCaptureFrontier(t, pgcatalog.InfoUdtPrivilegesOID)
}

func TestInfoUdtPrivilegesPopulation(t *testing.T) {
	testUdtPrivilegesPopulation(t, pgcatalog.InfoUdtPrivilegesOID)
}

func TestInfoUdtPrivilegesReciprocalAuthority(t *testing.T) {
	testUdtPrivilegesReciprocalAuthority(t, pgcatalog.InfoUdtPrivilegesOID)
}
