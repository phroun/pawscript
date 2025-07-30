export interface IPawScriptHost {
  // Core application state
  getCurrentContext(): any;
  updateStatus(message: string): void;
  requestInput(prompt: string, defaultValue?: string): Promise<string>;
  
  // UI operations
  render(): void;
  createWindow?(options: any): string;
  removeWindow?(id: string): void;
  
  // State management
  saveState?(): any;
  restoreState?(snapshot: any): void;
  
  // Event handling
  emit?(event: string, ...args: any[]): void;
  on?(event: string, handler: Function): void;
}

export interface SourcePosition {
  line: number;
  column: number;
  length: number;
  originalText: string;
}

export interface ParsedCommand {
  command: string;
  arguments: any[];
  position: SourcePosition;
  originalLine: string;
  type: 'single' | 'sequence' | 'conditional' | 'or';
}

export interface PawScriptError extends Error {
  position?: SourcePosition;
  originalLine?: string;
  context?: string[];
}

export interface PawScriptContext {
  // Host application reference
  host: IPawScriptHost;
  
  // Command arguments
  args: any[];
  
  // Current state info (provided by host)
  state: any;
  
  // Position information for error reporting
  position?: SourcePosition;
  
  // Utility methods
  requestToken(cleanup?: (tokenId: string) => void): string;
  resumeToken(tokenId: string, result: boolean): void;
  
  // Result management
  setResult(value: any): void;
  getResult(): any;
  hasResult(): boolean;
  clearResult(): void;
}

export type PawScriptHandler = (context: PawScriptContext) => boolean | string;

export interface PawScriptConfig {
  // Debug settings
  debug?: boolean;
  
  // Timeout settings
  defaultTokenTimeout?: number;
  
  // Syntax features
  enableSyntacticSugar?: boolean;
  allowMacros?: boolean;
  
  // Command parsing
  commandSeparators?: {
    sequence: string;    // default: ';'
    conditional: string; // default: '&'
    alternative: string; // default: '|'
  };
  
  // Error reporting
  showErrorContext?: boolean;
  contextLines?: number;
}

export interface TokenData {
  commandSequence: CommandSequence | null;
  parentToken: string | null;
  children: Set<string>;
  cleanupCallback: ((tokenId: string) => void) | null;
  timeoutId: NodeJS.Timeout | null;
  chainedToken: string | null;
  timestamp: number;
  // Store the actual execution state reference
  executionState?: any; // The actual ExecutionState instance
  suspendedResult?: any;
  hasSuspendedResult?: boolean;
  // Position tracking for error reporting
  position?: SourcePosition;
}

export interface CommandSequence {
  type: 'sequence' | 'conditional' | 'or';
  remainingCommands: ParsedCommand[];
  currentIndex: number;
  totalCommands: number;
  originalCommand: string;
  timestamp: number;
  // Result state for command sequences
  inheritedResult?: any;
  hasInheritedResult?: boolean;
  // Position tracking
  position?: SourcePosition;
}

// Substitution context for macro argument access during brace evaluation
export interface SubstitutionContext {
  args: any[];
  executionState: any; // Will be ExecutionState class instance
  parentContext?: SubstitutionContext;
}

// Source mapping for tracking original positions through transformations
export interface SourceMap {
  originalLines: string[];
  transformedToOriginal: Map<number, SourcePosition>;
  addMapping(transformedPos: number, originalPos: SourcePosition): void;
  getOriginalPosition(transformedPos: number): SourcePosition | null;
}

// Token with position information for parsing
export interface PositionedToken {
  text: string;
  position: SourcePosition;
  type: 'command' | 'argument' | 'operator' | 'separator';
}

// Parsing context that maintains position information
export interface ParsingContext {
  sourceMap: SourceMap;
  currentLine: number;
  currentColumn: number;
  nestingDepth: number;
  inQuote: boolean;
  quoteChar: string | null;
}
