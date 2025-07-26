```# PawScript

PawScript: A command language with token-based suspension for text editors and command-driven applications.

## Features

- **Complex Command Syntax**: Support for sequences (`;`), conditionals (`&`), and alternatives (`|`)
- **Token-Based Suspension**: Pause and resume command execution for long-running operations
- **Macro System**: Define and execute reusable command sequences
- **Syntactic Sugar**: Automatic transformation of convenient syntax patterns
- **Type Safety**: Full TypeScript support with comprehensive type definitions
- **Host Agnostic**: Clean interface for integration with any application
- **Command Line Tool**: Execute PawScript files directly from the command line

## Installation

```bash
npm install pawscript
```

## Command Line Usage

PawScript includes a `paw` command-line tool for executing scripts:

```bash
# Execute a script file
paw hello.paw

# Execute with arguments
paw script.paw -- arg1 arg2 arg3

# Execute from stdin
echo "echo 'Hello World'" | paw

# Execute redirected input with arguments
paw -- arg1 arg2 < script.paw

# Auto-adds .paw extension
paw hello  # Executes hello.paw
```

### Standard Library Commands

The CLI provides these built-in commands:

- **`argc`** - Returns the number of script arguments
- **`argv [index]`** - Returns all arguments or a specific argument by index
- **`echo/write/print <text>`** - Output text to stdout
- **`read`** - Read a line from stdin (interactive or redirected)
- **`true`** - Sets success state (exit code 0)
- **`false`** - Sets error state (exit code 1)

### Example Scripts

**hello.paw:**
```bash
echo "Hello from PawScript!";
echo "You provided {argc} arguments";
```

**interactive.paw:**
```bash
echo "What's your name?";
read;
echo "Hello, {get_result}!";
```

## Library Usage

## Quick Start

```typescript
import { PawScript } from 'pawscript';

// Create PawScript interpreter
const pawscript = new PawScript({
  debug: true,
  allowMacros: true
});

// Set up host interface
pawscript.setHost({
  getCurrentContext: () => ({ cursor: { x: 0, y: 0 } }),
  updateStatus: (msg) => console.log(msg),
  requestInput: (prompt) => Promise.resolve('user input'),
  render: () => console.log('render called')
});

// Register commands
pawscript.registerCommands({
  'hello': (ctx) => {
    console.log('Hello from PawScript!');
    return true;
  },
  'echo': (ctx) => {
    console.log('Echo:', ctx.args[0]);
    return true;
  }
});

// Execute commands
pawscript.execute('hello');                    // Simple command
pawscript.execute("echo 'Hello World'");      // Command with arguments
pawscript.execute('hello; echo "chained"');   // Command sequence
```

## Command Syntax

### Basic Commands
```typescript
pawscript.execute('save_file');
pawscript.execute("open_file '/path/to/file'");
pawscript.execute('move_cursor 10, 5');
```

### Command Sequences
```typescript
// Sequence: Execute all commands
pawscript.execute('save_file; close_buffer; open_file "new.txt"');

// Conditional: Stop on failure
pawscript.execute('save_file & close_buffer & exit');

// Alternative: Stop on success
pawscript.execute('auto_save | prompt_save | cancel');
```

### Syntactic Sugar
```typescript
// Automatic quote insertion for identifiers
pawscript.execute('macro hello(save_file; exit)');
// Becomes: pawscript.execute("macro 'hello', (save_file; exit)");
```

## Built-in Commands

When `allowMacros` is enabled (default), PawScript automatically registers these built-in commands:

### Macro Commands
- `macro <name>, <commands>` - Define a new macro
- `call <name>` - Execute a macro by name
- `macro_list` - List all defined macros
- `macro_delete <name>` - Delete a specific macro
- `macro_clear` - Clear all macros

### Macros
```typescript
// Define macro using built-in command with syntactic sugar
pawscript.execute("macro quick_save(save_file; update_status 'Saved')");

// Define macro with arguments
pawscript.execute("macro greet(echo 'Hello $1!')");

// Execute macro using built-in command
pawscript.execute('call quick_save');

// Execute macro with arguments
pawscript.execute("call greet 'World'");

// Or execute macro directly (if it's defined)
pawscript.execute('quick_save');
pawscript.execute("greet 'Alice'");

// Macros in sequences
pawscript.execute('quick_save; close_buffer');

// Programmatic macro management (bypasses syntactic sugar)
pawscript.defineMacro('quick_save', 'save_file; update_status "Saved"');
pawscript.executeMacro('quick_save');

### Usage Examples
```typescript
// Define a macro (using syntactic sugar)
pawscript.execute("macro quick_save(save_file; update_status 'Saved')");

// Execute a macro
pawscript.execute('call quick_save');

// List all macros
pawscript.execute('macro_list');

// Delete a macro
pawscript.execute('macro_delete quick_save');

// Clear all macros
pawscript.execute('macro_clear');
```

### Disabling Built-in Commands
```typescript
const pawscript = new PawScript({
  allowMacros: false  // Disables macro commands
});

// Or dynamically
pawscript.configure({ allowMacros: false });
```

## Token-Based Suspension (The "Paws" Feature)

For long-running operations, commands can return tokens that pause execution. This is the correct pattern:

```typescript
pawscript.registerCommand('async_operation', (ctx) => {
  // Request a token to pause execution
  const token = ctx.requestToken((tokenId) => {
    console.log('Operation was interrupted:', tokenId);
  });
  
  // Start async operation using setImmediate
  setImmediate(() => {
    // Simulate async work
    setTimeout(() => {
      console.log('Async operation completed');
      ctx.resumeToken(token, true); // Resume with success
    }, 5000);
  });
  
  return token; // Return token immediately to pause sequence
});

// This will pause at async_operation and resume when it completes
pawscript.execute('async_operation; echo "This runs after async completes"');
```

### Key Points About Tokens:

1. **Immediate Return**: Commands must return the token immediately, not wait for async completion
2. **Use setImmediate**: Start async work with `setImmediate()` to avoid blocking
3. **Resume Later**: Call `ctx.resumeToken()` when the async operation completes
4. **Cleanup Support**: Provide cleanup callbacks for interruption handling

## Result Management

PawScript commands can set **formal results** that flow through command sequences:

```typescript
pawscript.registerCommand('calculate', (ctx) => {
  const result = Number(ctx.args[0]) + Number(ctx.args[1]);
  ctx.setResult(result);  // Set formal result
  return true;            // Indicate success
});

// Result flows through sequences
pawscript.execute('calculate 5, 3; print_result');  // Prints 8
```

### Brace Expressions

Use `{...}` for command evaluation and `${...}` for prefixed evaluation:

```typescript
// Execute command and substitute result
pawscript.execute('echo {calculate 10, 5}');  // Outputs: 15

// Execute command and prefix result with $
pawscript.execute('echo ${get_arg_number}');  // If returns "2", outputs: $2
```

## Host Interface

PawScript integrates with your application through a host interface:

```typescript
interface IPawScriptHost {
  getCurrentContext(): any;
  updateStatus(message: string): void;
  requestInput(prompt: string, defaultValue?: string): Promise<string>;
  render(): void;
  // Optional methods for advanced features
  createWindow?(options: any): string;
  removeWindow?(id: string): void;
  saveState?(): any;
  restoreState?(snapshot: any): void;
  emit?(event: string, ...args: any[]): void;
  on?(event: string, handler: Function): void;
}
```

## Configuration

```typescript
const pawscript = new PawScript({
  debug: false,                    // Enable debug logging
  defaultTokenTimeout: 300000,     // Token timeout in ms (5 minutes)
  enableSyntacticSugar: true,      // Enable syntax transformations
  allowMacros: true,               // Enable macro system
  commandSeparators: {
    sequence: ';',                 // Command sequence separator
    conditional: '&',              // Conditional separator
    alternative: '|'               // Alternative separator
  }
});
```

## Integration Example

Here's how to integrate PawScript with an existing application:

```typescript
// Your existing application
class MyEditor {
  constructor() {
    this.pawscript = new PawScript({ debug: true });
    this.setupPawScript();
  }
  
  setupPawScript() {
    // Set up host interface
    this.pawscript.setHost({
      getCurrentContext: () => ({
        cursor: this.getCursorPosition(),
        selection: this.getSelection(),
        filename: this.getCurrentFilename()
      }),
      updateStatus: (msg) => this.statusBar.show(msg),
      requestInput: (prompt, def) => this.showPrompt(prompt, def),
      render: () => this.redraw()
    });
    
    // Register application-specific commands
    this.pawscript.registerCommands({
      'save_file': (ctx) => this.saveCurrentFile(),
      'open_file': (ctx) => this.openFile(ctx.args[0]),
      'move_cursor': (ctx) => this.moveCursor(ctx.args[0], ctx.args[1]),
      'find_text': (ctx) => this.findText(ctx.args[0])
    });
  }
  
  // Handle user input (key presses, menu clicks, etc.)
  handleCommand(commandString) {
    this.pawscript.execute(commandString);
  }
}
```

## API Reference

### PawScript

#### Constructor
```typescript
new PawScript(config?: PawScriptConfig)
```

#### Methods
- `setHost(host: IPawScriptHost)`: Set the host application interface
- `registerCommand(name: string, handler: PawScriptHandler)`: Register a single command
- `registerCommands(commands: Record<string, PawScriptHandler>)`: Register multiple commands
- `execute(commandString: string, ...args: any[])`: Execute a command string
- `requestToken(cleanup?, parent?, timeout?)`: Request an async token
- `resumeToken(tokenId: string, result: boolean)`: Resume a suspended command
- `defineMacro(name: string, commands: string)`: Define a macro
- `executeMacro(name: string)`: Execute a macro
- `listMacros()`: Get list of defined macros
- `deleteMacro(name: string)`: Delete a macro
- `clearMacros()`: Clear all macros
- `getTokenStatus()`: Get information about active tokens
- `configure(config: Partial<PawScriptConfig>)`: Update configuration

### PawScriptHandler

Command handlers receive a `PawScriptContext` object:

```typescript
interface PawScriptContext {
  host: IPawScriptHost;           // Reference to host application
  args: any[];                    // Parsed command arguments
  state: any;                     // Current application state
  requestToken(cleanup?: Function): string;  // Request async token
  resumeToken(tokenId: string, result: boolean): void;  // Resume token
  // Result management
  setResult(value: any): void;    // Set formal result
  getResult(): any;               // Get current result
  hasResult(): boolean;           // Check if result exists
  clearResult(): void;            // Clear current result
}
```

#### Return Values
- `boolean`: Synchronous success/failure
- `string` (starting with "token_"): Async token for suspension

### Command Parsing

PawScript automatically parses command arguments:

```typescript
// String arguments (quoted)
pawscript.execute("echo 'hello world'");
// → ctx.args = ['hello world']

// Multiple arguments
pawscript.execute("move_cursor 10, 20");
// → ctx.args = [10, 20]

// Mixed types
pawscript.execute("create_window 'MyWindow', 100, 50, true");
// → ctx.args = ['MyWindow', 100, 50, true]

// Parenthetical content (passed as-is)
pawscript.execute("macro hello(save_file; exit)");
// → After syntactic sugar: ctx.args = ['hello', 'save_file; exit']
```

## Error Handling

PawScript provides robust error handling:

```typescript
pawscript.registerCommand('risky_operation', (ctx) => {
  try {
    // Risky operation
    return performRiskyOperation();
  } catch (error) {
    ctx.host.updateStatus(`Operation failed: ${error.message}`);
    return false;
  }
});
```

## Testing

The library works well with Jest and other testing frameworks:

```typescript
import { PawScript } from 'pawscript';

describe('My Application Commands', () => {
  let pawscript: PawScript;
  let mockHost: any;

  beforeEach(() => {
    mockHost = {
      getCurrentContext: jest.fn().mockReturnValue({}),
      updateStatus: jest.fn(),
      requestInput: jest.fn(),
      render: jest.fn()
    };

    pawscript = new PawScript({ debug: false });
    pawscript.setHost(mockHost);
  });

  test('should execute my command', () => {
    const myCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('my_command', myCommand);
    
    const result = pawscript.execute('my_command');
    expect(result).toBe(true);
    expect(myCommand).toHaveBeenCalled();
  });

  test('should handle async commands with tokens', () => {
    const asyncCommand = jest.fn().mockImplementation((ctx) => {
      const token = ctx.requestToken();
      setImmediate(() => {
        ctx.resumeToken(token, true);
      });
      return token;
    });
    
    pawscript.registerCommand('async_cmd', asyncCommand);
    
    const result = pawscript.execute('async_cmd');
    expect(typeof result).toBe('string');
    expect(result).toMatch(/^token_/);
  });
});
```

## Advanced Features

### Token Chaining

PawScript automatically chains tokens in command sequences:

```typescript
// If 'async_save' returns a token, 'async_backup' will wait for it
pawscript.execute('async_save; async_backup; notify_complete');
```

### Fallback Handlers

You can register fallback handlers for unknown commands:

```typescript
pawscript.setFallbackHandler((cmdName, args) => {
  if (cmdName.startsWith('custom_')) {
    return handleCustomCommand(cmdName, args);
  }
  return null; // Let PawScript handle as unknown command
});
```

### Token Status Monitoring

Monitor active tokens for debugging:

```typescript
const status = pawscript.getTokenStatus();
console.log(`Active tokens: ${status.activeCount}`);
status.tokens.forEach(token => {
  console.log(`${token.id}: age ${token.age}ms, children: ${token.childCount}`);
});
```

## Best Practices

### 1. Proper Async Pattern
```typescript
// ✅ CORRECT
pawscript.registerCommand('async_save', (ctx) => {
  const token = ctx.requestToken();
  setImmediate(() => {
    fs.writeFile('file.txt', data, (err) => {
      ctx.resumeToken(token, !err);
    });
  });
  return token;
});

// ❌ WRONG - Don't use Promises directly
pawscript.registerCommand('wrong_async', async (ctx) => {
  await fs.promises.writeFile('file.txt', data);
  return true;
});
```

### 2. Error Handling
```typescript
pawscript.registerCommand('safe_command', (ctx) => {
  try {
    const result = riskyOperation(ctx.args[0]);
    ctx.host.updateStatus('Operation completed');
    return true;
  } catch (error) {
    ctx.host.updateStatus(`Error: ${error.message}`);
    return false;
  }
});
```

### 3. State Management
```typescript
pawscript.registerCommand('context_aware', (ctx) => {
  const { cursor, selection } = ctx.state;
  if (!selection) {
    ctx.host.updateStatus('No selection available');
    return false;
  }
  // Process selection...
  return true;
});
```

## The Name

**PawScript** gets its name from the token-based suspension system - when a command needs to wait for an async operation to complete, execution "paws" (pauses) until the operation finishes. The name also nods to the language's origins in the mew text editor (which has a cat mascot), while being professional enough for standalone use.

## Migration from Other Command Systems

If you're migrating from a different command system:

1. **Wrap existing handlers**: Your existing command handlers can be wrapped to match the PawScriptHandler interface
2. **Update async commands**: Convert Promise-based async commands to use the token pattern
3. **Configure syntax**: Disable syntactic sugar if you need exact command parsing compatibility
4. **Update macros**: Migrate existing macros to PawScript's macro system

## License

MIT

## Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass with `npm test`
5. Submit a pull request

## Changelog

### 0.1.3
- Implemented braces for command evaluation (function-like behavior)
- Implemented substitution for macro arguments $* $# $1 $2
- Added result management system with formal results, in addition to the success/fail states
- Added command-line tool (`paw`) for executing PawScript files
- Added standard library commands (argc, argv, echo, read, true, false)
- Fixed syntactic sugar parsing for multi-line content
- Fixed token suspension and resumption for async operations
- Improved macro execution with proper state management
- Enhanced test coverage and documentation

### 0.1.2
- Minor fixes

### 0.1.1
- Initial release
- Basic command execution with sequences, conditionals, and alternatives
- Token-based suspension system ("paws" feature)
- Macro system with define/execute/list capabilities
- Syntactic sugar for convenient command syntax
- Full TypeScript support with comprehensive type definitions
- Host-agnostic design for easy integration
- Comprehensive test suite and documentation
