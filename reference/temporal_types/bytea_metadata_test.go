package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceByteaNullAndEmptyMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct {
				name, decl string
				nullable   int
				nullRow    bool
			}{
				{"empty", "BYTEA", 1, false}, {"nulls", "BYTEA", 1, true}, {"required", "BYTEA NOT NULL", 0, false},
			} {
				referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE "+tc.name+"(v "+tc.decl+")", 0))
				var want [][]any
				if tc.nullRow {
					referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("INSERT INTO "+tc.name+" VALUES(NULL)", 1))
					want = [][]any{{nil}}
				}
				for _, prepared := range []bool{false, true} {
					referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v FROM "+tc.name, prepared, []string{"v"}, []int{tc.nullable}, want, false)
				}
			}
		})
	}
}

func TestReferenceByteaComputedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec("CREATE TABLE t(id INT,v BYTEA,r BYTEA NOT NULL)", 0))
			referenceByteaExec(t, p.Ctx, b.DB, b, fixture.Exec(`INSERT INTO t VALUES(1,'',''),(2,NULL,'\x41'),(3,'\x0080ff','\xff')`, 3))
			want := [][]any{{[]byte{}, []byte{}, []byte{}}, {nil, []byte{65}, nil}, {[]byte{0, 128, 255}, []byte{255}, []byte{0, 128, 255, 0, 128, 255}}}
			for _, prepared := range []bool{false, true} {
				for _, empty := range []bool{false, true} {
					q := "SELECT v,r,v||v AS doubled FROM t"
					rows := want
					if empty {
						q += " WHERE v=NULL"
						rows = nil
					}
					q += " ORDER BY id"
					referenceByteaRaw(t, p.Ctx, b.DB, b, q, prepared, []string{"v", "r", "doubled"}, []int{1, 0, -1}, rows, true)
				}
			}
			referenceByteaRaw(t, p.Ctx, b.DB, b, "SELECT v,r,v||v AS doubled FROM t ORDER BY id", false, []string{"v", "r", "doubled"}, []int{1, 0, -1}, want, false)
		})
	}
}
