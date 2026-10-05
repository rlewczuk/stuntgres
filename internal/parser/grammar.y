%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 col Column
 cols []Column
 typ TypeName
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
 roleOptions roleOptionState
}
%token <tok> IDENT NUMBER STRING PARAM
%token <tok> '+' '-' '[' ':' '(' '='
%token <tok> CREATE TABLE INSERT INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> SET SHOW SESSION LOCAL TO DEFAULT
%token <tok> IF EXISTS
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION RESET SESSION_USER CURRENT_USER CURRENT_ROLE
/* In SET scope position, SESSION wins over an empty optional scope.
 * Elsewhere SESSION remains a nonreserved ordinary name. */
%nonassoc EMPTY_SESSION_SCOPE
%nonassoc SESSION
/* A following '(' denotes an unsupported function, not a completed name list. */
%nonassoc NAME_LIST
%nonassoc '('
%type <stmt> statement create insert select set show create_role drop_role session_authorization identity_select excluded_role
%type <col> column
%type <cols> columns
%type <typ> type
%type <nullable> nullability
%type <tok> name setting_name role_name role_option_word type_name bare_label col_label generic_word suffix unsupported_query nulls_keyword nulls_position
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
%type <desc> role_kind
%type <roleOptions> role_options
%type <names> identity_names
%type <tok> identity_name
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: create | insert | select | set | show | create_role | drop_role | session_authorization | identity_select | excluded_role
 | generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | DROP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | RESET { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 } ;
set: SET optional_session setting_name setting_assignment setting_values { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Values:$5,Pos:$1.pos} }
 | SET optional_session setting_name setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Default:true,Pos:$1.pos} } ;
optional_session: /* empty */ %prec EMPTY_SESSION_SCOPE | SESSION ;
setting_assignment: TO | '=' ;
setting_values: setting_value { $$=[]string{$1} } | setting_values ',' setting_value { $$=append($1,$3) } ;
setting_value: STRING { $$=$1.text } | name { $$=$1.text }
 | TRUE { $$="true" } | FALSE { $$="false" }
 | NUMBER { var ok bool; $$, ok=settingInteger(sqllex,$1,false); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$, ok=settingInteger(sqllex,$2,false); if !ok { return 1 } }
 | '-' NUMBER { var ok bool; $$, ok=settingInteger(sqllex,$2,true); if !ok { return 1 } } ;
show: SHOW name { $$=&Show{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} } ;
create: CREATE TABLE relation_name '(' columns ')' { $$ = &CreateTable{Name:$3, Columns:$5, Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' columns ')' { $$ = &CreateTable{Name:$6, Columns:$8, Pos:$1.pos, IfNotExists:true} }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
columns: column { $$ = []Column{$1} } | columns ',' column { $$ = append($1,$3) } ;
column: name type nullability { $$ = Column{Name:$1.text, TypeName:$2, Nullable:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = TypeName{Name:"integer",Pos:$1.pos} } | BIGINT { $$ = TypeName{Name:"bigint",Pos:$1.pos} } | TEXT { $$ = TypeName{Name:"text",Pos:$1.pos} } | BOOLEAN { $$ = TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name { var ok bool; $$,ok=columnType(sqllex,$1); if !ok { return 1 } } ;
nullability: /* empty */ { $$ = true } | NULL { $$ = true } | NOT NULL { $$ = false }
 | suffix { sqllex.(*lexer).capability($1,unsupportedSuffixes,"column constraints are unsupported"); return 1 }
 | '[' { sqllex.(*lexer).failAt("0A000","array types are unsupported",$1.pos.Offset); return 1 } ;
insert: INSERT INTO relation_name insert_columns VALUES rows { $$ = &Insert{Name:$3, Targets:$4, Rows:$6, Pos:$1.pos} }
 | INSERT INTO relation_name insert_columns VALUES rows suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name insert_columns DEFAULT { sqllex.(*lexer).failAt("0A000","INSERT defaults are unsupported",$5.pos.Offset); return 1 }
 | INSERT INTO relation_name unsupported_query { sqllex.(*lexer).deferUnsupported($4,"INSERT SELECT is unsupported"); $$=nil }
 | INSERT INTO relation_name '(' insert_targets ')' unsupported_query { sqllex.(*lexer).deferUnsupported($7,"INSERT SELECT is unsupported"); $$=nil } ;
insert_columns: /* empty */ { $$ = nil } | '(' insert_targets ')' { $$ = $2 } ;
insert_targets: name { $$ = []InsertTarget{{Name:$1.text, Pos:$1.pos}} } | insert_targets ',' name { $$ = append($1, InsertTarget{Name:$3.text, Pos:$3.pos}) } ;
/* Failure-only query input recognition, not an executable SELECT expansion.
 * In INSERT, (SELECT) is a query input, not a reserved column name. Continue
 * parsing so that a trailing VALUES (or any other malformed suffix) wins. */
unsupported_query: SELECT query_projection query_from query_where query_order { $$=$1 }
 | '(' unsupported_query ')' { $$=$2 } ;
query_projection: /* empty */ | '*' | query_targets ;
query_targets: query_target | query_targets ',' query_target ;
query_target: name | NUMBER | STRING | PARAM | TRUE | FALSE | NULL | '+' NUMBER | '-' NUMBER ;
query_from: /* empty */ | FROM relation_name ;
query_where: /* empty */ | WHERE name '=' query_value ;
query_value: NUMBER | STRING | PARAM | TRUE | FALSE | NULL | '+' NUMBER | '-' NUMBER ;
query_order: /* empty */ | ORDER BY name direction ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name %prec NAME_LIST { $$ = append($1,$3.text) } ;
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
 | name '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | KW_T '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | AUTHORIZATION '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | value ':' { sqllex.(*lexer).failAt("0A000","casts are unsupported",$2.pos.Offset); return 1 }
 | DEFAULT { sqllex.(*lexer).failAt("0A000","default values are unsupported",$1.pos.Offset); return 1 }
 | CAST '(' { sqllex.(*lexer).failAt("0A000","casts are unsupported",$1.pos.Offset); return 1 } ;
select: SELECT projection FROM relation_name where order { $2.Name=$4; $2.Where=$5; $2.Order=$6; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection FROM relation_name name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$5.pos.Offset); return 1 }
 | SELECT projection FROM relation_name AS name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$6.pos.Offset); return 1 }
 | SELECT projection INTO { sqllex.(*lexer).failAt("0A000","SELECT INTO is unsupported",$3.pos.Offset); return 1 }
 | SELECT projection FROM relation_name where order suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 } ;
projection: '*' { $$ = &Select{Star:true} } | names { $$ = &Select{Columns:$1} }
 | names bare_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$2.pos.Offset); return 1 }
 | names AS col_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$3.pos.Offset); return 1 }
 | names ',' name '(' { sqllex.(*lexer).call($3,$4); return 1 }
 | names ',' KW_T '(' { sqllex.(*lexer).call($3,$4); return 1 }
 | names ',' AUTHORIZATION '(' { sqllex.(*lexer).call($3,$4); return 1 }
 | names ',' CAST '(' { sqllex.(*lexer).failAt("0A000","casts are unsupported",$3.pos.Offset); return 1 }
 | names '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | names '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | names '.' { sqllex.(*lexer).fail("0A000","qualified column references are unsupported"); return 1 }
 | value { sqllex.(*lexer).fail("0A000","literal projections are unsupported"); return 1 } ;
where: /* empty */ { $$ = nil } | WHERE name '=' value { $$ = &Predicate{Column:$2.text, Value:$4, Pos:$3.pos} }
 | WHERE name '>' { sqllex.(*lexer).fail("0A000","comparison operator is unsupported"); return 1 }
 | WHERE name '<' { sqllex.(*lexer).fail("0A000","comparison operator is unsupported"); return 1 }
 | WHERE name '!' { sqllex.(*lexer).fail("0A000","comparison operator is unsupported"); return 1 }
 | WHERE name suffix { if $3.keyword!=nil && ($3.text=="isnull" || $3.text=="notnull") { sqllex.(*lexer).failAt("0A000","expressions are unsupported",$3.pos.Offset) } else { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported") }; return 1 }
 | WHERE name KW_C { sqllex.(*lexer).capability($3,unsupportedSuffixes,"predicate syntax is unsupported"); return 1 } ;
order: /* empty */ { $$ = nil } | ORDER BY name direction { $$ = &Order{Column:$3.text, Desc:$4} }
 | ORDER BY name direction ',' { sqllex.(*lexer).fail("0A000","multiple sort keys are unsupported"); return 1 }
 | ORDER BY name direction nulls_keyword nulls_position { sqllex.(*lexer).failAt("0A000","NULLS ordering is unsupported",$5.pos.Offset); return 1 } ;
nulls_keyword: NULLS_LA ;
nulls_position: KW_U { if $1.keyword==nil || ($1.text!="first" && $1.text!="last") { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
setting_name: IDENT | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | VALUES | INSERT | BY | SET | SHOW | SESSION | IF | EXISTS | DROP | RESET | LOCAL ;
name: setting_name | ROLE ;
type_name: IDENT | KW_U | KW_T | KW_C | VALUES | INSERT | BY | SET | SHOW | SESSION | IF | EXISTS | DROP | RESET | LOCAL | ROLE | AUTHORIZATION ;
generic_word: IDENT | KW_U | KW_C | KW_T | KW_R | IF | EXISTS | LOCAL | AUTHORIZATION | WITH ;
/* Structural SELECT delimiters cannot be reduced to bare labels. */
bare_label: name { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | KW_T { if !$1.keyword.label() { if $1.text=="isnull" || $1.text=="notnull" { sqllex.(*lexer).failAt("0A000","expressions are unsupported",$1.pos.Offset) } else { sqllex.(*lexer).syntax($1) }; return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | SELECT | TABLE | ASC | DESC | NOT | NULL | TRUE | FALSE | DEFAULT | CAST | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | USER ;
col_label: name | KW_T | KW_R | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | USER | GROUP | WITH ;
suffix: KW_T | KW_R | INTO | DEFAULT | GROUP | WITH | USER ;
relation_name: name { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos}}} }
 | name '.' col_label { $$ = ObjectName{Parts:[]Identifier{{Text:$1.text, Pos:$1.pos},{Text:$3.text, Pos:$3.pos}}} }
 | name '.' error { sqllex.(*lexer).fail("42601","incomplete relation name"); return 1 }
 | name '.' col_label '.' col_label { sqllex.(*lexer).fail("0A000","three-part relation names are unsupported"); return 1 } ;
role_kind: ROLE { $$=false } | USER { $$=true } | GROUP { $$=false } ;
optional_with: /* empty */ | WITH ;
role_options: /* empty */ { $$=roleOptionState{} }
 | role_options role_option_word {
   switch fold($2.raw) {
   case "login": $$=$1.add($2,true)
   case "nologin": $$=$1.add($2,false)
   default:
    if unsupportedRoleOptions[fold($2.raw)] { sqllex.(*lexer).failAt("0A000","unsupported role option",$2.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1
   }
 } ;
role_option_word: generic_word | ROLE | USER | GROUP | DROP | RESET | BY ;
role_name: name {
 if $1.text=="public" || $1.text=="none" { sqllex.(*lexer).failAt("42939","role name \""+$1.text+"\" is reserved",$1.pos.Offset); return 1 }; $$=$1
 }
 | CURRENT_USER { sqllex.(*lexer).failAt("42939","CURRENT_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | SESSION_USER { sqllex.(*lexer).failAt("42939","SESSION_USER cannot be used as a role name here",$1.pos.Offset); return 1 }
 | CURRENT_ROLE { sqllex.(*lexer).failAt("42939","CURRENT_ROLE cannot be used as a role name here",$1.pos.Offset); return 1 } ;
create_role: CREATE role_kind role_name optional_with role_options {
 login:=$2
 if $5.count>0 { login=$5.login }
 $$=&CreateRole{Name:Identifier{Text:$3.text,Pos:$3.pos},Login:login,OptionCount:$5.count,SecondOption:$5.second}
 } ;
drop_role: DROP role_kind names { $$=&DropRole{Names:$3} } ;
session_authorization: SET SESSION AUTHORIZATION name { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | SET AUTHORIZATION name { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | RESET SESSION AUTHORIZATION { $$=&SessionAuthorization{Default:true} } ;
/* Recognize adjacent role commands only to reject them before creating an AST.
 * ROLE remains nonreserved in ordinary identifier positions. */
excluded_role: SET optional_session ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | SET LOCAL ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | RESET ROLE { sqllex.(*lexer).failAt("0A000","RESET ROLE is unsupported",$2.pos.Offset); return 1 } ;
excluded_role_assignment: /* empty */ | setting_assignment ;
excluded_role_target: name | STRING | DEFAULT ;
identity_name: SESSION_USER | CURRENT_USER ;
identity_names: identity_name { $$=[]string{$1.text} } | identity_names ',' identity_name { $$=append($1,$3.text) } ;
identity_select: SELECT identity_names { $$=&IdentitySelect{Names:$2} } ;
%%
