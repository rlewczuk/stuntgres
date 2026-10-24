package fixture

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/ngrok/sqlmw"
)

// RecordedNativePool opens the normal recording adapter over a supplied native
// connector, including fresh-driver Restore destinations. The caller keeps its
// unwrapped control pool; both pools share only this connector's real engine.
func RecordedNativePool(t *testing.T, connector driver.Connector) (*sql.DB, *Recorder) {
	t.Helper()
	wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, &Recorder{RawBackend: RawNative}
}
