// src/main.ts
import { PawScript } from './index.js';

async function main() {
  console.log('=== PawScript WASM Demo ===\n');

  const paw = new PawScript({ debug: false, allowMacros: true });
  await paw.ready;

  console.log('✓ PawScript WASM ready!\n');

  // --- Test 1: Basic echo command ---
  console.log('Test 1: Basic commands');
  let result = paw.execute('echo "Hello from WASM!"');
  console.log('Result:', result);
  console.log();

  // --- Test 2: Register custom synchronous command ---
  console.log('Test 2: Custom synchronous command');
  paw.registerCommand('greet', (ctx) => {
    const name = ctx.args[0] || 'World';
    console.log(`Hello, ${name}!`);
    ctx.setResult(`Greeted ${name}`);
    return true; // synchronous success
  });

  result = paw.execute('greet "WASM User"');
  console.log('Result:', result);
  console.log();

  // --- Test 3: Command with result value ---
  console.log('Test 3: Command that sets result');
  paw.registerCommand('compute', (ctx) => {
    const a = Number(ctx.args[0]) || 0;
    const b = Number(ctx.args[1]) || 0;
    const sum = a + b;
    ctx.setResult(sum);
    console.log(`Computed: ${a} + ${b} = ${sum}`);
    return true;
  });

  result = paw.execute('compute 5 7');
  console.log('Result:', result);
  console.log();

  // --- Test 4: Chaining commands with operators ---
  console.log('Test 4: Command chaining');
  result = paw.execute('echo "First" ; echo "Second" ; echo "Third"');
  console.log('Result:', result);
  console.log();

  // --- Test 5: Conditional execution ---
  console.log('Test 5: Conditional execution');
  paw.registerCommand('check_positive', (ctx) => {
    const num = Number(ctx.args[0]) || 0;
    const isPositive = num > 0;
    console.log(`${num} is ${isPositive ? 'positive' : 'not positive'}`);
    return isPositive;
  });

  result = paw.execute('check_positive 5 && echo "Number is positive!"');
  console.log('Result:', result);

  result = paw.execute('check_positive -3 && echo "This should not print"');
  console.log('Result:', result);
  console.log();

  // --- Test 6: Async command with token ---
  console.log('Test 6: Async command (demonstrates token)');
  paw.registerCommand('wait_for_input', (ctx) => {
    const token = ctx.requestToken();
    console.log(`Got async token: ${token}`);
    
    // Simulate async work
    setTimeout(() => {
      console.log('Async work complete, resuming...');
      ctx.setResult('Async result value');
      paw.resumeToken(token, true);
    }, 1000);

    return token; // Return token to suspend
  });

  result = paw.execute('wait_for_input');
  console.log('Result (immediate):', result);
  console.log('Waiting for async completion...');
  
  // Wait a bit for async completion
  await new Promise(resolve => setTimeout(resolve, 1500));
  console.log();

  // --- Test 7: Multiple commands ---
  console.log('Test 7: Multiple custom commands');
  paw.registerCommands({
    add: (ctx) => {
      const sum = ctx.args.reduce((a, b) => Number(a) + Number(b), 0);
      ctx.setResult(sum);
      console.log(`Sum: ${sum}`);
      return true;
    },
    multiply: (ctx) => {
      const product = ctx.args.reduce((a, b) => Number(a) * Number(b), 1);
      ctx.setResult(product);
      console.log(`Product: ${product}`);
      return true;
    },
    log_args: (ctx) => {
      console.log('Arguments received:', ctx.args);
      return true;
    }
  });

  result = paw.execute('add 1 2 3 4 5');
  console.log('Result:', result);

  result = paw.execute('multiply 2 3 4');
  console.log('Result:', result);

  result = paw.execute('log_args "hello" 42 true');
  console.log('Result:', result);
  console.log();

  // --- Test 8: Check token status ---
  console.log('Test 8: Token status');
  const tokenStatus = paw.getTokenStatus();
  console.log('Active tokens:', tokenStatus);
  console.log();

  console.log('=== Demo Complete ===');
}

main().catch(err => {
  console.error('Error in demo:', err);
  process.exit(1);
});
