export interface SourcePosition {
  line: number;
  column: number;
  length: number;
  originalText: string;
  filename?: string;              
  macroContext?: MacroContext;    
}

export interface MacroContext {
  macroName: string;
  definitionFile: string;
  definitionLine: number;
  definitionColumn: number;
  invocationFile?: string;        
  invocationLine?: number;
  invocationColumn?: number;
  parentMacro?: MacroContext;     
}

// NEW: Brace expression context for proper position tracking
export interface BraceContext {
  startLine: number;              // Line in parent where brace starts
  startColumn: number;            // Column in parent where brace starts  
  parentFilename?: string;        // Parent source filename
  braceContent: string;           // The content inside the braces
  parentMacroContext?: MacroContext; // Parent's macro context if any
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
  // Command arguments
  args: any[];
  
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

// Enhanced substitution context with brace context
export interface SubstitutionContext {
  args: any[];
  executionState: any; // Will be ExecutionState class instance
  parentContext?: SubstitutionContext;
  macroContext?: MacroContext; 
  // NEW: Track accumulated position offsets for nested brace expressions
  currentLineOffset?: number;
  currentColumnOffset?: number;
}

// Source mapping for tracking original positions through transformations
export interface SourceMap {
  filename?: string;                    
  originalLines: string[];
  transformedToOriginal: Map<number, SourcePosition>;
  addMapping(transformedPos: number, originalPos: SourcePosition): void;
  getOriginalPosition(transformedPos: number): SourcePosition | null;
  
  // Methods for macro tracking
  addMacroContext(position: SourcePosition, context: MacroContext): SourcePosition;
  getMacroChain(position: SourcePosition): MacroContext[];
  
  // NEW: Methods for brace context tracking
  adjustForBraceContext(position: SourcePosition, braceContext: BraceContext): SourcePosition;
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
  braceContext?: BraceContext; // NEW: Track if we're parsing inside a brace expression
}

// Enhanced macro definition with source tracking
export interface MacroDefinition {
  name: string;
  commands: string;
  definitionFile: string;
  definitionLine: number;
  definitionColumn: number;
  timestamp: number;
}
