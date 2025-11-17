// test-node.mjs
import { PawScript } from './dist/index.js';

async function main() {
  const paw = new PawScript({ debug: false, allowMacros: true });
  await paw.ready;
  console.log('Hi');
  
  paw.registerCommands({
    hello: (ctx) => {
      console.log('Hello from PawScript!');
      fmt.Println("Hello from Go gommand!");
      return true; // synchronous success
    },
    conlog: (ctx) => {
      console.log('conlog:', ctx.args?.[0]);
      return true;
    },
  });

  const result = paw.execute('echo "Hello Node"');
  console.log("Execute Returned: ", result);
  const m = paw.execute('echo "{read}"');
}

main().catch(console.error);

