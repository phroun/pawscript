# PawScript WASM - Quick Start

## Prerequisites

- Go 1.21+ with WASM support
- Node.js 18+
- npm or yarn

## Setup (5 minutes)

### 1. Get wasm_exec.js
```bash
# Copy from your Go installation
cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" .
```

### 2. Install Dependencies
```bash
npm install
```

### 3. Build
```bash
# Build everything
npm run build

# Or use the helper script
chmod +x build.sh
./build.sh
```

## Quick Examples

### Node.js (30 seconds)

```typescript
import { PawScript } from './index.js';

const paw = new PawScript();
await paw.ready;

// Execute commands
paw.execute('echo "Hello!"');

// Register custom command
paw.registerCommand('greet', (ctx) => {
  console.log(`Hello, ${ctx.args[0]}!`);
  return true;
});

paw.execute('greet "World"');
```

### Browser (1 minute)

```html
<!DOCTYPE html>
<html>
<head>
  <script src="wasm_exec.js"></script>
  <script type="module">
    import { PawScript } from './index.js';
    
    const paw = new PawScript();
    await paw.ready;
    
    paw.registerCommand('alert_msg', (ctx) => {
      alert(ctx.args[0]);
      return true;
    });
    
    paw.execute('alert_msg "Hello from WASM!"');
  </script>
</head>
<body>
  <h1>PawScript in Browser</h1>
</body>
</html>
```

## Common Patterns

### Setting Results
```typescript
paw.registerCommand('double', (ctx) => {
  const num = Number(ctx.args[0] || 0);
  ctx.setResult(num * 2);
  return true;
});

paw.execute('double 21');  // Result is 42
```

### Async Operations
```typescript
paw.registerCommand('fetch_data', (ctx) => {
  const token = ctx.requestToken();
  
  fetch('https://api.example.com/data')
    .then(response => response.json())
    .then(data => {
      ctx.setResult(data);
      paw.resumeToken(token, true);
    })
    .catch(() => {
      paw.resumeToken(token, false);
    });
  
  return token;
});
```

### Conditional Execution
```typescript
paw.registerCommand('check', (ctx) => {
  const value = Number(ctx.args[0]);
  return value > 0;  // true/false controls flow
});

paw.execute('check 5 && echo "Positive!"');
```

### Batch Commands
```typescript
paw.registerCommands({
  add: (ctx) => {
    const sum = ctx.args.reduce((a, b) => Number(a) + Number(b), 0);
    ctx.setResult(sum);
    return true;
  },
  multiply: (ctx) => {
    const product = ctx.args.reduce((a, b) => Number(a) * Number(b), 1);
    ctx.setResult(product);
    return true;
  }
});
```

## Debugging

### Enable Debug Mode
```typescript
const paw = new PawScript({ debug: true });
```

### Check Token Status
```typescript
const status = paw.getTokenStatus();
console.log('Active tokens:', status);
```

### Handle Errors
```typescript
const result = paw.execute('some_command');
if (result.type === 'status' && !result.success) {
  console.error('Command failed:', result.error);
}
```

## Testing

### Run Node.js Demo
```bash
npm run test:node
```

### Run Browser Demo
```bash
npm run test:browser
# Opens browser to demo page
```

## Next Steps

1. Read [README.md](README.md) for full API reference
2. See [IMPROVEMENTS.md](IMPROVEMENTS.md) for technical details
3. Check [main.ts](main.ts) for comprehensive examples
4. Open [index.html](index.html) in browser for interactive demo

## Troubleshooting

### "Go is not defined"
- Make sure `wasm_exec.js` is loaded before your module

### "WASM not ready yet"
- Wait for `await paw.ready` before calling methods

### Build fails
- Check Go version: `go version` (need 1.21+)
- Verify GOROOT: `go env GOROOT`

### Token never resumes
- Make sure to call `paw.resumeToken(token, status)`
- Check token status: `paw.getTokenStatus()`

## Performance Tips

1. **Batch commands**: Use `;` separator instead of multiple execute() calls
2. **Reuse instance**: Create one PawScript instance, reuse it
3. **Async for I/O**: Use tokens for network/file operations
4. **Avoid frequent small commands**: Combine when possible

## Security Notes

- WASM runs in sandbox (same as JavaScript)
- No file system access (use command handlers for I/O)
- No network access from WASM (use JS fetch in handlers)
- Commands registered from JS have full JS privileges

## Example Project Structure

```
my-pawscript-app/
├── pawscript.wasm       # Built WASM binary
├── wasm_exec.js         # Go WASM runtime
├── node_modules/
│   └── pawscript-wasm/  # This package
├── src/
│   ├── commands.ts      # Your custom commands
│   └── app.ts           # Your app logic
├── package.json
└── tsconfig.json
```

```typescript
// src/commands.ts
export function registerMyCommands(paw) {
  paw.registerCommands({
    // Your commands here
  });
}

// src/app.ts
import { PawScript } from 'pawscript-wasm';
import { registerMyCommands } from './commands';

const paw = new PawScript();
await paw.ready;

registerMyCommands(paw);

// Use PawScript in your app
paw.execute('my_command arg1 arg2');
```

## Getting Help

- Check issues in the repository
- Review examples in `main.ts` and `index.html`
- Read the full [README.md](README.md)

Happy scripting! 🐾
