package reference_test

import (
	"bytes"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func (f *clusterProgressCase) controls() {
	f.t.Helper()
	if f.b.PG { return }
	old,err := f.worker.QueryContext(f.p.Ctx,"SELECT id AS v FROM cp_heap ORDER BY id")
	if err!=nil { f.t.Fatal(err) }
	defer old.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(f.p.Ctx,f.p.Native,&dump); err!=nil { f.t.Fatal(err) }
	if err = stuntgres.Reset(f.p.Ctx,f.p.Native); err!=nil { f.t.Fatal(err) }
	f.idle()
	if err = stuntgres.Restore(f.p.Ctx,f.p.Native,bytes.NewReader(dump.Bytes())); err!=nil { f.t.Fatal(err) }
	f.idle()
	f.correct(true)
	fixture.CatalogControlNativeValues(f.t,old,1,2,3)
	var again bytes.Buffer
	if err = stuntgres.Dump(f.p.Ctx,f.p.Native,&again); err!=nil { f.t.Fatal(err) }
	if !bytes.Equal(dump.Bytes(),again.Bytes()) { f.t.Fatal("progress or changed logical state leaked into replay") }
}
