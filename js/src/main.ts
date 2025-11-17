import { PawScript } from './index.js';

async function main() {
  const paw = new PawScript({ debug: true, allowMacros: true });
  await paw.ready;

  console.log("PawScript WASM ready!");

  paw.registerCommands({
    hello: (ctx) => {
      console.log("Hello from JS command!");
      return true; // synchronous success
    },
    browserlog: (ctx) => {
      console.log("Browser Log:", ctx.args?.[0]);
      return true;
    },
  });

  const result = paw.execute('echo "Hello Browser"');
  console.log("Execute returned:", result);

  paw.execute('hello');
}

main().catch(console.error);
