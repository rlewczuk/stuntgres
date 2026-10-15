//go:build !noservertests

package reference_test

import (
	"os"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerRestoredGrants(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, entry := range []struct{ db, value string }{{fixture.ServerDatabase, "11"}, {fixture.ServerSecondDatabase, "22"}} {
				w := serverAuthenticated(t, f.DSNForDatabase(b, entry.db))
				serverWantCode(t, w.query(t, "CREATE TABLE server_grant_restore(v integer); INSERT INTO server_grant_restore VALUES("+entry.value+")"), "")
				serverScalar(t, w, "SELECT v FROM server_grant_restore", entry.value)
			}
		})
	}
	b, ok := f.NativeBackend()
	if !ok {
		return
	}
	original, err := os.ReadFile(f.Userfile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(original), "\n"), "\n")
	for i, line := range lines {
		fields := strings.Split(line, ":")
		if fields[0] == fixture.ServerUser {
			fields[2] = fixture.ServerDatabase + "," + fixture.ServerUser
			lines[i] = strings.Join(fields, ":")
		}
	}
	f.Stop(t)
	if err := os.WriteFile(f.Userfile, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Native userfile control is not a fabricated PostgreSQL allowlist API.
	// A revoked network grant must not erase its persisted owner's identity.
	f.Restart(t, true)
	denied := f.DSNForDatabase(b, fixture.ServerSecondDatabase)
	w := serverDial(t, denied)
	serverWantCode(t, w.login(t, denied, 196608), "42501")
	w = serverAuthenticated(t, f.DSNForDatabase(b, fixture.ServerDatabase))
	serverScalar(t, w, "SELECT v FROM server_grant_restore", "11")
	w.Close()
	f.WaitEvent(t, func(event map[string]any) bool {
		return event["message"] == "database restored" && event["database"] == fixture.ServerSecondDatabase
	})
	f.Stop(t)
	if err := os.WriteFile(f.Userfile, original, 0600); err != nil {
		t.Fatal(err)
	}
	f.Restart(t, true)
	w = serverAuthenticated(t, f.DSNForDatabase(b, fixture.ServerSecondDatabase))
	serverScalar(t, w, "SELECT v FROM server_grant_restore", "22")
}
