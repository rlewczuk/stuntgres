package visibility

import (
	"testing"
	"stuntgres/internal/engine/ids"
)

func TestSnapshotSyncAdvertisement(t *testing.T) {
	f:=newFixture(t)
	source,writer,observer:=f.tx(),f.tx(),f.tx()
	f.reg.SetLastAssignedXIDLocked(200)
	if err:=f.reg.WriteLocked(writer);err!=nil{t.Fatal(err)}
	old,err:=f.reg.SnapshotLocked(source,false);if err!=nil{t.Fatal(err)}
	f.reg.RetainExportSnapshotAtLocked(source,old,100)
	f.reg.DropSnapshotLocked(source)
	if !f.reg.SnapshotOlderLocked(source,old){t.Fatal("statement end withdrew retained export")}
	f.reg.EndTransactionLocked(writer,true)
	fresh,err:=f.reg.SnapshotLocked(source,false);if err!=nil{t.Fatal(err)}
	if fresh<=old {t.Fatal("RC command reused exported horizon as its current snapshot")}
	f.reg.RetainExportSnapshotAtLocked(source,fresh,150)
	if f.reg.Entries[source].ExportXmin!=old {t.Fatal("new export advanced oldest")}
	f.reg.WithdrawSnapshotLocked(source)
	if !f.reg.SnapshotOlderLocked(source,old){t.Fatal("withdraw lost export")}
	f.reg.ResetPhaseLocked(source)
	if !f.reg.SnapshotOlderLocked(source,old){t.Fatal("phase reset lost export")}
	if cutoff,err:=f.reg.VacuumCutoffLocked();err!=nil||cutoff!=100{t.Fatal("public full-XID retention confused writer order",cutoff,err)}
	entry:=f.reg.Entries[source];entry.Xmin,entry.Snapshot=old-1,true;f.reg.Entries[source]=entry
	if !f.reg.SnapshotOlderLocked(source,old-1){t.Fatal("older current command not advertised")}
	f.reg.DropSnapshotLocked(source)
	if f.reg.SnapshotOlderLocked(source,old-1){t.Fatal("ended command kept its transient horizon")}
	if refs:=f.reg.OlderSnapshotsLocked(observer,old);len(refs)!=1||refs[0]!=source{t.Fatal("export observer",refs)}
	f.reg.EndTransactionLocked(source,false)
	if f.reg.SnapshotOlderLocked(source,old){t.Fatal("whole end leaked advertisement")}
}

func TestSnapshotSyncSSIImportedFrontier(t *testing.T) {
	f:=newFixture(t)
	f.reg.SetCommitSeqLocked(10)
	creator,ender:=f.node(false),f.node(false)
	f.reg.SetCommitSeqLocked(20)
	f.reg.Serializable.order=7
	ref:=f.tx();f.reg.EnrollSerializableLocked(ref,true)
	imported:=f.reg.CaptureImportedSerializableLocked(ref,false,false,10)
	if imported.Frontier!=10||imported.SnapshotOrder!=7 {t.Fatalf("mixed data/SSI domains: %d/%d",imported.Frontier,imported.SnapshotOrder)}
	address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:123}
	// The ended version was born strictly after the retained F=10 but before
	// current G=20. Its creator, not its ender, is the invisible write witness.
	ender.Writes[address]=map[WriteKey]SerializableWrite{{Version:2,Deletion:true}:{Row:1,Version:2,Base:2,BaseBorn:15,Creator:creator.Ref,Deletion:true}}
	if err:=f.reg.SerializableReadLocked(imported,address);err!=nil{t.Fatal(err)}
	if _,ok:=imported.Outgoing[creator];!ok{t.Fatal("imported old frontier lost invisible creator evidence")}
	if _,ok:=imported.Outgoing[ender];ok{t.Fatal("used current frontier and selected the wrong ender")}
	readOnly:=f.tx();f.reg.EnrollSerializableLocked(readOnly,true)
	ro:=f.reg.CaptureImportedSerializableLocked(readOnly,true,false,10)
	if ro.Frontier!=10||ro.SnapshotOrder!=7||ro.Exempt||len(ro.Possible)==0{t.Fatal("RO import cloned source exemption instead of current population")}
}
