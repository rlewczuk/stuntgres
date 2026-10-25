package engine

import (
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// The binder reads stored view definitions only through its host: volatility
// of a CTE body's stored sources, and the input captures their retained
// expressions need, both across nested views.
func TestAnalysisHostReadsStoredViews(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE TYPE host_mood AS ENUM ('sad', 'ok')",
		"CREATE TABLE host_t(n INT, m host_mood)",
		"CREATE VIEW host_plain AS SELECT n FROM host_t",
		"CREATE VIEW host_moods AS SELECT m FROM host_t",
		"CREATE VIEW host_nested_moods AS SELECT m FROM host_moods",
		"CREATE VIEW host_sequences AS SELECT sequencename FROM pg_sequences",
		"CREATE VIEW host_nested_sequences AS SELECT sequencename FROM host_sequences",
	} {
		sessionSQL(t, s, q)
	}
	read := e.CatalogRead()
	auth := AuthorityOf(catalogAuthorization(e, read))
	host := e.AnalysisHost(nil)
	relation := func(name string) plan.Node {
		return &plan.SourceNode{Ref: catalogRef(t, e, read, name)}
	}
	for name, want := range map[string]bool{"host_plain": false, "host_moods": false, "host_nested_sequences": true} {
		if volatile, err := host.StoredViewVolatile(auth, relation(name)); err != nil || volatile != want {
			t.Errorf("StoredViewVolatile(%s) = %v, %v; want %v", name, volatile, err, want)
		}
	}
	for name, want := range map[string]int{"host_plain": 0, "host_nested_moods": 1} {
		d := read.MustRelation(catalogRef(t, e, read, name).Address)
		var diagnosed []*sqltype.Descriptor
		err := host.CaptureRetainedViewInput(auth, d, func() error { return nil }, func() error { return nil }, func(typ *sqltype.Descriptor) error {
			diagnosed = append(diagnosed, typ)
			return nil
		})
		if err != nil || len(diagnosed) < want || want == 0 && len(diagnosed) != 0 {
			t.Errorf("CaptureRetainedViewInput(%s) diagnosed %d types, err %v; want at least %d", name, len(diagnosed), err, want)
		}
		for _, typ := range diagnosed {
			if !typ.BaseType().IsEnum() {
				t.Errorf("CaptureRetainedViewInput(%s) diagnosed %v, want the enum", name, typ)
			}
		}
	}
}
