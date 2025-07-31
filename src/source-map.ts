import { SourcePosition, SourceMap } from './types';

export class SourceMapImpl implements SourceMap {
  public originalLines: string[];
  public transformedToOriginal: Map<number, SourcePosition>;
  
  constructor(originalSource: string) {
    this.originalLines = originalSource.split('\n');
    this.transformedToOriginal = new Map();
  }
  
  addMapping(transformedPos: number, originalPos: SourcePosition): void {
    this.transformedToOriginal.set(transformedPos, originalPos);
  }
  
  getOriginalPosition(transformedPos: number): SourcePosition | null {
    return this.transformedToOriginal.get(transformedPos) || null;
  }
  
  // Get context lines around a position
  getContext(position: SourcePosition, contextLines: number = 2): string[] {
    const start = Math.max(0, position.line - contextLines - 1);
    const end = Math.min(this.originalLines.length, position.line + contextLines);
    return this.originalLines.slice(start, end);
  }
  
  // Create a position object
  static createPosition(line: number, column: number, length: number, originalText: string): SourcePosition {
    return {
      line,
      column,
      length,
      originalText
    };
  }
}

// Parser that maintains position information through transformations
export class PositionAwareParser {
  private sourceMap: SourceMapImpl;
  private originalSource: string;
  
  constructor(source: string) {
    this.originalSource = source;
    this.sourceMap = new SourceMapImpl(source);
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
      const startPos = SourceMapImpl.createPosition(originalLine, originalColumn, 1, char);
      
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
          SourceMapImpl.createPosition(originalLine, originalColumn, 2, escapeSeq));
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
          const quotePos = SourceMapImpl.createPosition(originalLine, originalColumn, 1, quoteChar);
          
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
              SourceMapImpl.createPosition(originalLine, originalColumn, 1, nextChar));
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
          const quotePos = SourceMapImpl.createPosition(originalLine, originalColumn, 1, quoteChar);
          
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
              SourceMapImpl.createPosition(originalLine, originalColumn, 1, nextChar));
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
  
  private skipBlockComment(
    str: string, 
    startIndex: number, 
    closeChar: string,
    startLine: number,
    startColumn: number
  ): { newIndex: number; newLine: number; newColumn: number } {
    let i = startIndex + 2; // Skip the #( or #{
    let depth = 1;
    let line = startLine;
    let column = startColumn + 2;
    const openChar = closeChar === ')' ? '(' : '{';
    
    while (i < str.length && depth > 0) {
      const char = str[i];
      
      if (char === '\n') {
        line++;
        column = 1;
        i++;
        continue;
      }
      
      // Handle escape sequences within comments
      if (char === '\\' && i + 1 < str.length) {
        i += 2;
        column += 2;
        continue;
      }
      
      // Handle ONLY double quoted strings within comments
      // Single quotes are treated as regular text to allow contractions like "don't", "can't"
      if (char === '"') {
        i++;
        column++;
        // Skip until end of quoted string
        while (i < str.length) {
          const quoteChar = str[i];
          if (quoteChar === '\n') {
            line++;
            column = 1;
          } else {
            column++;
          }
          
          if (quoteChar === '\\' && i + 1 < str.length) {
            i += 2;
            if (str[i - 1] === '\n') {
              line++;
              column = 1;
            } else {
              column++;
            }
          } else if (quoteChar === '"') {
            i++;
            break;
          } else {
            i++;
          }
        }
        continue;
      }
      
      // Check for nested comment start
      if (char === '#' && i + 1 < str.length && str[i + 1] === openChar) {
        depth++;
        i += 2;
        column += 2;
        continue;
      }
      
      // Check for comment end
      if (char === closeChar && i + 1 < str.length && str[i + 1] === '#') {
        depth--;
        i += 2;
        column += 2;
        
        // If we've closed all nested comments, we're done
        if (depth === 0) {
          break;
        }
        continue;
      }
      
      i++;
      column++;
    }
    
    return { newIndex: i, newLine: line, newColumn: column };
  }
  
  getSourceMap(): SourceMapImpl {
    return this.sourceMap;
  }
}
