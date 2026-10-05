%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 col Column
 cols []Column
 typ Type
 nullable bool
 targets []InsertTarget
 names []string
 rows [][]Value
 row []Value
 value Value
 selectStmt *Select
 pred *Predicate
 order *Order
 desc bool
 objectName ObjectName
 settingValues []string
 settingValue string
}
%token <tok> IDENT NUMBER STRING PARAM
%token <tok> '+' '-'
%token <tok> CREATE TABLE INSERT INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE
%token <tok> INTEGER BIGINT TEXT BOOLEAN UNSUP_TYPE UNSUP_IDENT UNSUP
%token <tok> SET SHOW SESSION TO DEFAULT
/* In SET scope position, SESSION wins over an empty optional scope.
 * Elsewhere SESSION remains a nonreserved ordinary name. */
%nonassoc EMPTY_SESSION_SCOPE
%nonassoc SESSION
/* A following '(' denotes an unsupported function, not a completed name list. */
%nonassoc NAME_LIST
%nonassoc '('
%type <stmt> statement create insert select set show
%type <col> column
%type <cols> columns
%type <typ> type
%type <nullable> nullability
%type <tok> name
%type <targets> insert_columns insert_targets
%type <objectName> relation_name
%type <names> names
%type <rows> rows
%type <row> row values
%type <value> value
%type <selectStmt> projection
%type <pred> where
%type <order> order
%type <desc> direction
%type <settingValues> setting_values
%type <settingValue> setting_value
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } ;
statement: create | insert | select | set | show ;
set: SET optional_session name setting_assignment setting_values { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Values:$5,Pos:$1.pos} }
 | SET optional_session name setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Default:true,Pos:$1.pos} } ;
optional_session: /* empty */ %prec EMPTY_SESSION_SCOPE | SESSION ;
setting_assignment: TO | '=' ;
setting_values: setting_value { $$=[]string{$1} } | setting_values ',' setting_value { $$=append($1,$3) } ;
setting_value: STRING { $$=$1.text } | name { $$=$1.text }
 | TRUE { $$="true" } | FALSE { $$="false" }
 | NUMBER { $$=settingInteger($1.text,false) }
 | '+' NUMBER { $$=settingInteger($2.text,false) }
 | '-' NUMBER { $$=settingInteger($2.text,true) } ;
show: SHOW name { $$=&Show{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} } ;
create: CREATE TABLE relation_name '(' columns ')' { $$ = &CreateTable{Name:$3, Columns:$5, Pos:$1.pos} }
 | CREATE TABLE relation_name NOT { code:="42601"; if len($3.Parts)==1 && $3.Parts[0].Text=="if" { code="0A000" }; sqllex.(*lexer).fail(code,"unsupported CREATE syntax"); return 1 } ;
columns: column { $$ = []Column{$1} } | columns ',' column { $$ = append($1,$3) } ;
column: name type nullability { $$ = Column{Name:$1.text, Type:$2, Nullable:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = Int4 } | BIGINT { $$ = Int8 } | TEXT { $$ = Text } | BOOLEAN { $$ = Bool }
 | UNSUP_TYPE { sqllex.(*lexer).fail("0A000", "unsupported column type"); return 1 } ;
nullability: /* empty */ { $$ = true } | NULL { $$ = true } | NOT NULL { $$ = false } ;
insert: INSERT INTO relation_name insert_columns VALUES rows { $$ = &Insert{Name:$3, Targets:$4, Rows:$6, Pos:$1.pos} } ;
insert_columns: /* empty */ { $$ = nil } | '(' insert_targets ')' { $$ = $2 } ;
insert_targets: name { $$ = []InsertTarget{{Name:$1.text, Pos:$1.pos}} } | insert_targets ',' name { $$ = append($1, InsertTarget{Name:$3.text, Pos:$3.pos}) } ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name { $$ = append($1,$3.text) } ;
rows: row { $$ = [][]Value{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' values ')' { $$ = $2 } ;
values: value { $$ = []Value{$1} } | values ',' value { $$ = append($1,$3) } ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | PARAM { $$ = Value{Kind:ParameterValue, Parameter:sqllex.(*lexer).parameter($1.text), Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"+"); if !ok { return 1 }; $$.Pos=$1.pos }
 | '-' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"-"); if !ok { return 1 }; $$.Pos=$1.pos }
 | value '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | name '(' { sqllex.(*lexer).fail("0A000","functions are unsupported"); return 1 } ;
select: SELECT projection FROM relation_name where order { $2.Name=$4; $2.Where=$5; $2.Order=$6; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection FROM relation_name name { sqllex.(*lexer).fail("0A000","table aliases are unsupported"); return 1 } ;
projection: '*' { $$ = &Select{Star:true} } | names { $$ = &Select{Columns:$1} }
 | names name { sqllex.(*lexer).fail("0A000","projection aliases are unsupported"); return 1 }
 | names '(' { sqllex.(*lexer).fail("0A000","functions are unsupported"); return 1 }
 | names '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | names '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value { sqllex.(*lexer).fail("0A000","literal projections are unsupported"); return 1 } ;
where: /* empty */ { $$ = nil } | WHERE name '=' value { $$ = &Predicate{Column:$2.text, Value:$4} } ;
order: /* empty */ { $$ = nil } | ORDER BY name direction { $$ = &Order{Column:$3.text, Desc:$4} }
 | ORDER BY name direction ',' { sqllex.(*lexer).fail("0A000","multiple sort keys are unsupported"); return 1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
name: IDENT | INTEGER | BIGINT | TEXT | BOOLEAN | VALUES | UNSUP_TYPE | UNSUP_IDENT | SET | SHOW | SESSION ;
relation_name: name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos}}} }
 | name '.' name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos},{Text:$3.text, Pos:$3.pos}}} }
 | name '.' error { sqllex.(*lexer).fail("42601","incomplete relation name"); return 1 }
 | name '.' name '.' name { sqllex.(*lexer).fail("0A000","three-part relation names are unsupported"); return 1 } ;
%%
