import { SourcePosition, PawScriptError } from './types';

export class Logger {
  private enabled: boolean;
  
  constructor(enabled: boolean = false) {
    this.enabled = enabled;
  }
  
  debug(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.log(`[PawScript DEBUG] ${message}`, ...args);
    }
  }
  
  warn(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.warn(`[PawScript WARN] ${message}`, ...args);
    }
  }
  
  error(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.error(`[PawScript ERROR] ${message}`, ...args);
    }
  }
  
  // Enhanced error reporting with position information
  errorWithPosition(message: string, position?: SourcePosition, context?: string[]): void {
    if (!this.enabled) return;
    
    let errorMessage = `[PawScript ERROR] ${message}`;
    
    if (position) {
      errorMessage += `\n  at line ${position.line}, column ${position.column}`;
      
      if (context && context.length > 0) {
        errorMessage += '\n';
        
        // Show context lines with highlighting
        const contextStart = Math.max(0, position.line - 2);
        const contextEnd = Math.min(context.length, position.line + 1);
        
        for (let i = contextStart; i < contextEnd; i++) {
          const lineNum = i + 1;
          const isErrorLine = lineNum === position.line;
          const prefix = isErrorLine ? '>' : ' ';
          const lineNumStr = lineNum.toString().padStart(3);
          
          errorMessage += `\n  ${prefix} ${lineNumStr} | ${context[i]}`;
          
          if (isErrorLine && position.column > 0) {
            // Add caret indicator
            const indent = '      | ' + ' '.repeat(position.column - 1);
            const caret = '^'.repeat(Math.max(1, position.length));
            errorMessage += `\n  ${indent}${caret}`;
          }
        }
      }
    }
    
    console.error(errorMessage);
  }
  
  // Create a PawScriptError with position information
  createError(message: string, position?: SourcePosition, context?: string[]): PawScriptError {
    const error = new Error(message) as PawScriptError;
    error.position = position;
    error.context = context;
    
    if (position) {
      error.originalLine = context && context[position.line - 1] || '';
    }
    
    return error;
  }
  
  // Log a PawScriptError with full context
  logError(error: PawScriptError): void {
    this.errorWithPosition(error.message, error.position, error.context);
  }
  
  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }
  
  // Format command execution errors with position
  commandError(commandName: string, message: string, position?: SourcePosition, context?: string[]): void {
    const fullMessage = `Error executing command '${commandName}': ${message}`;
    this.errorWithPosition(fullMessage, position, context);
  }
  
  // Format parsing errors with position
  parseError(message: string, position?: SourcePosition, context?: string[]): void {
    const fullMessage = `Parse error: ${message}`;
    this.errorWithPosition(fullMessage, position, context);
  }
  
  // Format unknown command errors with position
  unknownCommandError(commandName: string, position?: SourcePosition, context?: string[]): void {
    const message = `Unknown command: ${commandName}`;
    this.errorWithPosition(message, position, context);
  }
  
  // Helper to create context from source lines
  static createContext(sourceLines: string[]): string[] {
    return sourceLines.slice();
  }
  
  // Helper to extract context around a position
  static extractContext(sourceLines: string[], position: SourcePosition, contextLines: number = 2): string[] {
    const start = Math.max(0, position.line - contextLines - 1);
    const end = Math.min(sourceLines.length, position.line + contextLines);
    return sourceLines.slice(start, end);
  }
}
