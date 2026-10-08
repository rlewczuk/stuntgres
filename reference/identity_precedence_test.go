//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceIdentityDeclarationSequenceBeforeDefaults(t *testing.T){identityReferenceCase(t,func(t *testing.T,db *sql.DB){
	for _,q:=range []string{`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(CACHE 0))`,`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(CACHE 0),p INT DEFAULT 'oops')`}{_,err:=db.Exec(q);want:=controlsReferenceDiagnostic("22023","CACHE (0) must be greater than zero");referenceAssertDiagnostic(t,err,&want)}
	identityRefExec(t,db,`CREATE SEQUENCE side`,0)
	q:=`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(AS bigint))`;_,err:=db.Exec(q);want:=controlsReferenceDiagnostic("42601","conflicting or redundant options");want.Position=strconv.Itoa(strings.Index(q,"AS bigint")+1);referenceAssertDiagnostic(t,err,&want)
	_,err=db.Exec(`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME public.side))`);want=controlsReferenceDiagnostic("42P07",`relation "side" already exists`);referenceAssertDiagnostic(t,err,&want)
})}

func TestReferenceIdentityDeclarationPersistence(t *testing.T){
	identityReferenceCase(t,func(t *testing.T,db *sql.DB){
		identityRefExec(t,db,`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(UNLOGGED START 7))`,0)
		for _,option:=range []string{"logged","unlogged"}{_,err:=db.Exec(`ALTER TABLE i ALTER id SET `+option);want:=controlsReferenceDiagnostic("XX000",`option "`+option+`" not recognized`);referenceAssertDiagnostic(t,err,&want)}
		identityRefExec(t,db,`INSERT INTO i DEFAULT VALUES`,1);identityRefRows(t,db,`SELECT * FROM i`,[][]int64{{7}})
		_,err:=db.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(LOGGED UNLOGGED))`);identityRefState(t,err,"42601")
	})
}
