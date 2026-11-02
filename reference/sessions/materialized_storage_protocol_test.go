//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageProtocol(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE MATERIALIZED VIEW ms_wire AS SELECT 1 AS id,'kept'::text AS note"), "")
			serverWantCode(t, w.query(t, "CREATE INDEX ms_wire_idx ON ms_wire(id)"), "")
			for n, q := range []string{
				"ALTER MATERIALIZED VIEW ms_wire ALTER id SET STATISTICS 7",
				"ALTER MATERIALIZED VIEW ms_wire ALTER id SET (n_distinct=2)",
				"ALTER MATERIALIZED VIEW ms_wire ALTER note SET STORAGE EXTERNAL",
				"ALTER MATERIALIZED VIEW ms_wire ALTER note SET COMPRESSION pglz",
				"ALTER MATERIALIZED VIEW ms_wire SET (fillfactor=70)",
				"ALTER MATERIALIZED VIEW ms_wire CLUSTER ON ms_wire_idx",
				"ALTER MATERIALIZED VIEW ms_wire SET ACCESS METHOD heap",
				"ALTER MATERIALIZED VIEW ms_wire SET TABLESPACE pg_default",
				"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT",
			} {
				name := fmt.Sprintf("ms_alter_%d", n)
				frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name))
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "1tnZ")
				serverWantReady(t, frames, 'I')
				for _, frame := range frames {
					if frame.tag == 't' && (len(frame.body) != 2 || frame.body[0] != 0 || frame.body[1] != 0) {
						t.Fatal("utility has parameters", frame.body)
					}
				}
				// Parse/Describe has not executed the utility.
				if n == 0 {
					serverScalar(t, w, "SELECT attstattarget IS NULL FROM pg_attribute WHERE attrelid='ms_wire'::regclass AND attname='id'", "t")
				}
				frames = serverCycle(t, w, serverBind("", name, nil, nil), serverDescribe('P', ""), serverExecute("", 0))
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "2nCZ")
				serverWantReady(t, frames, 'I')
				transactionalViewsWireCommand(t, frames, "ALTER MATERIALIZED VIEW")
				frames = w.query(t, q)
				serverWantCode(t, frames, "")
				transactionalViewsWireCommand(t, frames, "ALTER MATERIALIZED VIEW")
				serverScalar(t, w, "SELECT note FROM ms_wire", "kept")
			}
			frames := w.query(t, "ALTER TABLE ms_wire SET (fillfactor=80)")
			serverWantCode(t, frames, "")
			transactionalViewsWireCommand(t, frames, "ALTER TABLE")
			serverScalar(t, w, "SELECT attstattarget FROM pg_attribute WHERE attrelid='ms_wire'::regclass AND attname='id'", "7")
		})
	}
}
