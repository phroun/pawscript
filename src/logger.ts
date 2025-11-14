import { SourcePosition, MacroContext } from './types';

export interface PawScriptError extends Error {
  position?: SourcePosition;
  originalLine?: string;
  context?: string[];
}

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
  
  // Enhanced error reporting with position information and macro context
  errorWithPosition(message: string, position?: SourcePosition, context?: string[]): void {
    if (!this.enabled) return;
    
    let errorMessage = `[PawScript ERROR] ${message}`;
    
    if (position) {
      // Add basic position info
      const filename = position.filename || '<unknown>';
      errorMessage += `\n  at line ${position.line}, column ${position.column} in ${filename}`;
      
      // Add macro context if present
      if (position.macroContext) {
        errorMessage += this.formatMacroContext(position.macroContext);
      }
      
      // Add source context lines
      if (context && context.length > 0) {
        errorMessage += this.formatSourceContext(position, context);
      }
    }
    
    console.error(errorMessage);
  }
  
  // Format macro call chain for error messages
  private formatMacroContext(macroContext: MacroContext): string {
    const chain = this.getMacroChain(macroContext);
    
    let message = '\n\nMacro call chain:';
    
    chain.forEach((context, index) => {
      const indent = '  '.repeat(index + 1);
      message += `\n${indent}→ macro "${context.macroName}"`;
      message += `\n${indent}  defined in ${context.definitionFile}:${context.definitionLine}:${context.definitionColumn}`;
      
      if (context.invocationFile && context.invocationLine) {
        message += `\n${indent}  called from ${context.invocationFile}:${context.invocationLine}:${context.invocationColumn}`;
      }
    });
    
    return message;
  }
  
  // Extract macro chain from a macro context
  private getMacroChain(macroContext: MacroContext): MacroContext[] {
    const chain: MacroContext[] = [];
    let current: MacroContext | undefined = macroContext;
    
    while (current) {
      chain.push(current);
      current = current.parentMacro;
    }
    
    return chain;
  }
  
  // Format source context with line numbers and caret indicators
  private formatSourceContext(position: SourcePosition, context: string[]): string {
    let message = '\n';
    
    // Show context lines with highlighting
    const contextStart = Math.max(0, position.line - 2);
    const contextEnd = Math.min(context.length, position.line + 1);
    
    for (let i = contextStart; i < contextEnd; i++) {
      const lineNum = i + 1;
      const isErrorLine = lineNum === position.line;
      const prefix = isErrorLine ? '>' : ' ';
      const lineNumStr = lineNum.toString().padStart(3);
      
      message += `\n  ${prefix} ${lineNumStr} | ${context[i]}`;
      
      if (isErrorLine && position.column > 0) {
        // Add caret indicator
        const indent = '      | ' + ' '.repeat(position.column - 1);
        const caret = '^'.repeat(Math.max(1, position.length));
        message += `\n  ${indent}${caret}`;
      }
    }
    
    return message;
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
  
  // Log a PawScriptError with full context (including macro chain)
  logError(error: PawScriptError): void {
    this.errorWithPosition(error.message, error.position, error.context);
  }
  
  // Format a complete error message with macro context
  formatMacroError(error: PawScriptError): string {
    let message = error.message;
    
    if (error.position) {
      const filename = error.position.filename || '<unknown>';
      message += ` at line ${error.position.line}, column ${error.position.column} in ${filename}`;
      
      if (error.position.macroContext) {
        message += this.formatMacroContext(error.position.macroContext);
      }
      
      if (error.context && error.context.length > 0) {
        message += this.formatSourceContext(error.position, error.context);
      }
    }
    
    return message;
  }
  
  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }
  
  // Format command execution errors with position and macro context
  commandError(commandName: string, message: string, position?: SourcePosition, context?: string[]): void {
    const fullMessage = `Error executing command '${commandName}': ${message}`;
    this.errorWithPosition(fullMessage, position, context);
  }
  
  // FIXED: Parse errors should ALWAYS be visible, regardless of debug setting
  parseError(message: string, position?: SourcePosition, context?: string[]): void {
    const fullMessage = `Parse error: ${message}`;
    
    // Always show parse errors - they're critical syntax issues
    let errorOutput = `[PawScript ERROR] ${fullMessage}`;
    
    if (position) {
      // Add basic position info
      const filename = position.filename || '<unknown>';
      errorOutput += `\n  at line ${position.line}, column ${position.column} in ${filename}`;
      
      // Add macro context if present
      if (position.macroContext) {
        errorOutput += this.formatMacroContext(position.macroContext);
      }
      
      // Add source context lines
      if (context && context.length > 0) {
        errorOutput += this.formatSourceContext(position, context);
      }
    }
    
    // Always output to stderr, regardless of debug setting
    console.error(errorOutput);
  }
  
  // FIXED: Unknown command errors should ALWAYS be visible, like parse errors
  unknownCommandError(commandName: string, position?: SourcePosition, context?: string[]): void {
    const message = `Unknown command: ${commandName}`;
    
    // Always show unknown command errors - they're critical execution issues
    let errorOutput = `[PawScript ERROR] ${message}`;
    
    if (position) {
      // Add basic position info
      const filename = position.filename || '<unknown>';
      errorOutput += `\n  at line ${position.line}, column ${position.column} in ${filename}`;
      
      // Add macro context if present
      if (position.macroContext) {
        errorOutput += this.formatMacroContext(position.macroContext);
      }
      
      // Add source context lines
      if (context && context.length > 0) {
        errorOutput += this.formatSourceContext(position, context);
      }
    }
    
    // Always output to stderr, regardless of debug setting
    console.error(errorOutput);
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
