# pawscript

PawScript: A command language with token-based suspension for text editors and command-driven applications.

## Features

- **Complex Command Syntax**: Support for sequences (`;`), conditionals (`&`), and alternatives (`|`)
- **Token-Based Suspension**: Pause and resume command execution for long-running operations
- **Macro System**: Define and execute reusable command sequences
- **Syntactic Sugar**: Automatic transformation of convenient syntax patterns
- **Type Safety**: Full TypeScript support with comprehensive type definitions
- **Host Agnostic**: Clean interface for integration with any application

## Installation

```bash
npm install pawscript
```

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

### Macros
```typescript
// Define macro
pawscript.defineMacro('quick_save', 'save_file; update_status "Saved"');

// Execute macro
pawscript.execute('quick_save');

// Macros in sequences
pawscript.execute('quick_save; close_buffer');
```

## Token-Based Suspension (The "Paws" Feature)

For long-running operations, commands can return tokens that pause execution:

```typescript
pawscript.registerCommand('async_operation', (ctx) => {
  // Request a token to pause execution
  const token = ctx.requestToken((tokenId) => {
    console.log('Operation was interrupted:', tokenId);
  });
  
  // Start async operation
  setTimeout(() => {
    console.log('Async operation completed');
    ctx.resumeToken(token, true); // Resume with success
  }, 5000);
  
  return token; // Return token to pause sequence
});

// This will pause at async_operation and resume when it completes
pawscript.execute('async_operation; echo "This runs after async completes"');
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
  defaultTokenTimeout: 300000,     // Token timeout in ms
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
}
```

#### Return Values
- `boolean`: Synchronous success/failure
- `string` (starting with "token_"): Async token for suspension
- `Promise<boolean>`: Async operation

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

The library includes comprehensive test utilities:

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
});
```

## The Name

**PawScript** gets its name from the token-based suspension system - when a command needs to wait for an async operation to complete, execution "paws" (pauses) until the operation finishes. The name also nods to the language's origins in the mew text editor (which has a cat mascot), while being professional enough for standalone use.

## License

MIT

## Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass with `npm test`
5. Submit a pull request

## Changelog

### 1.0.0
- Initial release
- Basic command execution
- Token-based suspension system ("paws")
- Macro system
- TypeScript support
