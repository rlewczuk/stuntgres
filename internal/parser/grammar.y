%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 col Column
 elements []TableElement
 element TableElement
 likeOptions []LikeOption
 likeOption LikeOption
 objectNames []ObjectName
 dropMode DropMode
 typ TypeName
 nullable bool
 targets []InsertTarget
 names []string
 rows [][]Expr
 row []Expr
 value Value
 expr Expr
 selectStmt *Select
 order *Order
 desc bool
 objectName ObjectName
 settingValues settingList
 settingValue settingAtom
 roleOptions roleOptionState
 settingName settingName
 tables []*CreateTable
 table *CreateTable
}
%token <tok> IDENT NUMBER STRING PARAM
%token <tok> '+' '-' '[' ':' '(' '=' ','
%token <tok> CREATE TABLE INSERT INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE
%token <tok> INTEGER BIGINT TEXT BOOLEAN KW_U KW_C KW_T KW_R AS CAST NULLS_LA
%token <tok> TIMESTAMP WITHOUT TIME ZONE
%token <tok> SET SHOW RESET ALL SESSION TO DEFAULT LOCAL SETTING_FUNC ON
%token <tok> IF EXISTS
%token <tok> ROLE USER GROUP DROP WITH AUTHORIZATION SESSION_USER CURRENT_USER CURRENT_ROLE
%token <tok> LOCK ACCESS EXCLUSIVE MODE IN
%token <tok> LIKE INCLUDING EXCLUDING SCHEMA ALTER RENAME OWNER CASCADE RESTRICT CURRENT_SCHEMA
%token <tok> COMMENT COLUMN IS
%token <tok> BEGIN START COMMIT END ROLLBACK ABORT WORK TRANSACTION AND NO CHAIN
%token <tok> ISOLATION LEVEL READ ONLY DEFERRABLE SAVEPOINT RELEASE PREPARE PREPARED
%token <tok> COMMITTED UNCOMMITTED REPEATABLE SERIALIZABLE
/* Reduce a scalar/name only after checking failure-only continuations.
 * Arithmetic remains unsupported; WHERE name suffix keeps its positioned
 * predicate diagnostic rather than reducing to a completed bare scalar.
 * '(' selects a function path, not a completed ordinary name/name list. */
%nonassoc NAME_LIST
%nonassoc '+' '-' INTO KW_R COLUMN ALL DEFAULT ON USER GROUP IN AND SAVEPOINT
/* KW_T, LIKE and WITH also start suffixes. Shift an adjacent type tail before
 * completing a single type name: known excluded families use their first type
 * location, while arbitrary tails are syntax errors at the second token. */
%nonassoc TYPE_NAME
%nonassoc '('
%nonassoc KW_T IS LIKE WITH WITHOUT
%type <stmt> statement create drop_table alter_drop_column alter_drop_single insert select set show reset create_role drop_role session_authorization identity_select excluded_role lock_table create_schema alter_schema drop_schema transaction_control
%type <col> column
%type <elements> elements optional_elements
%type <element> element
%type <likeOptions> like_options
%type <likeOption> like_option
%type <tok> like_option_name
%type <objectNames> relation_names
%type <dropMode> drop_mode
%type <typ> type timestamp_type generic_precision type_modifier_head
%type <nullable> nullability
%type <tok> name setting_word role_name role_option_word type_name bare_label col_label generic_word generic_word_without_with suffix unsupported_query nulls_keyword nulls_position
%type <settingName> setting_name set_name local_setting_name
%type <targets> insert_columns insert_targets
%type <objectName> relation_name
%type <objectName> alter_relation
%type <desc> optional_column drop_if_exists
%type <tok> alter_action
%type <names> names
%type <rows> rows
%type <row> row values call_args scalar_targets
%type <value> value
%type <expr> scalar target where
%type <selectStmt> projection
%type <order> order
%type <desc> direction
%type <settingValues> setting_values
%type <settingValue> setting_value
%type <desc> role_kind
%type <roleOptions> role_options
%type <names> identity_names
%type <tok> identity_name
%type <tables> schema_tables
%type <table> schema_table
%type <tok> schema_owner
%type <desc> drop_behavior
%type <stmt> comment
%type <objectName> comment_column_name
%type <value> comment_text
%type <tok> comment_excluded_kind comment_excluded_word
%type <tok> transaction_modes transaction_mode transaction_start commit_word rollback_start
%type <desc> opt_chain
%type <tok> transaction_name
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { if $1 != nil { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } } ;
statement: create | drop_table | alter_drop_column | insert | select | set | show | reset | create_role | drop_role | session_authorization | identity_select | excluded_role | lock_table | create_schema | alter_schema | drop_schema | comment | transaction_control
 | generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | DROP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER generic_word { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER ROLE { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER USER { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 }
 | ALTER GROUP { sqllex.(*lexer).capability($1,unsupportedCommands,"statement is unsupported"); return 1 } ;
comment: COMMENT ON TABLE relation_name IS comment_text { $$=&Comment{Kind:CommentTable,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON COLUMN comment_column_name IS comment_text { $$=&Comment{Kind:CommentColumn,Name:$4,Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON SCHEMA name IS comment_text { $$=&Comment{Kind:CommentSchema,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON ROLE name IS comment_text { $$=&Comment{Kind:CommentRole,Name:ObjectName{Parts:[]Identifier{{Text:$4.text,Pos:$4.pos}}},Text:$6.Text,Null:$6.Kind==NullValue,Pos:$1.pos} }
 | COMMENT ON comment_excluded_kind relation_name IS comment_text { sqllex.(*lexer).deferUnsupported($3,"COMMENT object kind is unsupported"); $$=nil } ;
comment_column_name: name { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}} }
 | name '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos}}} }
 | name '.' col_label '.' col_label { $$=ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos},{Text:$3.text,Pos:$3.pos},{Text:$5.text,Pos:$5.pos}}} } ;
comment_text: STRING { $$=Value{Kind:StringValue,Text:$1.text,Pos:$1.pos} }
 | NULL { $$=Value{Kind:NullValue,Pos:$1.pos} } ;
comment_excluded_word: IDENT | KW_U | KW_T | KW_R ;
comment_excluded_kind: comment_excluded_word { if $1.keyword==nil || !unsupportedCommentKinds[$1.text] { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } ;
transaction_control: transaction_start { $$=&TransactionControl{Kind:KindStart,Pos:$1.pos} }
 | transaction_start transaction_modes { sqllex.(*lexer).deferUnsupported($2,"transaction modes are unsupported"); $$=nil }
 | commit_word opt_transaction opt_chain { $$=&TransactionControl{Kind:KindCommit,Chain:$3,Pos:$1.pos} }
 | rollback_start opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$2,Pos:$1.pos} }
 | ABORT opt_transaction opt_chain { $$=&TransactionControl{Kind:KindRollback,Chain:$3,Pos:$1.pos} }
 | SAVEPOINT name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | RELEASE optional_savepoint name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | rollback_start TO optional_savepoint name { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | PREPARE TRANSACTION STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | COMMIT PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil }
 | ROLLBACK PREPARED STRING { sqllex.(*lexer).deferUnsupported($1,"statement is unsupported"); $$=nil } ;
transaction_start: BEGIN opt_transaction { $$=$1 } | START TRANSACTION { $$=$1 } ;
commit_word: COMMIT | END ;
/* Share the entire ROLLBACK [WORK|TRANSACTION] prefix before choosing an
 * executable end or failure-only TO. Never shift optional words into TO-only
 * syntax while reducing a competing bare ROLLBACK end introducer. */
rollback_start: ROLLBACK opt_transaction { $$=$1 } ;
opt_transaction: /* empty */ | WORK | TRANSACTION ;
opt_chain: /* empty */ { $$=false } | AND CHAIN { $$=true } | AND NO CHAIN { $$=false } ;
/* PostgreSQL shifts the optional SAVEPOINT keyword before parsing its name:
 * RELEASE SAVEPOINT and ROLLBACK TO SAVEPOINT still require an operand.
 * SAVEPOINT stays name-eligible in actual name positions. */
optional_savepoint: /* empty */ %prec NAME_LIST | SAVEPOINT ;
transaction_modes: transaction_mode | transaction_modes ',' transaction_mode { $$=$1 } | transaction_modes transaction_mode { $$=$1 } ;
transaction_mode: ISOLATION LEVEL isolation_mode { $$=$1 }
 | READ ONLY { $$=$1 }
 | READ mode_write { $$=$1 }
 | DEFERRABLE
 | NOT DEFERRABLE { $$=$1 } ;
isolation_mode: READ COMMITTED | READ UNCOMMITTED | REPEATABLE READ | SERIALIZABLE ;
mode_write: KW_U { if $1.text!="write" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_table: LOCK TABLE relation_name IN ACCESS EXCLUSIVE MODE { $$=&LockTable{Name:$3,Pos:$1.pos} }
 | LOCK TABLE relation_name IN ACCESS EXCLUSIVE MODE lock_nowait { sqllex.(*lexer).deferUnsupported($1,"LOCK options are unsupported"); $$=nil }
 | LOCK TABLE relation_name { sqllex.(*lexer).deferUnsupported($1,"LOCK requires explicit ACCESS EXCLUSIVE MODE"); $$=nil }
 | LOCK TABLE relation_name lock_nowait { sqllex.(*lexer).deferUnsupported($1,"LOCK options are unsupported"); $$=nil }
 | LOCK TABLE relation_name IN excluded_lock_mode MODE optional_lock_nowait { sqllex.(*lexer).deferUnsupported($1,"LOCK mode is unsupported"); $$=nil }
 | LOCK TABLE relation_name ',' excluded_lock_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK target lists are unsupported"); $$=nil }
 | LOCK TABLE relation_name '*' lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK descendants are unsupported"); $$=nil }
 | LOCK TABLE lock_only relation_name optional_lock_star lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK ONLY is unsupported"); $$=nil }
 | LOCK excluded_lock_target lock_more_targets lock_options { sqllex.(*lexer).deferUnsupported($1,"LOCK requires TABLE and explicit ACCESS EXCLUSIVE MODE"); $$=nil } ;
/* Failure-only release forms: no option-bearing executable AST. Consume the
 * complete input so malformed suffixes retain their syntax diagnostics. */
excluded_lock_targets: excluded_lock_target | excluded_lock_targets ',' excluded_lock_target ;
excluded_lock_target: relation_name optional_lock_star | lock_only relation_name optional_lock_star ;
optional_lock_star: /* empty */ | '*' ;
lock_more_targets: /* empty */ | ',' excluded_lock_targets ;
lock_options: optional_lock_nowait | IN lock_any_mode MODE optional_lock_nowait ;
optional_lock_nowait: /* empty */ | lock_nowait ;
lock_any_mode: ACCESS EXCLUSIVE | excluded_lock_mode ;
excluded_lock_mode: ACCESS lock_share | lock_row lock_share | lock_row EXCLUSIVE
 | lock_share | lock_share lock_update EXCLUSIVE | lock_share lock_row EXCLUSIVE | EXCLUSIVE ;
lock_share: KW_U { if $1.text!="share" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_row: KW_C { if $1.text!="row" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_update: KW_U { if $1.text!="update" { sqllex.(*lexer).syntax($1); return 1 } } ;
lock_only: ONLY ;
lock_nowait: KW_U { if $1.text!="nowait" { sqllex.(*lexer).syntax($1); return 1 } } ;
set: SET set_name setting_assignment setting_values { $$=parsedSet($2.identifier(),$4,$1.pos) }
 | SET set_name setting_assignment DEFAULT { $$=&Set{Name:$2.identifier(),Default:true,Pos:$1.pos} }
 | SET SESSION set_name setting_assignment setting_values { $$=parsedSet($3.identifier(),$5,$1.pos) }
 | SET SESSION set_name setting_assignment DEFAULT { $$=&Set{Name:$3.identifier(),Default:true,Pos:$1.pos} }
 /* LOCAL as a parameter name is distinct from transaction-local scope. */
 | SET LOCAL setting_assignment setting_values { $$=parsedSet(Identifier{Text:$2.text,Pos:$2.pos},$4,$1.pos) }
 | SET LOCAL setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$2.text,Pos:$2.pos},Default:true,Pos:$1.pos} }
 | SET SESSION LOCAL setting_assignment setting_values { $$=parsedSet(Identifier{Text:$3.text,Pos:$3.pos},$5,$1.pos) }
 | SET SESSION LOCAL setting_assignment DEFAULT { $$=&Set{Name:Identifier{Text:$3.text,Pos:$3.pos},Default:true,Pos:$1.pos} }
 | SET LOCAL local_setting_name setting_assignment setting_values { s:=parsedSet($3.identifier(),$5,$1.pos);s.Local=true;$$=s }
 | SET LOCAL local_setting_name setting_assignment DEFAULT { $$=&Set{Name:$3.identifier(),Default:true,Local:true,Pos:$1.pos} }
 | SET SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$2.pos},settingList{values:[]string{$3.text},stringValues:[]bool{true}},$1.pos) }
 | SET SESSION SCHEMA STRING { $$=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos) }
 | SET LOCAL SCHEMA STRING { s:=parsedSet(Identifier{Text:"search_path",Pos:$3.pos},settingList{values:[]string{$4.text},stringValues:[]bool{true}},$1.pos);s.Local=true;$$=s } ;
setting_assignment: TO | '=' ;
setting_values: setting_value { $$=settingList{values:[]string{$1.text},stringValues:[]bool{$1.isString}} } | setting_values ',' setting_value { $$=$1;$$.values=append($$.values,$3.text);$$.stringValues=append($$.stringValues,$3.isString) } ;
setting_value: STRING { $$=settingAtom{text:$1.text,isString:true} } | name { $$=settingAtom{text:$1.text,isString:true} }
 | TRUE { $$=settingAtom{text:"true",isString:true} } | FALSE { $$=settingAtom{text:"false",isString:true} }
 | ON { $$=settingAtom{text:$1.text,isString:true} }
 | NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$1,false); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,false); if !ok { return 1 } }
 | '-' NUMBER { var ok bool; $$.text, ok=settingInteger(sqllex,$2,true); if !ok { return 1 } } ;
show: SHOW setting_name { $$=&Show{Name:$2.identifier(),Pos:$1.pos} }
 | SHOW ALL { $$=&Show{All:true,Pos:$1.pos} } ;
reset: RESET set_name { $$=&Reset{Name:$2.identifier(),Pos:$1.pos} }
 | RESET LOCAL { $$=&Reset{Name:Identifier{Text:$2.text,Pos:$2.pos},Pos:$1.pos} }
 | RESET ALL { $$=&Reset{All:true,Pos:$1.pos} } ;
setting_name: name { $$=settingName{first:$1} }
 | setting_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
set_name: setting_word { $$=settingName{first:$1} }
 | LOCAL '.' name { $$=settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | ROLE '.' name { $$=settingName{first:$1,parts:[]string{$1.text,$3.text}} }
 | set_name '.' name { $$=$1; if $$.parts==nil { $$.parts=[]string{$$.first.text,$3.text} } else { $$.parts=append($$.parts,$3.text) } } ;
/* An undotted ROLE after SET LOCAL belongs only to the excluded role command,
 * never to the setting-name reduction. Dotted ROLE and ordinary LOCAL remain
 * parameter names, and ROLE stays name-eligible everywhere else. */
local_setting_name: set_name { $$=$1 }
 | LOCAL { $$=settingName{first:$1} } ;
create: CREATE TABLE relation_name '(' optional_elements ')' { $$ = &CreateTable{Name:$3, Elements:$5, Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' { $$ = &CreateTable{Name:$6, Elements:$8, Pos:$1.pos, IfNotExists:true} }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
optional_elements: /* empty */ { $$=nil } | elements { $$=$1 } ;
elements: element { $$=[]TableElement{$1} } | elements ',' element { $$=append($1,$3) } ;
element: column { c:=$1; $$=TableElement{Column:&c,Pos:c.Pos} }
 | LIKE relation_name like_options { $$=TableElement{Like:&TableLike{Name:$2,Options:$3},Pos:$1.pos} } ;
like_options: /* empty */ { $$=nil } | like_options like_option { $$=append($1,$2) } ;
like_option: INCLUDING like_option_name { $$=LikeOption{Name:$2.text,Including:true,Pos:$1.pos} }
 | EXCLUDING like_option_name { $$=LikeOption{Name:$2.text,Pos:$1.pos} } ;
like_option_name: name { if $1.keyword==nil || !validLikeOption($1.text) { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 } | ALL ;
drop_table: DROP TABLE relation_names drop_mode { $$=&DropTable{Names:$3,Mode:$4,Pos:$1.pos} }
 | DROP TABLE IF EXISTS relation_names drop_mode { $$=&DropTable{Names:$5,IfExists:true,Mode:$6,Pos:$1.pos} } ;
relation_names: relation_name { $$=[]ObjectName{$1} } | relation_names ',' relation_name { $$=append($1,$3) } ;
drop_mode: /* empty */ { $$=DropRestrict } | RESTRICT { $$=DropRestrict } | CASCADE { $$=DropCascade } ;
alter_drop_column: alter_drop_single { $$=$1 }
 | alter_drop_single ',' { sqllex.(*lexer).failAt("0A000","ALTER TABLE action lists are unsupported",$2.pos.Offset); return 1 }
 | ALTER TABLE alter_relation alter_action { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$4.pos.Offset); return 1 }
 | ALTER TABLE lock_only relation_name alter_action { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$5.pos.Offset); return 1 }
 | ALTER TABLE lock_only '(' relation_name ')' alter_action { sqllex.(*lexer).failAt("0A000","ALTER TABLE action is unsupported",$7.pos.Offset); return 1 } ;
alter_drop_single: ALTER TABLE alter_relation DROP optional_column drop_if_exists name drop_mode { $$=&AlterTableDropColumn{Name:$3,Column:Identifier{Text:$7.text,Pos:$7.pos},IfExists:$6,Mode:$8,Pos:$1.pos} }
 | ALTER TABLE lock_only relation_name DROP optional_column drop_if_exists name drop_mode { $$=&AlterTableDropColumn{Name:$4,Column:Identifier{Text:$8.text,Pos:$8.pos},IfExists:$7,Mode:$9,Only:true,Pos:$1.pos} }
 | ALTER TABLE lock_only '(' relation_name ')' DROP optional_column drop_if_exists name drop_mode { $$=&AlterTableDropColumn{Name:$5,Column:Identifier{Text:$10.text,Pos:$10.pos},IfExists:$9,Mode:$11,Only:true,Pos:$1.pos} } ;
alter_relation: relation_name { $$=$1 } | relation_name '*' { $$=$1 } ;
optional_column: /* empty */ { $$=false } | COLUMN { $$=true } ;
drop_if_exists: /* empty */ { $$=false } | IF EXISTS { $$=true } ;
alter_action: generic_word | ALTER | RENAME | OWNER | SET ;
column: name type nullability { $$ = Column{Name:$1.text, TypeName:$2, Nullable:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = TypeName{Name:"integer",Pos:$1.pos} } | BIGINT { $$ = TypeName{Name:"bigint",Pos:$1.pos} } | TEXT { $$ = TypeName{Name:"text",Pos:$1.pos} } | BOOLEAN { $$ = TypeName{Name:"boolean",Pos:$1.pos} }
 | type_name %prec TYPE_NAME { $$=columnType($1) }
 | timestamp_type { $$=$1 }
 | type_modifier_head generic_precision ')' { if sqllex.(*lexer).floatPrecision { $$=$2;$$.Pos=$1.Pos;sqllex.(*lexer).floatPrecision=false } else { $$=$1;$$.Precision=$2.Precision;$$.PrecisionPos=$2.PrecisionPos } }
 | type_name type_name { if $1.keyword!=nil && $2.keyword!=nil && $1.text=="double" && $2.text=="precision" { $$=TypeName{Name:"double precision",Pos:$1.pos} } else { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 } }
 | type_name WITH { if excludedTypeTail($1,$2) { sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset) } else { sqllex.(*lexer).syntax($2) }; return 1 }
 | type_name '.' { sqllex.(*lexer).failAt("0A000","qualified types are unsupported",$1.pos.Offset); return 1 } ;
/* FLOAT and quoted timestamp share the generic modifier opener, but retain
 * their distinct unsigned and signed precision contracts. */
type_modifier_head: type_name '(' {var ok bool;$$,ok=typeModifierHead(sqllex,$1,$2);if !ok{return 1}} ;
timestamp_type: TIMESTAMP %prec TYPE_NAME { $$=TypeName{Name:"timestamp",Pos:$1.pos} }
 | TIMESTAMP '(' NUMBER ')' %prec TYPE_NAME {var ok bool;$$,ok=timestampPrecision(sqllex,$3,false);if !ok{return 1};$$.Name="timestamp";$$.Pos=$1.pos}
 | TIMESTAMP WITHOUT TIME ZONE {$$=TypeName{Name:"timestamp",Pos:$1.pos}}
 | TIMESTAMP '(' NUMBER ')' WITHOUT TIME ZONE {var ok bool;$$,ok=timestampPrecision(sqllex,$3,false);if !ok{return 1};$$.Name="timestamp";$$.Pos=$1.pos}
 | TIMESTAMP WITH {sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1}
 | TIMESTAMP '(' NUMBER ')' WITH {if _,ok:=timestampPrecision(sqllex,$3,false);!ok{return 1};sqllex.(*lexer).failAt("0A000","unsupported column type",$1.pos.Offset);return 1} ;
generic_precision: NUMBER {var ok bool;if sqllex.(*lexer).floatPrecision {$$,ok=floatType(sqllex,lexeme{text:"float"},$1)} else {$$,ok=timestampPrecision(sqllex,$1,false)};if !ok{return 1}}
 | '-' {if sqllex.(*lexer).floatPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=timestampPrecision(sqllex,$3,true);if !ok{return 1};$$.PrecisionPos=$1.pos}
 | '+' {if sqllex.(*lexer).floatPrecision {sqllex.(*lexer).syntax($1);return 1}} NUMBER {var ok bool;$$,ok=timestampPrecision(sqllex,$3,false);if !ok{return 1};$$.PrecisionPos=$1.pos} ;
nullability: /* empty */ { $$ = true } | NULL { $$ = true } | NOT NULL { $$ = false }
 | suffix { sqllex.(*lexer).capability($1,unsupportedSuffixes,"column constraints are unsupported"); return 1 }
 | '[' { sqllex.(*lexer).failAt("0A000","array types are unsupported",$1.pos.Offset); return 1 }
 | '(' { sqllex.(*lexer).failAt("0A000","type modifiers are unsupported",$1.pos.Offset); return 1 } ;
insert: INSERT INTO relation_name insert_columns VALUES rows { $$ = &Insert{Name:$3, Targets:$4, Rows:$6, Pos:$1.pos} }
 | INSERT INTO relation_name insert_columns VALUES rows suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"INSERT suffix is unsupported"); return 1 }
 | INSERT INTO relation_name insert_columns DEFAULT VALUES { if !sqllex.(*lexer).options.RestoreDefaultValues { sqllex.(*lexer).failAt("0A000","INSERT defaults are unsupported",$5.pos.Offset); return 1 }; $$=&Insert{Name:$3,Targets:$4,Rows:[][]Expr{nil},Pos:$1.pos,RestoreDefaultValues:true} }
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
rows: row { $$ = [][]Expr{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' values ')' { $$ = $2 } ;
values: scalar { $$ = []Expr{$1} } | values ',' scalar { $$ = append($1,$3) } ;
scalar: value %prec NAME_LIST { $$=LiteralExpr{Value:$1} }
 | PARAM { $$=ParameterExpr{Number:sqllex.(*lexer).parameter($1.text),Pos:$1.pos} }
 | timestamp_type STRING { $$=TypedLiteralExpr{TypeName:$1,Text:$2.text,Pos:$2.pos} }
 | scalar ':' {sqllex.(*lexer).failAt("0A000","casts are unsupported",$2.pos.Offset);return 1} ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"+"); if !ok { return 1 }; $$.Pos=$1.pos }
 | '-' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"-"); if !ok { return 1 }; $$.Pos=$1.pos }
 | value '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | name '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | KW_T '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | IS '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | LIKE '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | AUTHORIZATION '(' { sqllex.(*lexer).call($1,$2); return 1 }
 | DEFAULT { sqllex.(*lexer).failAt("0A000","default values are unsupported",$1.pos.Offset); return 1 }
 | CAST '(' { sqllex.(*lexer).failAt("0A000","casts are unsupported",$1.pos.Offset); return 1 } ;
select: SELECT projection FROM relation_name where order { $2.Name=$4; $2.Where=$5; $2.Order=$6; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection { sqllex.(*lexer).deferUnsupported($1,"SELECT without FROM is unsupported"); $$=nil }
 | SELECT projection FROM relation_name name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$5.pos.Offset); return 1 }
 | SELECT projection FROM relation_name AS name { sqllex.(*lexer).failAt("0A000","table aliases are unsupported",$6.pos.Offset); return 1 }
 | SELECT projection INTO { sqllex.(*lexer).failAt("0A000","SELECT INTO is unsupported",$3.pos.Offset); return 1 }
 | SELECT projection FROM relation_name where order suffix { sqllex.(*lexer).capability($7,unsupportedSuffixes,"SELECT suffix is unsupported"); return 1 }
 | SELECT SETTING_FUNC '(' call_args ')' { var ok bool; $$,ok=settingCall(sqllex,$2,$4); if !ok { return 1 } } ;
call_args: /* empty */ { $$=nil } | values { $$=$1 } ;
target: name %prec NAME_LIST { $$=ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$1.text,Pos:$1.pos}}},Pos:$1.pos} }
 | scalar { $$=$1 } ;
scalar_targets: target { $$=[]Expr{$1} } | scalar_targets ',' target { $$=append($1,$3) } ;
projection: '*' { $$ = &Select{Star:true} } | scalar_targets { $$ = &Select{Targets:$1} }
 | scalar_targets bare_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$2.pos.Offset); return 1 }
 | scalar_targets AS col_label { sqllex.(*lexer).failAt("0A000","projection aliases are unsupported",$3.pos.Offset); return 1 }
 | scalar_targets '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | scalar_targets '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | scalar_targets '.' { sqllex.(*lexer).fail("0A000","qualified column references are unsupported"); return 1 } ;
where: /* empty */ { $$ = nil }
 | WHERE target { $$=$2 }
 | WHERE name '=' scalar { $$ = OperatorExpr{Name:ObjectName{Parts:[]Identifier{{Text:"=",Pos:$3.pos}}},Args:[]Expr{ColumnExpr{Name:ObjectName{Parts:[]Identifier{{Text:$2.text,Pos:$2.pos}}},Pos:$2.pos},$4},Pos:$3.pos} }
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
/* Keep ROLE/LOCAL command disambiguation while preserving ordinary names. */
setting_word: IDENT | KW_U | KW_C | INTEGER | BIGINT | TEXT | BOOLEAN | TIMESTAMP %prec NAME_LIST | WITHOUT | TIME | ZONE | VALUES | INSERT | BY | SET | SHOW | RESET | SESSION | SETTING_FUNC %prec NAME_LIST | IF | EXISTS | DROP | LOCK | ACCESS | EXCLUSIVE | MODE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | COMMENT | transaction_name ;
name: setting_word | ROLE | LOCAL ;
transaction_name: BEGIN | START | COMMIT | ROLLBACK | ABORT | WORK | TRANSACTION | NO | CHAIN | ISOLATION | LEVEL | READ | SAVEPOINT | RELEASE | PREPARE | PREPARED | COMMITTED | UNCOMMITTED | REPEATABLE | SERIALIZABLE ;
type_name: IDENT | KW_U | KW_T | KW_C | WITHOUT | TIME | ZONE | VALUES | INSERT | BY | SET | SHOW | RESET | SESSION | LOCAL | SETTING_FUNC | IF | EXISTS | DROP | ROLE | AUTHORIZATION | LOCK | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | SCHEMA | ALTER | RENAME | OWNER | CASCADE | RESTRICT | CURRENT_SCHEMA | COMMENT | IS | transaction_name ;
generic_word: generic_word_without_with | WITH ;
generic_word_without_with: IDENT | KW_U | KW_C | KW_T | KW_R | TIMESTAMP | WITHOUT | TIME | ZONE | IF | EXISTS | ALL | LOCAL | ON | AUTHORIZATION | IN | ACCESS | EXCLUSIVE | MODE | LIKE | INCLUDING | EXCLUDING | CASCADE | RESTRICT | COLUMN | IS | AND | ONLY | DEFERRABLE ;
/* Structural SELECT delimiters cannot be reduced to bare labels. */
bare_label: name { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | LIKE { $$=$1 }
 | IS { $$=$1 }
 | COLUMN { $$=$1 }
 | KW_T { if !$1.keyword.label() { if $1.text=="isnull" || $1.text=="notnull" { sqllex.(*lexer).failAt("0A000","expressions are unsupported",$1.pos.Offset) } else { sqllex.(*lexer).syntax($1) }; return 1 }; $$=$1 }
 | KW_R { if !$1.keyword.label() { sqllex.(*lexer).syntax($1); return 1 }; $$=$1 }
 | IN { $$=$1 }
 | ON { sqllex.(*lexer).syntax($1); return 1 }
 | SELECT | TABLE | ASC | DESC | NOT | NULL | TRUE | FALSE | DEFAULT | CAST | ALL | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | AND | END | ONLY | DEFERRABLE ;
col_label: name | LIKE | KW_T | KW_R | CREATE | TABLE | INTO | SELECT | FROM | WHERE | ORDER | ASC | DESC | NOT | NULL | TRUE | FALSE | TO | DEFAULT | AS | CAST | ALL | ON | AUTHORIZATION | SESSION_USER | CURRENT_USER | CURRENT_ROLE | CURRENT_SCHEMA | USER | GROUP | WITH | IN | COLUMN | IS | AND | END | ONLY | DEFERRABLE ;
suffix: LIKE | KW_T | KW_R | INTO | DEFAULT | ALL | ON | GROUP | WITH | USER | IN | IS | AND ;
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
/* WITH is a reserved introducer, never a role option after optional_with. */
role_option_word: generic_word_without_with | ROLE | USER | GROUP | DROP | RESET | BY | transaction_name | END ;
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
/* IF remains a name unless followed by EXISTS; that lookahead cannot continue
 * an ordinary comma-list. Defer exclusion so malformed suffixes stay 42601. */
drop_role: DROP role_kind names { $$=&DropRole{Names:$3} }
 | DROP role_kind IF EXISTS names { sqllex.(*lexer).deferUnsupported($3,"DROP ROLE IF EXISTS is unsupported"); $$=nil } ;
session_authorization: SET SESSION AUTHORIZATION name { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$4.text,OwnerDirective:true} }
 | SET SESSION AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | SET AUTHORIZATION name { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION STRING { $$=&SessionAuthorization{Name:$3.text} }
 | SET AUTHORIZATION DEFAULT { $$=&SessionAuthorization{Default:true} }
 | RESET SESSION AUTHORIZATION { $$=&SessionAuthorization{Default:true} } ;
/* Recognize adjacent role commands only to reject them before creating an AST.
 * ROLE remains nonreserved in ordinary identifier positions. */
excluded_role: SET ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$2.pos.Offset); return 1 }
 | SET SESSION ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | SET LOCAL ROLE excluded_role_assignment excluded_role_target { sqllex.(*lexer).failAt("0A000","SET ROLE is unsupported",$3.pos.Offset); return 1 }
 | RESET ROLE { sqllex.(*lexer).failAt("0A000","RESET ROLE is unsupported",$2.pos.Offset); return 1 } ;
excluded_role_assignment: /* empty */ | setting_assignment ;
excluded_role_target: name | STRING | DEFAULT ;
identity_name: SESSION_USER | CURRENT_USER | CURRENT_SCHEMA | CURRENT_SCHEMA '(' ')' { $$=$1 } ;
identity_names: identity_name { $$=[]string{$1.text} } | identity_names ',' identity_name { $$=append($1,$3.text) } ;
identity_select: SELECT identity_names { $$=&IdentitySelect{Names:$2} } ;
schema_owner: name | CURRENT_USER | CURRENT_ROLE | SESSION_USER ;
schema_table: CREATE TABLE relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$3,Elements:$5,Pos:$1.pos} }
 | CREATE TABLE IF NOT EXISTS relation_name '(' optional_elements ')' { $$=&CreateTable{Name:$6,Elements:$8,Pos:$1.pos,IfNotExists:true} }
 | CREATE generic_word { sqllex.(*lexer).capability($2,unsupportedCreateObjects,"CREATE syntax is unsupported"); return 1 } ;
schema_tables: /* empty */ { $$=nil } | schema_tables schema_table { $$=append($1,$2) }
 | schema_tables KW_R { sqllex.(*lexer).capability($2,unsupportedCommands,"schema element is unsupported"); return 1 } ;
create_schema: CREATE SCHEMA name schema_tables { $$=&CreateSchema{Name:$3.text,Tables:$4} }
 | CREATE SCHEMA name AUTHORIZATION schema_owner schema_tables { $$=&CreateSchema{Name:$3.text,Authorization:$5.text,AuthorizationIdentity:$5.keyword!=nil&&$5.keyword.category==Reserved,Tables:$6} }
 | CREATE SCHEMA AUTHORIZATION schema_owner schema_tables { $$=&CreateSchema{Authorization:$4.text,AuthorizationIdentity:$4.keyword!=nil&&$4.keyword.category==Reserved,Tables:$5} }
 | CREATE SCHEMA IF NOT EXISTS name { $$=&CreateSchema{Name:$6.text,IfNotExists:true} }
 | CREATE SCHEMA IF NOT EXISTS name AUTHORIZATION schema_owner { $$=&CreateSchema{Name:$6.text,Authorization:$8.text,AuthorizationIdentity:$8.keyword!=nil&&$8.keyword.category==Reserved,IfNotExists:true} }
 | CREATE SCHEMA IF NOT EXISTS AUTHORIZATION schema_owner { $$=&CreateSchema{Authorization:$7.text,AuthorizationIdentity:$7.keyword!=nil&&$7.keyword.category==Reserved,IfNotExists:true} } ;
alter_schema: ALTER SCHEMA name RENAME TO name { $$=&AlterSchema{Name:$3.text,NewName:$6.text} }
 | ALTER SCHEMA name OWNER TO schema_owner { $$=&AlterSchema{Name:$3.text,Owner:$6.text,OwnerIdentity:$6.keyword!=nil&&$6.keyword.category==Reserved} } ;
drop_behavior: /* empty */ { $$=false } | RESTRICT { $$=false } | CASCADE { $$=true } ;
drop_schema: DROP SCHEMA names drop_behavior { $$=&DropSchema{Names:$3,Cascade:$4} }
 | DROP SCHEMA IF EXISTS names drop_behavior { $$=&DropSchema{Names:$5,IfExists:true,Cascade:$6} } ;
%%
