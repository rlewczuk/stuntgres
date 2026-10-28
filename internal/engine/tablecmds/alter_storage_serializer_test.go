package tablecmds_test

import (
	"context"
	"strings"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

// SQL outcomes/replay parity belong to reference/alter. This checks the actual
// serializer's canonical ONLY/default and late divergent-index repair protocol.
func TestAlterStorageSerializer(t *testing.T) {
	e:=engine.New()
	for _,query:=range []string{
		`CREATE TABLE storage_parent(t text)`,
		`CREATE TABLE storage_child() INHERITS(storage_parent)`,
		`ALTER TABLE storage_parent ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz`,
		`CREATE INDEX storage_child_dup ON storage_child(t,t)`,
		`ALTER TABLE ONLY storage_child ALTER t SET STORAGE DEFAULT`,
	} {enginetest.Run(t,e,query)}
	var script strings.Builder
	if err:=engine.Dump(context.Background(),e,&script);err!=nil {t.Fatal(err)}
	text:=script.String()
	child:=strings.Index(text,`ALTER TABLE ONLY "public"."storage_child" ALTER COLUMN "t" SET STORAGE EXTENDED;`)
	index:=strings.Index(text,`CREATE INDEX "storage_child_dup"`)
	repair:=strings.Index(text,`UPDATE pg_catalog.pg_attribute SET attstorage=E'e',attcompression=E'',attname=E't1' WHERE attrelid=E'"public"."storage_child_dup"'::regclass AND attnum=2;`)
	if child<0 || index<0 || repair<0 || child<index || repair<child {t.Fatal("constructor-complete policy/index repair ordering",text)}
	if strings.Contains(text,`ALTER TABLE "public"."storage_parent" ALTER COLUMN "t" SET STORAGE`) {t.Fatal("recursive parent replay")}
	if authority:=strings.LastIndex(text[:repair],"SET SESSION AUTHORIZATION DEFAULT;");authority<index {t.Fatal("late repair lacks authenticated authority")}
}

func TestAlterStorageInheritanceRetirementSerializer(t *testing.T) {
	e := engine.New()
	for _, query := range []string{
		`CREATE TABLE storage_history_parent(t text,gone text)`,
		`ALTER TABLE storage_history_parent ALTER gone SET STORAGE EXTERNAL, ALTER gone SET COMPRESSION pglz`,
		`CREATE TABLE storage_history_child() INHERITS(storage_history_parent)`,
		`CREATE TABLE storage_history_grand() INHERITS(storage_history_child)`,
		`ALTER TABLE ONLY storage_history_parent ALTER t SET STORAGE MAIN`,
		`ALTER TABLE storage_history_parent DROP COLUMN gone`,
	} { enginetest.Run(t, e, query) }
	var script strings.Builder
	if err := engine.Dump(context.Background(), e, &script); err != nil { t.Fatal(err) }
	text := script.String()
	consumer := strings.Index(text, `CREATE TABLE "public"."storage_history_grand"`)
	retired := strings.Index(text, ` SET STORAGE EXTERNAL;`)
	drop := strings.Index(text, ` DROP COLUMN `)
	final := strings.Index(text, `ALTER TABLE ONLY "public"."storage_history_parent" ALTER COLUMN "t" SET STORAGE MAIN;`)
	if consumer < 0 || retired < consumer || drop < retired || final < strings.LastIndex(text, ` DROP COLUMN `) {
		t.Fatal("retirement must follow consumers and precede DROP; live policy follows all constructors", text)
	}
	if authority := strings.LastIndex(text[:final], "SET SESSION AUTHORIZATION DEFAULT;"); authority < drop {
		t.Fatal("late component completion lacks bounded authenticated authority")
	}
}
