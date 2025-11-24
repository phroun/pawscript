// src/index.ts
declare const Go: any; // provided by wasm_exec.js

export type PawConfig = { 
  debug?: boolean; 
  allowMacros?: boolean;
};

export type ExecuteResult = 
  | { type: 'status'; success: boolean; error?: string }
  | { type: 'token'; token: string }
  | { type: 'early_return'; success: boolean; hasResult: boolean; result?: any };

export interface PawContext {
  args: any[];
  setResult(value: any): void;
  getResult(): any;
  hasResult(): boolean;
  requestToken(): string;
}

export type CommandHandler = (ctx: PawContext) => boolean | string;

export class PawScript {
  public ready: Promise<void>;
  private go: any;
  private instance: WebAssembly.Instance | null = null;
  private config: PawConfig;

  constructor(config: PawConfig = {}) {
    this.config = config;
    this.ready = this.init();
  }

  private async init() {
    if (typeof window === "undefined") {
      // Node environment: load wasm_exec support
      // Note: wasm_exec_loader.js is a plain script, not an ES module
      try {
        // @ts-ignore - wasm_exec_loader.js is not an ES module
        await import('./wasm_exec_loader.js');
      } catch (err) {
        console.warn("Could not load wasm_exec_loader.js:", err);
        // Go might already be defined globally
      }
    }

    this.go = new Go();

    // Redirect stdout/stderr
    this.go.importObject.env = {
      ...this.go.importObject.env,
      write: (fd: number, ptr: number, len: number) => {
        const memory = new Uint8Array(this.go._inst.exports.mem.buffer, ptr, len);
        const text = new TextDecoder("utf-8").decode(memory);
        if (fd === 1) console.log(text);
        else if (fd === 2) console.error(text);
        return len;
      }
    };

    let wasmBytes: ArrayBuffer;

    if (typeof window === "undefined") {
      const fs = await import("fs/promises");
      const path = await import("path");
      const __dirname = path.dirname(new URL(import.meta.url).pathname);
      const wasmPath = path.join(__dirname, "pawscript.wasm");
      const buffer = await fs.readFile(wasmPath);
      wasmBytes = buffer.buffer.slice(buffer.byteOffset, buffer.byteOffset + buffer.byteLength);
    } else {
      const resp = await fetch("./pawscript.wasm");
      wasmBytes = await resp.arrayBuffer();
    }

    const mod = await WebAssembly.instantiate(wasmBytes, this.go.importObject);
    this.instance = mod.instance;
    
    // Start the Go runtime (non-blocking)
    this.go.run(this.instance).catch((err: any) => {
      console.error("Go runtime exited with error:", err);
    });

    // Wait for the Go runtime to set up global functions
    // The Go code prints "PawScript WASM ready!" and registers functions
    await this.waitForReady();
  }

  private async waitForReady(maxAttempts = 50, delayMs = 100): Promise<void> {
    for (let i = 0; i < maxAttempts; i++) {
      const g = globalThis as any;
      if (g.pawscript_execute && 
          g.pawscript_register_command && 
          g.pawscript_resume_token) {
        // All core functions are available
        // Add a small extra delay to ensure everything is fully initialized
        await new Promise(resolve => setTimeout(resolve, 50));
        return;
      }
      await new Promise(resolve => setTimeout(resolve, delayMs));
    }
    throw new Error('Timeout waiting for PawScript WASM to initialize');
  }

  /**
   * Register a JavaScript function as a PawScript command
   */
  public registerCommand(name: string, handler: CommandHandler): boolean {
    const register = (globalThis as any).pawscript_register_command;
    if (!register) throw new Error("PawScript WASM not ready yet");
    return register(name, handler);
  }

  /**
   * Register multiple commands at once
   */
  public registerCommands(commands: Record<string, CommandHandler>): void {
    for (const [name, handler] of Object.entries(commands)) {
      this.registerCommand(name, handler);
    }
  }

  /**
   * Execute a PawScript command string
   * Returns execution result with type information
   */
  public execute(command: string): ExecuteResult {
    const exec = (globalThis as any).pawscript_execute;
    if (!exec) throw new Error("PawScript WASM not ready yet");
    return exec(command);
  }

  /**
   * Resume an async token with status and optional result
   */
  public resumeToken(token: string, success: boolean, result?: any): boolean {
    const resume = (globalThis as any).pawscript_resume_token;
    if (!resume) throw new Error("PawScript WASM not ready yet");
    return resume(token, success, result);
  }

  /**
   * Get information about active tokens
   */
  public getTokenStatus(): any {
    const getStatus = (globalThis as any).pawscript_get_token_status;
    if (!getStatus) throw new Error("PawScript WASM not ready yet");
    return getStatus();
  }

  /**
   * Execute a command and wait for completion if async
   * Returns a promise that resolves with the result
   */
  public async executeAsync(command: string, onProgress?: (status: string) => void): Promise<boolean> {
    return new Promise((resolve, reject) => {
      const result = this.execute(command);

      if (result.type === 'status') {
        resolve(result.success);
      } else if (result.type === 'token') {
        // Token-based async - caller needs to handle resume
        onProgress?.(`Waiting for token: ${result.token}`);
        // Note: In a real implementation, you'd set up a listener for when the token completes
        // For now, just reject with info
        reject(new Error(`Async token returned: ${result.token}. Call resumeToken() to continue.`));
      } else if (result.type === 'early_return') {
        resolve(result.success);
      } else {
        reject(new Error('Unknown result type'));
      }
    });
  }
}
