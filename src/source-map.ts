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

export interface BraceContext {
  startLine: number;              
  startColumn: number;            
  parentFilename?: string;        
  braceContent: string;           
  parentMacroContext?: MacroContext; 
}

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

export class SourceMapImpl implements SourceMap {
  public filename?: string;
  public originalLines: string[];
  public transformedToOriginal: Map<number, SourcePosition>;
  public braceContext?: BraceContext; // NEW: Track if this source map is for a brace expression
  
  constructor(originalSource: string, filename?: string, braceContext?: BraceContext) {
    this.filename = filename;
    this.originalLines = originalSource.split('\n');
    this.transformedToOriginal = new Map();
    this.braceContext = braceContext;
  }
  
  addMapping(transformedPos: number, originalPos: SourcePosition): void {
    // Ensure filename is set if not already present
    if (!originalPos.filename && this.filename) {
      originalPos = { ...originalPos, filename: this.filename };
    }
    
    // Apply brace context adjustment if this is a brace expression source map
    if (this.braceContext) {
      originalPos = this.adjustForBraceContext(originalPos, this.braceContext);
    }
    
    this.transformedToOriginal.set(transformedPos, originalPos);
  }
  
  getOriginalPosition(transformedPos: number): SourcePosition | null {
    return this.transformedToOriginal.get(transformedPos) || null;
  }
  
  // Get context lines around a position
  getContext(position: SourcePosition, contextLines: number = 2): string[] {
    // If we have brace context, we need to get context from the parent source
    if (this.braceContext) {
      // This is tricky - we'd need access to parent source lines
      // For now, return the brace content lines
      const start = Math.max(0, position.line - contextLines - 1);
      const end = Math.min(this.originalLines.length, position.line + contextLines);
      return this.originalLines.slice(start, end);
    }
    
    const start = Math.max(0, position.line - contextLines - 1);
    const end = Math.min(this.originalLines.length, position.line + contextLines);
    return this.originalLines.slice(start, end);
  }
  
  // Add macro context to a position
  addMacroContext(position: SourcePosition, context: MacroContext): SourcePosition {
    return {
      ...position,
      macroContext: context
    };
  }
  
  // Get the full macro call chain from a position
  getMacroChain(position: SourcePosition): MacroContext[] {
    const chain: MacroContext[] = [];
    let current = position.macroContext;
    
    while (current) {
      chain.push(current);
      current = current.parentMacro;
    }
    
    return chain;
  }
  
  // NEW: Adjust position for brace context
  adjustForBraceContext(position: SourcePosition, braceContext: BraceContext): SourcePosition {
    let adjustedLine = braceContext.startLine;
    let adjustedColumn = braceContext.startColumn;
    
    if (position.line === 1) {
      // Error is on the first line of brace content
      // Add the column offset (plus 1 for the opening brace)
      adjustedColumn = braceContext.startColumn + position.column;
    } else {
      // Error is on a subsequent line
      // Line number is relative to brace start
      adjustedLine = braceContext.startLine + position.line - 1;
      adjustedColumn = position.column; // Column is absolute on non-first lines
    }
    
    return {
      ...position,
      line: adjustedLine,
      column: adjustedColumn,
      filename: braceContext.parentFilename || position.filename,
      macroContext: braceContext.parentMacroContext || position.macroContext
    };
  }
  
  // Create a position object with enhanced context
  static createPosition(
    line: number, 
    column: number, 
    length: number, 
    originalText: string,
    filename?: string,
    macroContext?: MacroContext
  ): SourcePosition {
    return {
      line,
      column,
      length,
      originalText,
      filename,
      macroContext
    };
  }
  
  // Create a macro context
  static createMacroContext(
    macroName: string,
    definitionFile: string,
    definitionLine: number,
    definitionColumn: number,
    invocationFile?: string,
    invocationLine?: number,
    invocationColumn?: number,
    parentMacro?: MacroContext
  ): MacroContext {
    return {
      macroName,
      definitionFile,
      definitionLine,
      definitionColumn,
      invocationFile,
      invocationLine,
      invocationColumn,
      parentMacro
    };
  }
  
  // NEW: Create a brace context
  static createBraceContext(
    startLine: number,
    startColumn: number,
    braceContent: string,
    parentFilename?: string,
    parentMacroContext?: MacroContext
  ): BraceContext {
    return {
      startLine,
      startColumn,
      parentFilename,
      braceContent,
      parentMacroContext
    };
  }
}

// Enhanced parser that maintains position information through transformations with filename and brace context support
export class PositionAwareParser {
  private sourceMap: SourceMapImpl;
  private originalSource: string;
  
  constructor(source: string, filename?: string, braceContext?: BraceContext) {
    this.originalSource = source;
    this.sourceMap = new SourceMapImpl(source, filename, braceContext);
  }
  
  // Remove comments while preserving position mapping
  removeComments(source: string): { result: string; sourceMap: SourceMapImpl } {
    let result = '';
    let originalLine = 1;
    let originalColumn = 1;
    let resultPosition = 0;
    let i = 0;
    const length = source.length;
    
    while (i < length) {
      const char = source[i];
      const startPos = SourceMapImpl.createPosition(
        originalLine, 
        originalColumn, 
        1, 
        char,
        this.sourceMap.filename  // Include filename in positions
      );
      
      // Handle newlines for position tracking
      if (char === '\n') {
        result += char;
        this.sourceMap.addMapping(resultPosition, startPos);
        resultPosition++;
        originalLine++;
        originalColumn = 1;
        i++;
        continue;
      }
      
      // Handle escape sequences
      if (char === '\\' && i + 1 < length) {
        const escapeSeq = source.substring(i, i + 2);
        result += escapeSeq;
        this.sourceMap.addMapping(resultPosition, 
          SourceMapImpl.createPosition(
            originalLine, 
            originalColumn, 
            2, 
            escapeSeq,
            this.sourceMap.filename
          ));
        resultPosition += 2;
        originalColumn += 2;
        i += 2;
        continue;
      }
      
      // Handle quoted strings - skip comment processing inside quotes
      if (char === '"') {
        result += char;
        this.sourceMap.addMapping(resultPosition, startPos);
        resultPosition++;
        originalColumn++;
        i++;
        
        // Find the end of the quoted string
        while (i < length) {
          const quoteChar = source[i];
          const quotePos = SourceMapImpl.createPosition(
            originalLine, 
            originalColumn, 
            1, 
            quoteChar,
            this.sourceMap.filename
          );
          
          if (quoteChar === '\n') {
            originalLine++;
            originalColumn = 1;
          } else {
            originalColumn++;
          }
          
          result += quoteChar;
          this.sourceMap.addMapping(resultPosition, quotePos);
          resultPosition++;
          
          if (quoteChar === '\\' && i + 1 < length) {
            // Handle escaped characters in quotes
            const nextChar = source[i + 1];
            result += nextChar;
            this.sourceMap.addMapping(resultPosition, 
              SourceMapImpl.createPosition(
                originalLine, 
                originalColumn, 
                1, 
                nextChar,
                this.sourceMap.filename
              ));
            resultPosition++;
            if (nextChar === '\n') {
              originalLine++;
              originalColumn = 1;
            } else {
              originalColumn++;
            }
            i += 2;
          } else if (quoteChar === '"') {
            i++;
            break;
          } else {
            i++;
          }
        }
        continue;
      }
      
      // Handle single quoted strings
      if (char === "'") {
        result += char;
        this.sourceMap.addMapping(resultPosition, startPos);
        resultPosition++;
        originalColumn++;
        i++;
        
        // Find the end of the quoted string
        while (i < length) {
          const quoteChar = source[i];
          const quotePos = SourceMapImpl.createPosition(
            originalLine, 
            originalColumn, 
            1, 
            quoteChar,
            this.sourceMap.filename
          );
          
          if (quoteChar === '\n') {
            originalLine++;
            originalColumn = 1;
          } else {
            originalColumn++;
          }
          
          result += quoteChar;
          this.sourceMap.addMapping(resultPosition, quotePos);
          resultPosition++;
          
          if (quoteChar === '\\' && i + 1 < length) {
            // Handle escaped characters in quotes
            const nextChar = source[i + 1];
            result += nextChar;
            this.sourceMap.addMapping(resultPosition, 
              SourceMapImpl.createPosition(
                originalLine, 
                originalColumn, 
                1, 
                nextChar,
                this.sourceMap.filename
              ));
            resultPosition++;
            if (nextChar === '\n') {
              originalLine++;
              originalColumn = 1;
            } else {
              originalColumn++;
            }
            i += 2;
          } else if (quoteChar === "'") {
            i++;
            break;
          } else {
            i++;
          }
        }
        continue;
      }
      
      // Handle comments starting with #
      if (char === '#') {
        // Check for block comments #( ... )# or #{ ... }#
        if (i + 1 < length) {
          const nextChar = source[i + 1];
          
          if (nextChar === '(' || nextChar === '{') {
            // Found block comment start
            const commentStart = i;
            const openBrace = nextChar;
            const closeBrace = openBrace === '(' ? ')' : '}';
            let depth = 1;
            let commentEnd = -1;
            
            // Skip past the opening #( or #{
            let j = i + 2;
            let tempLine = originalLine;
            let tempColumn = originalColumn + 2;
            
            // Find the matching closing }# or )#
            while (j < length && depth > 0) {
              const c = source[j];
              
              if (c === '\n') {
                tempLine++;
                tempColumn = 1;
                j++;
                continue;
              }
              
              // Handle escape sequences
              if (c === '\\' && j + 1 < length) {
                j += 2;
                tempColumn += 2;
                continue;
              }
              
              // Handle double quoted strings (but not single quotes - they're just text in comments)
              if (c === '"') {
                j++;
                tempColumn++;
                // Skip to end of quoted string
                while (j < length && source[j] !== '"') {
                  if (source[j] === '\\' && j + 1 < length) {
                    j += 2;
                    tempColumn += 2;
                  } else {
                    if (source[j] === '\n') {
                      tempLine++;
                      tempColumn = 1;
                    } else {
                      tempColumn++;
                    }
                    j++;
                  }
                }
                if (j < length && source[j] === '"') {
                  j++;
                  tempColumn++;
                }
                continue;
              }
              
              // Check for nested comment start
              if (c === '#' && j + 1 < length && source[j + 1] === openBrace) {
                depth++;
                j += 2;
                tempColumn += 2;
                continue;
              }
              
              // Check for comment end
              if (c === closeBrace && j + 1 < length && source[j + 1] === '#') {
                depth--;
                if (depth === 0) {
                  commentEnd = j + 2; // Position after the closing #
                  break;
                }
                j += 2;
                tempColumn += 2;
                continue;
              }
              
              j++;
              tempColumn++;
            }
            
            if (commentEnd !== -1) {
              // Successfully found and skipped the block comment
              i = commentEnd;
              originalLine = tempLine;
              originalColumn = tempColumn;
              continue;
            } else {
              // Unclosed block comment - treat # as regular character
              result += char;
              this.sourceMap.addMapping(resultPosition, startPos);
              resultPosition++;
              originalColumn++;
              i++;
              continue;
            }
          }
        }
        
        // Check for line comments - # followed by whitespace or end of line
        const isAtStart = originalColumn === 1;
        const isPrecededByWhitespace = i > 0 && /\s/.test(source[i - 1]);
        const isValidCommentStart = isAtStart || isPrecededByWhitespace;
        
        if (isValidCommentStart) {
          const isFollowedByWhitespaceOrEnd = i + 1 >= length || /\s/.test(source[i + 1]);
          
          if (isFollowedByWhitespaceOrEnd) {
            // This is a line comment - skip to end of line
            while (i < length && source[i] !== '\n') {
              i++;
              originalColumn++;
            }
            // Don't increment here - the newline will be handled in the next iteration
            continue;
          }
        }
      }
      
      // Regular character - add to result with position mapping
      result += char;
      this.sourceMap.addMapping(resultPosition, startPos);
      resultPosition++;
      originalColumn++;
      i++;
    }
    
    return { result, sourceMap: this.sourceMap };
  }
  
  getSourceMap(): SourceMapImpl {
    return this.sourceMap;
  }
}
