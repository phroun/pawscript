// src/index.ts
declare const Go: any; // provided by wasm_exec.js

export type PawConfig = { debug?: boolean; allowMacros?: boolean };

export class PawScript {
  public ready: Promise<void>;
  private go: any;
  private instance: WebAssembly.Instance | null = null;
  private jsCommands: Map<string, (ctx: any) => any> = new Map();
  private pendingTokens: Map<string, (status: boolean) => void> = new Map();
  private config: PawConfig;

  constructor(config: PawConfig = {}) {
    this.config = config;
    this.ready = this.init();
  }

  private async init() {
  
    if (typeof window === "undefined") {
      // Node environment: dynamically load wasm_exec.js
      await import('./wasm_exec_loader.js'); // ensure Go is defined globally
    }

    this.go = new Go();

    // Optional: redirect stdout/stderr
    this.go.importObject.env = {
      ...this.go.importObject.env,
      write: (fd: number, ptr: number, len: number) => {
        const memory = new Uint8Array(this.go._inst.exports.mem.buffer, ptr, len);
        const text = new TextDecoder("utf-8").decode(memory);
        if (fd === 1) console.log(text);  // stdout
        else if (fd === 2) console.error(text); // stderr
        return len;
      }
    };

    let wasmBytes: ArrayBuffer;

    if (typeof window === "undefined") {
      const fs = await import("fs/promises");
      const path = await import("path");

      // Get directory of current JS file
      const __dirname = path.dirname(new URL(import.meta.url).pathname);

      // WASM is in the same folder as index.js
      const wasmPath = path.join(__dirname, "pawscript.wasm");

      const buffer = await fs.readFile(wasmPath); // returns Node Buffer
      wasmBytes = buffer.buffer.slice(buffer.byteOffset, buffer.byteOffset + buffer.byteLength); // convert to ArrayBuffer
    } else {
      const resp = await fetch("./pawscript.wasm");
      wasmBytes = await resp.arrayBuffer();
    }

    const mod = await WebAssembly.instantiate(wasmBytes, this.go.importObject);
    this.instance = mod.instance;
    this.go.run(this.instance).catch((err: any) => {
      console.error("Go runtime exited with error:", err);
    });
  }

  // Register JS functions as PawScript commands
  public registerCommands(commands: Record<string, (ctx: any) => any>) {
    for (const [name, fn] of Object.entries(commands)) {
      this.jsCommands.set(name, fn);
      (globalThis as any).pawscript_register_js_command(name, fn);
    }
  }

  // Execute a command string
  public execute(command: string) {
    const exec = (globalThis as any).pawscript_execute;
    if (!exec) throw new Error("PawScript WASM not ready yet");
    return exec(command);
  }

  // Resume an async token
  public resumeToken(token: string, status: boolean) {
    const resume = (globalThis as any).pawscript_resume_token;
    if (!resume) throw new Error("PawScript WASM not ready yet");
    return resume(token, status);
  }
}

