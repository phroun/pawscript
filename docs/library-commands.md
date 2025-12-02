# PawScript Library Commands Quick Reference

## core::
| Command | Usage | Description |
|---------|-------|-------------|
| `true` | `true` | Sets success status |
| `false` | `false` | Sets error status |
| `set_result` | `set_result <value>` | Explicitly sets the result value |
| `get_result` | `get_result` | Gets the current result value |
| `get_status` | `get_status` | Gets previous command's status as bool |
| `get_substatus` | `get_substatus` | Gets whether all brace expressions succeeded |
| `ret` | `ret [value]` | Early return from block |
| `if` | `if <value>` | Normalize truthy/falsy to boolean |
| `stack_trace` | `stack_trace` | Get current call stack |
| `bubble` | `bubble <flavor>, <content>` | Create a bubble entry |
| `bubble_orphans` | `bubble_orphans` | Get orphaned bubbles list |
| `include` | `include <path>` | Include and execute another script |

## types::
| Command | Usage | Description |
|---------|-------|-------------|
| `infer` | `infer <value>` | Returns the type of a value |
| `type` | `type <varname>` | Returns type without fetching value |
| `list` | `list <items...> [from: json]` | Creates a list from arguments |
| `len` | `len <list\|string\|channel> [keys: true]` | Returns length |
| `arrlen` | `arrlen <list>` | Count positional items only |
| `maplen` | `maplen <list>` | Count named arguments only |
| `arrtype` | `arrtype <list>` | Get type of positional items |
| `maptype` | `maptype <list>` | Get type of named arguments |
| `arrsolid` | `arrsolid <list>` | Check if all positional items are same type |
| `mapsolid` | `mapsolid <list>` | Check if all named args are same type |
| `arrser` | `arrser <list>` | Check if positional items are serializable |
| `mapser` | `mapser <list>` | Check if named args are serializable |
| `json` | `json <value> [pretty: true] [color: true]` | Serialize to JSON string |
| `string` | `string <value> [pretty: true]` | Convert to string |
| `float` | `float <value>` | Convert to float |
| `number` | `number <value>` | Convert to number (int or float) |
| `bool` | `bool <value>` | Convert to boolean |
| `symbol` | `symbol <value>` | Convert to symbol |
| `block` | `block <value>` | Convert to block |

## strlist::
| Command | Usage | Description |
|---------|-------|-------------|
| `bytes` | `bytes <args...> [single: true]` | Create byte array |
| `slice` | `slice <list\|str>, <start>, <end>` | Extract portion (end exclusive) |
| `slice` | `slice <list>, only: arr\|map` | Extract positional or named portion |
| `append` | `append <list\|str>, <item>` | Append item/suffix |
| `prepend` | `prepend <list\|str>, <item>` | Prepend item/prefix |
| `compact` | `compact <list>` | Remove nil/undefined items |
| `concat` | `concat <list1>, <list2>` | Concatenate lists or strings |
| `split` | `split <string>, <delimiter>` | Split string into list |
| `join` | `join <list>, <separator>` | Join list into string |
| `upper` | `upper <string>` | Convert to uppercase |
| `lower` | `lower <string>` | Convert to lowercase |
| `trim` | `trim <string> [chars]` | Trim whitespace or chars |
| `trim_start` | `trim_start <string> [chars]` | Trim from start |
| `trim_end` | `trim_end <string> [chars]` | Trim from end |
| `contains` | `contains <list\|str>, <item>` | Check if contains item/substring |
| `index` | `index <list\|str>, <item>` | Find index of item/substring |
| `replace` | `replace <str>, <old>, <new> [all: true]` | Replace substring |
| `starts_with` | `starts_with <str>, <prefix>` | Check prefix |
| `ends_with` | `ends_with <str>, <suffix>` | Check suffix |
| `repeat` | `repeat <str\|list>, <count>` | Repeat string/list N times |
| `sort` | `sort <list> [by: <macro>]` | Sort list |
| `match` | `match <string>, <pattern>` | Regex match (returns bool) |
| `regex_find` | `regex_find <string>, <pattern>` | Find all regex matches |
| `regex_replace` | `regex_replace <str>, <pattern>, <repl>` | Regex replace |
| `keys` | `keys <list>` | Get named argument keys |
| `struct_def` | `struct_def <fields...>` | Define a struct type |
| `struct` | `struct <def>, <source>` | Create struct instance |

## cmp::
| Command | Usage | Description |
|---------|-------|-------------|
| `eq` | `eq <a>, <b>, ...` | Deep equality (all equal) |
| `neq` | `neq <a>, <b>, ...` | Deep inequality (any differ) |
| `eqs` | `eqs <a>, <b>, ...` | Shallow equality (by identity) |
| `neqs` | `neqs <a>, <b>, ...` | Shallow inequality |
| `lt` | `lt <a>, <b>, ...` | Strictly ascending order |
| `gt` | `gt <a>, <b>, ...` | Strictly descending order |
| `lte` | `lte <a>, <b>, ...` | Ascending or equal |
| `gte` | `gte <a>, <b>, ...` | Descending or equal |

## basicmath::
| Command | Usage | Description |
|---------|-------|-------------|
| `add` | `add <a>, <b>, ...` | Sum arguments |
| `sub` | `sub <a>, <b>, ...` | Subtract from first |
| `mul` | `mul <a>, <b>, ...` | Multiply arguments |
| `idiv` | `idiv <a>, <b>` | Integer division (floored) |
| `fdiv` | `fdiv <a>, <b>` | Float division |
| `iremainder` | `iremainder <a>, <b>` | Integer remainder |
| `imodulo` | `imodulo <a>, <b>` | Integer modulo |
| `fremainder` | `fremainder <a>, <b>` | Float remainder |
| `fmodulo` | `fmodulo <a>, <b>` | Float modulo |
| `floor` | `floor <value>` | Round down |
| `ceil` | `ceil <value>` | Round up |
| `trunc` | `trunc <value>` | Truncate toward zero |
| `round` | `round <value>` | Round to nearest |
| `abs` | `abs <value>` | Absolute value |
| `min` | `min <a>, <b>, ...` | Minimum value |
| `max` | `max <a>, <b>, ...` | Maximum value |

## math:: (requires IMPORT)
| Command | Usage | Description |
|---------|-------|-------------|
| `sin` | `sin <radians>` | Sine |
| `cos` | `cos <radians>` | Cosine |
| `tan` | `tan <radians>` | Tangent |
| `atan2` | `atan2 <y>, <x>` | Arc tangent of y/x |
| `deg` | `deg <radians>` | Convert to degrees |
| `rad` | `rad <degrees>` | Convert to radians |
| `log` | `log <value> [base: N]` | Logarithm (default base 10) |
| `log10` | `log10 <value>` | Base-10 logarithm |
| `ln` | `ln <value>` | Natural logarithm |
| `pow` | `pow <base>, <exp>` | Exponentiation |

Constants: `#tau`, `#e`, `#root2`, `#root3`, `#root5`, `#phi`, `#ln2`

## bitwise::
| Command | Usage | Description |
|---------|-------|-------------|
| `bitwise_and` | `bitwise_and <a>, <b> [align: left\|right]` | Bitwise AND |
| `bitwise_or` | `bitwise_or <a>, <b> [align: left\|right]` | Bitwise OR |
| `bitwise_xor` | `bitwise_xor <a>, <b> [align: left\|right]` | Bitwise XOR |
| `bitwise_not` | `bitwise_not <value>` | Bitwise NOT |
| `bitwise_shl` | `bitwise_shl <value>, <distance>` | Shift left |
| `bitwise_shr` | `bitwise_shr <value>, <distance>` | Shift right |
| `bitwise_rol` | `bitwise_rol <value>, <dist> [bitlength: N]` | Rotate left |
| `bitwise_ror` | `bitwise_ror <value>, <dist> [bitlength: N]` | Rotate right |

## flow::
| Command | Usage | Description |
|---------|-------|-------------|
| `fizz` | `fizz <condition>, <block> [else: <block>]` | Conditional execution |
| `burst` | `burst <list>, <block>` | Iterate over list items |
| `while` | `while <condition>, <block>` | Loop while condition true |
| `for` | `for <init>, <cond>, <step>, <block>` | C-style for loop |
| `break` | `break` | Exit loop |
| `continue` | `continue` | Next iteration |

## macros::
| Command | Usage | Description |
|---------|-------|-------------|
| `macro` | `macro <name>, <body>` | Define a macro |
| `macro_forward` | `macro_forward <name>, <target>` | Create macro alias |
| `call` | `call <macro>, <args...>` | Call a macro |
| `macro_list` | `macro_list` | List defined macros |
| `macro_delete` | `macro_delete <name>` | Delete a macro |
| `macro_clear` | `macro_clear` | Clear all macros |
| `command_ref` | `command_ref <name>` | Get reference to built-in command |

## io::
| Command | Usage | Description |
|---------|-------|-------------|
| `write` | `write [file\|channel], <args...>` | Output without newline |
| `echo` | `echo [file], <args...>` | Output with newline |
| `print` | `print [file], <args...>` | Alias for echo |
| `read` | `read [file\|channel] [eof: true]` | Read line or all |
| `read_bytes` | `read_bytes <file> [count] [all: true]` | Read binary data |
| `write_bytes` | `write_bytes <file>, <bytes>` | Write binary data |
| `rune` | `rune <codepoint>` | Integer to Unicode char |
| `ord` | `ord <string>` | First char to codepoint |
| `clear` | `clear [mode]` | Clear screen/region |
| `color` | `color <fg> [bg] [bold:] [reset:]` | Set terminal colors |
| `cursor` | `cursor [x] [y] [visible:] [shape:]` | Get/set cursor position |

## os::
| Command | Usage | Description |
|---------|-------|-------------|
| `argc` | `argc [list]` | Get argument count |
| `argv` | `argv [list] [index]` | Get arguments or specific arg |
| `exec` | `exec <command>, <args...>` | Execute external command |

## files::
| Command | Usage | Description |
|---------|-------|-------------|
| `file` | `file <path> [mode: r\|w\|a\|rw] [create:]` | Open file |
| `close` | `close <file>` | Close file |
| `seek` | `seek <file>, <offset> [from: start\|current\|end]` | Seek position |
| `tell` | `tell <file>` | Get current position |
| `flush` | `flush <file>` | Flush buffers |
| `truncate` | `truncate <file>` | Truncate at current position |
| `file_close` | `file_close <file>` | Explicit close |
| `file_exists` | `file_exists <path>` | Check if path exists |
| `file_info` | `file_info <path>` | Get file metadata |
| `list_dir` | `list_dir [path]` | List directory contents |
| `mkdir` | `mkdir <path> [parents: true]` | Create directory |
| `rm` | `rm <path>` | Remove file |
| `rmdir` | `rmdir <path> [recursive: true]` | Remove directory |
| `abs_path` | `abs_path <path>` | Get absolute path |
| `join_path` | `join_path <parts...>` | Join path components |
| `dir_name` | `dir_name <path>` | Get directory portion |
| `base_name` | `base_name <path>` | Get filename portion |
| `file_ext` | `file_ext <path>` | Get file extension |

## time::
| Command | Usage | Description |
|---------|-------|-------------|
| `msleep` | `msleep <milliseconds>` | Sleep (async) |
| `microtime` | `microtime` | Get microseconds since epoch |
| `datetime` | `datetime [tz] [stamp] [src_tz]` | Format/convert datetime |

## channels::
| Command | Usage | Description |
|---------|-------|-------------|
| `channel` | `channel [buffer_size]` | Create channel |
| `channel_subscribe` | `channel_subscribe <channel>` | Subscribe to channel |
| `channel_send` | `channel_send <channel>, <value>` | Send to channel |
| `channel_recv` | `channel_recv <channel>` | Receive from channel |
| `channel_close` | `channel_close <channel>` | Close channel |
| `channel_disconnect` | `channel_disconnect <ch>, <id>` | Disconnect subscriber |
| `channel_opened` | `channel_opened <channel>` | Check if open |

## fibers::
| Command | Usage | Description |
|---------|-------|-------------|
| `fiber` | `fiber <macro>, <args...>` | Spawn fiber |
| `fiber_wait` | `fiber_wait <handle>` | Wait for fiber completion |
| `fiber_count` | `fiber_count` | Get active fiber count |
| `fiber_id` | `fiber_id` | Get current fiber ID |
| `fiber_wait_all` | `fiber_wait_all` | Wait for all fibers |
| `fiber_bubble` | `fiber_bubble up\|<handle>` | Transfer bubbles |

## coroutines::
| Command | Usage | Description |
|---------|-------|-------------|
| `generator` | `generator <macro>, <args...>` | Create generator |
| `resume` | `resume <token>` | Resume generator |
| `yield` | `yield <value>` | Yield from generator |
| `suspend` | `suspend` | Suspend execution |
| `token_valid` | `token_valid <token>` | Check if token valid |
| `each` | `each <list>` | Create list iterator |
| `pair` | `pair <list>` | Create key-value iterator |
| `range` | `range <start>, <end> [step]` | Create range iterator |
| `rng` | `rng [seed]` | Create random generator |
| `random` | `random [min] [max]` | Generate random number |

## debug::
| Command | Usage | Description |
|---------|-------|-------------|
| `log_print` | `log_print <level>, <msg>, [cats...]` | Log message |
| `error_logging` | `error_logging [default:] [floor:]` | Configure error log |
| `debug_logging` | `debug_logging [default:] [floor:]` | Configure debug log |
| `bubble_logging` | `bubble_logging [default:] [floor:]` | Configure bubble capture |
| `mem_stats` | `mem_stats` | Get memory statistics |
| `env_dump` | `env_dump` | Dump module environment |
| `lib_dump` | `lib_dump` | Dump inherited library |
| `bubble_dump` | `bubble_dump` | Dump bubble map |
| `bubble_orphans_dump` | `bubble_orphans_dump` | Dump orphaned bubbles |
