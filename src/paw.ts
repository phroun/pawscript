#!/usr/bin/env node

import * as fs from 'fs';
import * as path from 'path';
import * as readline from 'readline';
import { PawScript } from './pawscript';

class PawCLI {
  private pawscript: PawScript;
  private scriptArgs: string[] = [];
  private originalStdin: NodeJS.ReadStream;
  
  constructor() {
    this.pawscript = new PawScript({ debug: false });
    this.originalStdin = process.stdin;
    this.registerStandardLibrary();
  }
  
  private registerStandardLibrary() {
    // argc - returns number of arguments
    this.pawscript.registerCommand('argc', (ctx) => {
      ctx.setResult(this.scriptArgs.length);
      return true;
    });
    
    // argv - returns array of arguments or specific argument by index
    this.pawscript.registerCommand('argv', (ctx) => {
      if (ctx.args.length === 0) {
        ctx.setResult(this.scriptArgs);
      } else {
        const index = Number(ctx.args[0]);
        if (index >= 0 && index < this.scriptArgs.length) {
          ctx.setResult(this.scriptArgs[index]);
        } else {
          ctx.setResult(undefined);
        }
      }
      return true;
    });

    // script_error - output error messages with position information
    this.pawscript.registerCommand('script_error', (ctx) => {
      const message = ctx.args[0] || 'Unknown error';
      
      // Extract position information if available
      let errorOutput = `[SCRIPT ERROR] ${message}`;
      
      if (ctx.position) {
        errorOutput += ` at line ${ctx.position.line}, column ${ctx.position.column}`;
        
        // Add source context if available
        if (ctx.position.originalText) {
          errorOutput += `\n  Source: ${ctx.position.originalText}`;
        }
      }
      
      // Output to stderr
      process.stderr.write(errorOutput + '\n');
      return true;
    });
    
    // echo/write/print - output to stdout
    const outputCommand = (ctx: any) => {
      const text = ctx.args.join(' ');
      process.stdout.write(text + '\n');
      return true;
    };
    
    this.pawscript.registerCommand('echo', outputCommand);
    this.pawscript.registerCommand('write', outputCommand);
    this.pawscript.registerCommand('print', outputCommand);
    
    // read - read a line from stdin
    this.pawscript.registerCommand('read', (ctx) => {
      const token = ctx.requestToken();
      
      const rl = readline.createInterface({
        input: process.stdin,
        output: process.stderr, // Echo goes to stderr if needed
      });
      
      rl.once('line', (line) => {
        ctx.setResult(line);
        rl.close();
        ctx.resumeToken(token, true);
      });
      
      rl.once('close', () => {
        if (!ctx.hasResult()) {
          ctx.setResult('');
          ctx.resumeToken(token, false);
        }
      });
      
      return token;
    });
    
    // true - sets success state
    this.pawscript.registerCommand('true', (ctx) => {
      return true;
    });
    
    // false - sets error state
    this.pawscript.registerCommand('false', (ctx) => {
      return false;
    });
  }
  
  private async readFromStdin(): Promise<string> {
    return new Promise((resolve, reject) => {
      let data = '';
      
      process.stdin.setEncoding('utf8');
      
      process.stdin.on('data', (chunk) => {
        data += chunk;
      });
      
      process.stdin.on('end', () => {
        resolve(data);
      });
      
      process.stdin.on('error', (err) => {
        reject(err);
      });
    });
  }
  
  private async findScriptFile(filename: string): Promise<string | null> {
    // First try the exact filename
    if (fs.existsSync(filename)) {
      return filename;
    }
    
    // If no extension, try adding .paw
    if (!path.extname(filename)) {
      const pawFile = filename + '.paw';
      if (fs.existsSync(pawFile)) {
        return pawFile;
      }
    }
    
    return null;
  }
  
  private showUsage() {
    console.error(`Usage: paw [script.paw] [-- args...]
       paw < input.paw
       echo "commands" | paw

Execute PawScript commands from a file, stdin, or pipe.

Options:
  script.paw    Script file to execute (adds .paw extension if needed)
  --            Separates script filename from arguments (for stdin input)
  
Examples:
  paw hello.paw           # Execute hello.paw
  paw hello               # Execute hello.paw (adds .paw extension)
  paw script.paw -- a b   # Execute script.paw with args "a" and "b"
  echo "echo Hello" | paw # Execute commands from pipe
  paw -- arg1 arg2 < script.paw  # Execute from stdin with arguments
`);
  }
  
  async run() {
    const args = process.argv.slice(2);
    let scriptFile: string | null = null;
    let scriptContent: string = '';
    
    // Check for -- separator
    const separatorIndex = args.indexOf('--');
    let scriptArgs: string[] = [];
    let fileArgs: string[] = args;
    
    if (separatorIndex !== -1) {
      fileArgs = args.slice(0, separatorIndex);
      scriptArgs = args.slice(separatorIndex + 1);
    }
    
    this.scriptArgs = scriptArgs;
    
    // Check if stdin is redirected/piped
    const isStdinRedirected = !process.stdin.isTTY;
    
    if (fileArgs.length > 0) {
      // Filename provided
      const requestedFile = fileArgs[0];
      scriptFile = await this.findScriptFile(requestedFile);
      
      if (!scriptFile) {
        console.error(`Error: Script file not found: ${requestedFile}`);
        if (!path.extname(requestedFile)) {
          console.error(`Also tried: ${requestedFile}.paw`);
        }
        process.exit(1);
      }
      
      try {
        scriptContent = fs.readFileSync(scriptFile, 'utf8');
      } catch (error) {
        console.error(`Error reading script file: ${error}`);
        process.exit(1);
      }
      
      // Remaining fileArgs (after the script name) become script arguments
      if (separatorIndex === -1) {
        this.scriptArgs = fileArgs.slice(1);
      }
      
    } else if (isStdinRedirected) {
      // No filename, but stdin is redirected - read from stdin
      try {
        scriptContent = await this.readFromStdin();
      } catch (error) {
        console.error(`Error reading from stdin: ${error}`);
        process.exit(1);
      }
      
    } else {
      // No filename and stdin is not redirected - show usage
      this.showUsage();
      process.exit(1);
    }
    
    // Execute the script
    try {
      const result = this.pawscript.execute(scriptContent);
      
      // Exit with appropriate code
      if (typeof result === 'boolean') {
        process.exit(result ? 0 : 1);
      } else {
        // If result is a token, we have async operations pending
        // The process will exit when they complete
      }
      
    } catch (error) {
      console.error(`Script execution error: ${error}`);
      process.exit(1);
    }
  }
}

// Run the CLI if this file is executed directly
if (require.main === module) {
  const cli = new PawCLI();
  cli.run().catch((error) => {
    console.error(`Fatal error: ${error}`);
    process.exit(1);
  });
}

export { PawCLI };
