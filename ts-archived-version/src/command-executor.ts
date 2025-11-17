import { Logger } from './logger';
import { ExecutionState } from './execution-state';
import { SourceMapImpl, PositionAwareParser } from './source-map';
import { 
  PawScriptHandler, 
  TokenData, 
  CommandSequence, 
  SubstitutionContext,
  SourcePosition,
  ParsedCommand,
  PawScriptError,
  ParsingContext
} from './types';

type ParsedCommandWithSeparator = ParsedCommand & {
  separator: 'none' | ';' | '&' | '|';
};

export class CommandExecutor {
  private commands = new Map<string, PawScriptHandler>();
  private activeTokens = new Map<string, TokenData>();
  private nextTokenId = 1;
  private logger: Logger;
  private fallbackHandler: ((cmdName: string, args: any[], executionState?: any, position?: SourcePosition) => any) | null = null;
  
  constructor(logger: Logger) {
    this.logger = logger;
  }
  
  registerCommand(name: string, handler: PawScriptHandler): void {
    this.commands.set(name, handler);
    this.logger.debug(`Registered command: ${name}`);
  }
  
  registerCommands(commands: Record<string, PawScriptHandler>): void {
    for (const [name, handler] of Object.entries(commands)) {
      this.registerCommand(name, handler);
    }
  }
  
  unregisterCommand(name: string): boolean {
    if (this.commands.has(name)) {
      this.commands.delete(name);
      this.logger.debug(`Unregistered command: ${name}`);
      return true;
    }
    this.logger.warn(`Attempted to unregister unknown command: ${name}`);
    return false;
  }

  setFallbackHandler(handler: (cmdName: string, args: any[], executionState?: any, position?: SourcePosition) => any): void {
    this.fallbackHandler = handler;
  }

  requestCompletionToken(
    cleanupCallback: ((tokenId: string) => void) | null = null, 
    parentTokenId: string | null = null, 
    timeoutMs: number = 300000,
    executionState?: ExecutionState,
    position?: SourcePosition
  ): string {
    const tokenId = `token_${this.nextTokenId++}`;
    
    const timeoutId = setTimeout(() => {
      this.logger.warn(`Token ${tokenId} timed out, forcing cleanup`);
      this.forceCleanupToken(tokenId);
    }, timeoutMs);
    
    this.activeTokens.set(tokenId, {
      commandSequence: null,
      parentToken: parentTokenId,
      children: new Set(),
      cleanupCallback: cleanupCallback,
      timeoutId: timeoutId,
      chainedToken: null,
      timestamp: Date.now(),
      executionState: executionState,
      suspendedResult: executionState?.getResult(),
      hasSuspendedResult: executionState?.hasResultValue(),
      position: position
    });
    
    if (parentTokenId && this.activeTokens.has(parentTokenId)) {
      this.activeTokens.get(parentTokenId)!.children.add(tokenId);
    }

    this.logger.debug(`Created completion token: ${tokenId}, parent: ${parentTokenId || 'none'}, hasResult: ${executionState?.hasResultValue()}`);
    return tokenId;
  }
  
  getTokenStatus(): any {
    return {
      activeCount: this.activeTokens.size,
      tokens: Array.from(this.activeTokens.entries()).map(([id, data]) => ({
        id,
        parentToken: data.parentToken,
        childCount: data.children.size,
        hasCommandSequence: !!data.commandSequence,
        age: Date.now() - data.timestamp,
        hasSuspendedResult: data.hasSuspendedResult,
        position: data.position
      }))
    };
  }
  
  pushCommandSequence(
    tokenId: string, 
    type: 'sequence' | 'conditional' | 'or', 
    remainingCommands: ParsedCommand[], 
    currentIndex: number, 
    originalCommand: string,
    executionState?: ExecutionState,
    position?: SourcePosition
  ): void {
    if (!this.activeTokens.has(tokenId)) {
      throw new Error(`Invalid completion token: ${tokenId}`);
    }
    
    const tokenData = this.activeTokens.get(tokenId)!;
    const stateSnapshot = executionState ? executionState.getSnapshot() : { result: undefined, hasResult: false };
    
    tokenData.commandSequence = {
      type,
      remainingCommands: [...remainingCommands],
      currentIndex,
      totalCommands: remainingCommands.length + currentIndex,
      originalCommand,
      timestamp: Date.now(),
      inheritedResult: stateSnapshot.result,
      hasInheritedResult: stateSnapshot.hasResult,
      position: position
    };
    
    this.logger.debug(`Pushed command sequence onto token ${tokenId}. Type: ${type}, Remaining: ${remainingCommands.length}, hasResult: ${stateSnapshot.hasResult}`);
  }

  popAndResumeCommandSequence(tokenId: string, result: boolean, finalResult?: any, hasFinalResult?: boolean): boolean {
    if (!this.activeTokens.has(tokenId)) {
      this.logger.warn(`Attempted to resume with invalid token: ${tokenId}`);
      return false;
    }
    
    const tokenData = this.activeTokens.get(tokenId)!;
    
    this.logger.debug(`Popping command sequence from token ${tokenId}. Type: ${tokenData.commandSequence?.type}, Result: ${result}, hasFinalResult: ${hasFinalResult}`);
    
    this.cleanupTokenChildren(tokenId);
    
    if (tokenData.timeoutId) {
      clearTimeout(tokenData.timeoutId);
    }
    
    const executionState = tokenData.executionState || new ExecutionState();
    
    let success = result;
    if (tokenData.commandSequence) {
      success = this.resumeCommandSequence(tokenData.commandSequence, result, executionState);
    }
    
    const chainedToken = tokenData.chainedToken;
    
    this.activeTokens.delete(tokenId);
    
    if (tokenData.parentToken && this.activeTokens.has(tokenData.parentToken)) {
      this.activeTokens.get(tokenData.parentToken)!.children.delete(tokenId);
    }
    
    if (chainedToken && this.activeTokens.has(chainedToken)) {
      this.logger.debug(`Triggering chained token ${chainedToken} with result ${success}`);
      return this.popAndResumeCommandSequence(chainedToken, success, executionState.getResult(), executionState.hasResultValue());
    }
    
    return success;
  }
  
  private cleanupTokenChildren(tokenId: string): void {
    const tokenData = this.activeTokens.get(tokenId);
    if (!tokenData) return;
    
    for (const childTokenId of tokenData.children) {
      this.forceCleanupToken(childTokenId);
    }
  }
  
  forceCleanupToken(tokenId: string): void {
    const tokenData = this.activeTokens.get(tokenId);
    if (!tokenData) return;
    
    this.logger.debug(`Force cleaning up token: ${tokenId}`);
    
    if (tokenData.cleanupCallback) {
      try {
        tokenData.cleanupCallback(tokenId);
      } catch (error) {
        this.logger.error(`Error in cleanup callback for token ${tokenId}:`, error);
      }
    }
    
    if (tokenData.timeoutId) {
      clearTimeout(tokenData.timeoutId);
    }
    
    this.cleanupTokenChildren(tokenId);
    this.activeTokens.delete(tokenId);
  }
  
  private resumeCommandSequence(commandSequence: CommandSequence, result: boolean, executionState: ExecutionState): boolean {
    switch (commandSequence.type) {
      case 'sequence':
        return this.resumeSequence(commandSequence, result, executionState);
      case 'conditional':
        return this.resumeConditional(commandSequence, result, executionState);
      case 'or':
        return this.resumeOr(commandSequence, result, executionState);
      default:
        this.logger.error(`Unknown command sequence type: ${commandSequence.type}`);
        return false;
    }
  }
  
  private resumeSequence(sequence: CommandSequence, result: boolean, executionState: ExecutionState): boolean {
    let success = result;
    
    for (const parsedCmd of sequence.remainingCommands) {
      if (!parsedCmd.command.trim()) continue;
      
      const cmdResult = this.executeParsedCommand(parsedCmd, executionState);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
    }
    
    return success;
  }

  private resumeConditional(sequence: CommandSequence, result: boolean, executionState: ExecutionState): boolean {
    if (!result) {
      return false;
    }
    
    let success: boolean = result;
    
    for (const parsedCmd of sequence.remainingCommands) {
      if (!parsedCmd.command.trim()) continue;
      
      const cmdResult = this.executeParsedCommand(parsedCmd, executionState);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
      if (!success) break;
    }
    
    return success;
  }
  
  private resumeOr(sequence: CommandSequence, result: boolean, executionState: ExecutionState): boolean {
    if (result) {
      return true;
    }
    
    let success = false;
    
    for (const parsedCmd of sequence.remainingCommands) {
      if (!parsedCmd.command.trim()) continue;
      
      const cmdResult = this.executeParsedCommand(parsedCmd, executionState);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
      if (success) break;
    }
    
    return success;
  }
  
  execute(commandStr: string, ...args: any[]): boolean | string {
    this.logger.debug(`execute called with command: ${commandStr}, args: ${JSON.stringify(args)}`);
    
    const executionState = new ExecutionState();
    
    if (args.length > 0) {
      const handler = this.commands.get(commandStr);
      if (handler) {
        try {
          const result = handler(this.createContext(args, executionState));
          if (typeof result === 'string' && result.startsWith('token_')) {
            this.logger.warn('Command with explicit args returned token - this is not supported');
            return false;
          }
          return result;
        } catch (error) {
          this.logger.commandError(commandStr, error instanceof Error ? error.message : String(error));
          this.executeScriptError(`Error executing command: ${commandStr} - ${error}`);
          return false;
        }
      }
      this.logger.unknownCommandError(commandStr);
      this.executeScriptError(`Unknown command: ${commandStr}`);
      return false;
    }
    
    const substitutionContext = {
      args: [],
      executionState: executionState
    };
    
    return this.executeWithState(commandStr, executionState, substitutionContext);
  }
  
  executeWithState(
    commandStr: string, 
    executionState: ExecutionState, 
    substitutionContext?: SubstitutionContext,
    filename?: string,
    lineOffset: number = 0,     // NEW: Add this many lines to all positions
    columnOffset: number = 0    // NEW: Add this many columns to first-line positions
  ): boolean | string {
    // DEBUG LOGGING
    if (lineOffset > 0 || columnOffset > 0) {
      console.error(`[DEBUG] executeWithState: offsets line=${lineOffset}, column=${columnOffset}`);
      console.error(`[DEBUG] Command: "${commandStr}"`);
      console.error(`[DEBUG] Filename: ${filename || '<none>'}`);
    }
    
    try {
      // Parse with position tracking and filename
      const parser = new PositionAwareParser(commandStr, filename);
      const { result: cleanedCommand, sourceMap } = parser.removeComments(commandStr);
      
      // APPLY OFFSETS: Adjust all positions in the source map
      if (lineOffset > 0 || columnOffset > 0) {
        console.error(`[DEBUG] About to apply position offsets to source map`);
        this.applyPositionOffsets(sourceMap, lineOffset, columnOffset);
      }
      
      // Parse into commands
      const parsedCommands = this.parseCommandSequence(cleanedCommand, sourceMap);
      
      // Check for parse errors - empty result indicates parse failure
      if (parsedCommands === null) {
        return false; // Parse error already logged
      }
      
      if (parsedCommands.length === 0) {
        return true; // Empty command is success
      }
      
      if (parsedCommands.length === 1) {
        return this.executeParsedCommand(parsedCommands[0], executionState, substitutionContext);
      }
      
      // Multiple commands - execute as sequence with flow control
      return this.executeCommandSequence(parsedCommands, executionState, substitutionContext);
      
    } catch (error) {
      if (error instanceof Error) {
        const pawError = error as PawScriptError;
        if (pawError.position) {
          this.logger.logError(pawError);
          this.executeScriptError(this.formatPositionError(pawError));
        } else {
          this.logger.error(`Execution error: ${error.message}`);
          this.executeScriptError(`Execution error: ${error.message}`);
        }
      }
      return false;
    }
  }
  
  // NEW METHOD: Apply line/column offsets to all positions in source map
  private applyPositionOffsets(sourceMap: SourceMapImpl, lineOffset: number, columnOffset: number): void {
    // Get all mappings
    const mappings = Array.from(sourceMap.transformedToOriginal.entries());
    
    // DEBUG LOGGING
    console.error(`[DEBUG] applyPositionOffsets: ${mappings.length} mappings, line=${lineOffset}, column=${columnOffset}`);
    
    if (mappings.length === 0) {
      console.error(`[DEBUG] WARNING: No position mappings found in source map!`);
      return;
    }
    
    // Clear the map
    sourceMap.transformedToOriginal.clear();
    
    // Re-add with adjusted positions
    for (const [transformedPos, originalPos] of mappings) {
      const beforeLine = originalPos.line;
      const beforeColumn = originalPos.column;
      
      // FIXED CALCULATION: Handle first line vs subsequent lines correctly
      const adjustedPos: SourcePosition = {
        ...originalPos,
        line: originalPos.line + lineOffset,
        // Column offset only applies to line 1 of the brace content
        column: originalPos.line === 1 
          ? originalPos.column + columnOffset 
          : originalPos.column
      };
      
      console.error(`[DEBUG] Adjusted (${transformedPos}): ${beforeLine}:${beforeColumn} -> ${adjustedPos.line}:${adjustedPos.column}`);
      
      sourceMap.transformedToOriginal.set(transformedPos, adjustedPos);
    }
  }
  
  private parseCommandSequence(commandStr: string, sourceMap: SourceMapImpl): ParsedCommandWithSeparator[] | null {
    const commands: ParsedCommandWithSeparator[] = [];
    let currentCommand = '';
    let nestingDepth = 0;
    let inQuote = false;
    let quoteChar: string | null = null;
    let i = 0;
    let line = 1;
    let column = 1;
    let commandStartLine = 1;
    let commandStartColumn = 1;
    let currentSeparator: 'none' | ';' | '&' | '|' = 'none';
    
    const addCommand = (cmd: string, separator: 'none' | ';' | '&' | '|', endPos: { line: number, column: number }) => {
      const trimmed = cmd.trim();
      if (trimmed) {
        const position = sourceMap.getOriginalPosition(0) || 
          SourceMapImpl.createPosition(
            commandStartLine, 
            commandStartColumn, 
            trimmed.length, 
            trimmed,
            sourceMap.filename
          );
        commands.push({
          command: trimmed,
          arguments: [],
          position,
          originalLine: sourceMap.originalLines[commandStartLine - 1] || '',
          type: 'single',
          separator: separator
        });
      }
      currentCommand = '';
      commandStartLine = endPos.line;
      commandStartColumn = endPos.column;
    };
    
    while (i < commandStr.length) {
      const char = commandStr[i];
      
      // Handle escape sequences
      if (char === '\\' && i + 1 < commandStr.length) {
        currentCommand += char + commandStr[i + 1];
        i += 2;
        column += 2;
        continue;
      }
      
      // Handle quotes
      if ((char === '"' || char === "'") && !inQuote) {
        inQuote = true;
        quoteChar = char;
        currentCommand += char;
        i++;
        column++;
        continue;
      }
      
      if (char === quoteChar && inQuote) {
        inQuote = false;
        quoteChar = null;
        currentCommand += char;
        i++;
        column++;
        continue;
      }
      
      // Skip processing separators inside quotes
      if (inQuote) {
        currentCommand += char;
        if (char === '\n') {
          line++;
          column = 1;
        } else {
          column++;
        }
        i++;
        continue;
      }
      
      // Track nesting depth
      if (char === '(' || char === '{') {
        nestingDepth++;
        currentCommand += char;
        i++;
        column++;
        continue;
      }
      
      if (char === ')' || char === '}') {
        nestingDepth--;
        currentCommand += char;
        i++;
        column++;
        continue;
      }
      
      // Skip processing separators inside nested structures
      if (nestingDepth > 0) {
        currentCommand += char;
        if (char === '\n') {
          line++;
          column = 1;
        } else {
          column++;
        }
        i++;
        continue;
      }
      
      // Handle separators at top level (not in quotes or nested structures)
      
      // Handle semicolon
      if (char === ';') {
        addCommand(currentCommand, currentSeparator, { line, column: column + 1 });
        currentSeparator = ';';
        i++;
        column++;
        continue;
      }
      
      // Handle & operator
      if (char === '&') {
        addCommand(currentCommand, currentSeparator, { line, column: column + 1 });
        currentSeparator = '&';
        i++;
        column++;
        continue;
      }
      
      // Handle | operator  
      if (char === '|') {
        addCommand(currentCommand, currentSeparator, { line, column: column + 1 });
        currentSeparator = '|';
        i++;
        column++;
        continue;
      }
      
      // Handle newlines - act as semicolon separators
      if (char === '\n') {
        // If we have a command and no pending separator, split here
        if (currentCommand.trim()) {
          addCommand(currentCommand, currentSeparator, { line: line + 1, column: 1 });
          currentSeparator = ';'; // Next command has implicit semicolon separator
        }
        
        line++;
        column = 1;
        i++;
        continue;
      }
      
      // Regular character
      currentCommand += char;
      i++;
      column++;
    }
    
    // Check for unclosed quotes - this IS a real parse error
    if (inQuote) {
      this.logger.parseError(
        `Unclosed quote: missing closing ${quoteChar}`,
        SourceMapImpl.createPosition(line, column, 1, currentCommand, sourceMap.filename),
        sourceMap.originalLines
      );
      return null; // Parse error
    }
    
    // Handle final command
    if (currentCommand.trim()) {
      addCommand(currentCommand, currentSeparator, { line, column });
    }
    
    // Leading operators are valid - no validation needed
    return commands;
  }
  
  private executeCommandSequence(commands: ParsedCommandWithSeparator[], executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    let lastResult: boolean = true; // Default to true for leading operators at start of file
    
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.command.trim()) continue;
      
      // Apply flow control based on separator (works for both leading and trailing operators)
      let shouldExecute = true;
      
      if (cmd.separator === '&') {
        // AND: execute only if last command succeeded
        shouldExecute = lastResult;
      } else if (cmd.separator === '|') {
        // OR: execute only if last command failed  
        shouldExecute = !lastResult;
      }
      // For 'none' and ';': always execute
      
      if (!shouldExecute) {
        // Skip this command but continue with the same lastResult
        this.logger.debug(`Skipping command "${cmd.command}" due to flow control (separator: ${cmd.separator}, lastResult: ${lastResult})`);
        continue;
      }
      
      const result = this.executeParsedCommand(cmd, executionState, substitutionContext);

      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command returned token ${result}, setting up sequence continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended sequence for token ${tokenId}`);
            },
            undefined,
            300000,
            executionState,
            cmd.position
          );
          
          // Convert to regular ParsedCommand for token sequence
          const parsedRemaining = remainingCommands.map(cmdWithSep => ({
            command: cmdWithSep.command,
            arguments: cmdWithSep.arguments,
            position: cmdWithSep.position,
            originalLine: cmdWithSep.originalLine,
            type: cmdWithSep.type
          } as ParsedCommand));
          
          this.pushCommandSequence(sequenceToken, 'sequence', parsedRemaining, i + 1, 'sequence', executionState, cmd.position);
          this.chainTokens(result, sequenceToken);
          
          return sequenceToken;
        } else {
          return result;
        }
      }
      
      lastResult = result as boolean;
    }
    
    return lastResult;
  }
  
  private executeParsedCommand(parsedCmd: ParsedCommand, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    try {
      return this.executeSingleCommand(parsedCmd.command, executionState, substitutionContext, parsedCmd.position);
    } catch (error) {
      if (error instanceof Error) {
        this.logger.commandError(parsedCmd.command, error.message, parsedCmd.position, [parsedCmd.originalLine]);
        this.executeScriptError(this.formatPositionError({
          message: `Error executing command '${parsedCmd.command}': ${error.message}`,
          position: parsedCmd.position,
          context: [parsedCmd.originalLine]
        } as PawScriptError));
      }
      return false;
    }
  }
  
  private chainTokens(firstToken: string, secondToken: string): void {
    const firstTokenData = this.activeTokens.get(firstToken);
    const secondTokenData = this.activeTokens.get(secondToken);
    
    if (!firstTokenData || !secondTokenData) {
      this.logger.error(`Cannot chain tokens: ${firstToken} or ${secondToken} not found`);
      return;
    }
    
    firstTokenData.chainedToken = secondToken;
    secondTokenData.parentToken = firstToken;
    
    this.logger.debug(`Chained token ${secondToken} to complete after ${firstToken}`);
  }
  
  private createContext(args: any[], executionState: ExecutionState, position?: SourcePosition): any {
    return {
      args: args,
      position: position,
      requestToken: (cleanup?: (tokenId: string) => void) => {
        return this.requestCompletionToken(cleanup, undefined, 300000, executionState, position);
      },
      resumeToken: (tokenId: string, result: boolean) => {
        return this.popAndResumeCommandSequence(tokenId, result, executionState.getResult(), executionState.hasResultValue());
      },
      setResult: (value: any) => executionState.setResult(value),
      getResult: () => executionState.getResult(),
      hasResult: () => executionState.hasResultValue(),
      clearResult: () => executionState.clearResult()
    };
  }

  private applySyntacticSugar(commandStr: string): string {
    const spaceIndex = commandStr.indexOf(' ');
    if (spaceIndex === -1) {
      return commandStr;
    }
    
    const commandPart = commandStr.substring(0, spaceIndex);
    const argsPart = commandStr.substring(spaceIndex + 1);
    
    const identifierParenMatch = argsPart.match(/^([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.+?)\)(.*)$/s);
    
    if (identifierParenMatch) {
      const identifier = identifierParenMatch[1];
      const content = identifierParenMatch[2];
      
      return `${commandPart} '${identifier}', (${content})`;
    }
    
    return commandStr;
  }
  
  private executeSingleCommand(commandStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext, position?: SourcePosition): boolean | string {
    commandStr = commandStr.trim();

    commandStr = this.applySyntacticSugar(commandStr);
    
    this.logger.debug(`executeSingleCommand called with: "${commandStr}"`);
    
    if (substitutionContext) {
      commandStr = this.applySubstitution(commandStr, substitutionContext);
      this.logger.debug(`After substitution: "${commandStr}"`);
    }
    
    const parsed = this.parseCommand(commandStr, substitutionContext);
    const cmdName = parsed.command;
    const args = parsed.arguments;
    
    this.logger.debug(`Parsed as - Command: "${cmdName}", Args: ${JSON.stringify(args)}`);
    
    let handler = this.commands.get(cmdName);
    
    if (!handler && this.fallbackHandler) {
      this.logger.debug(`Command "${cmdName}" not found, trying fallback handler`);
      const fallbackResult = this.fallbackHandler(cmdName, args, executionState, position);
      if (fallbackResult !== null) {
        this.logger.debug(`Fallback handler returned: ${fallbackResult}`);
        return fallbackResult;
      }
    }
    
    if (!handler) {
      // DEBUG LOGGING for unknown command
      console.error(`[DEBUG] Unknown command "${cmdName}" at position:`, position);
      
      this.logger.unknownCommandError(cmdName, position);
      this.executeScriptError(`Unknown command: ${cmdName}${position ? this.formatPosition(position) : ''}`);
      return false;
    }
    
    try {
      this.logger.debug(`Executing ${cmdName} with args: ${JSON.stringify(args)}`);
      return handler(this.createContext(args, executionState, position));
    } catch (error) {
      this.logger.commandError(cmdName, error instanceof Error ? error.message : String(error), position);
      this.executeScriptError(`Error executing command: ${cmdName} - ${error}${position ? this.formatPosition(position) : ''}`);
      return false;
    }
  }
  
  private parseCommand(commandStr: string, substitutionContext?: SubstitutionContext): { command: string; arguments: any[] } {
    if (!commandStr.trim()) {
      return { command: '', arguments: [] };
    }
    
    let commandEnd = -1;
    let inQuote = false;
    let quoteChar: string | null = null;
    
    for (let i = 0; i < commandStr.length; i++) {
      const char = commandStr[i];
      
      if (char === '\\' && i + 1 < commandStr.length) {
        i++;
        continue;
      }
      
      if ((char === '"' || char === "'") && !inQuote) {
        inQuote = true;
        quoteChar = char;
        continue;
      }
      
      if (char === quoteChar && inQuote) {
        inQuote = false;
        quoteChar = null;
        continue;
      }
      
      if (!inQuote && (char === ' ' || char === '\t')) {
        commandEnd = i;
        break;
      }
    }
    
    if (commandEnd === -1) {
      const substitutedCommand = this.applySubstitution(commandStr, substitutionContext);
      return { command: substitutedCommand, arguments: [] };
    }
    
    const command = this.applySubstitution(commandStr.substring(0, commandEnd), substitutionContext);
    const argsStr = commandStr.substring(commandEnd);
    
    if (!argsStr) {
      return { command, arguments: [] };
    }
    
    const args = this.parseArguments(argsStr, substitutionContext);
    
    this.logger.debug(`Parsed command: "${command}" with args: ${JSON.stringify(args)}`);
    
    return { command, arguments: args };
  }
  
  private parseArguments(argsStr: string, substitutionContext?: SubstitutionContext): any[] {
    const args: any[] = [];
    let currentArg = '';
    let inQuote = false;
    let quoteChar: string | null = null;
    let parenCount = 0;
    let braceCount = 0;
    let i = 0;
    
    while (i < argsStr.length) {
      const char = argsStr[i];
      
      if (char === '\\' && i + 1 < argsStr.length) {
        currentArg += char + argsStr[i + 1];
        i += 2;
        continue;
      }
      
      if ((char === '"' || char === "'") && !inQuote) {
        inQuote = true;
        quoteChar = char;
        currentArg += char;
        i++;
        continue;
      }
      
      if (char === quoteChar && inQuote) {
        inQuote = false;
        quoteChar = null;
        currentArg += char;
        i++;
        continue;
      }
      
      if (!inQuote && char === '(') {
        parenCount++;
        currentArg += char;
        i++;
        continue;
      }
      
      if (!inQuote && char === ')') {
        parenCount--;
        currentArg += char;
        i++;
        continue;
      }
      
      if (!inQuote && char === '{') {
        braceCount++;
        currentArg += char;
        i++;
        continue;
      }
      
      if (!inQuote && char === '}') {
        braceCount--;
        currentArg += char;
        i++;
        continue;
      }
      
      if (!inQuote && parenCount === 0 && braceCount === 0 && char === ',') {
        args.push(this.parseArgumentValue(currentArg.trim(), substitutionContext));
        currentArg = '';
        
        while (i + 1 < argsStr.length && (argsStr[i + 1] === ' ' || argsStr[i + 1] === '\t')) {
          i++;
        }
        
        i++;
        continue;
      }
      
      currentArg += char;
      i++;
    }
    
    if (currentArg.trim() || args.length > 0) {
      args.push(this.parseArgumentValue(currentArg.trim(), substitutionContext));
    }
    
    return args;
  }
  
  private parseArgumentValue(argStr: string, substitutionContext?: SubstitutionContext): any {
    if (!argStr) {
      return null;
    }
    
    argStr = this.applySubstitution(argStr, substitutionContext);
    
    if (argStr.startsWith('(') && argStr.endsWith(')')) {
      return argStr.substring(1, argStr.length - 1);
    }
    
    if ((argStr.startsWith('"') && argStr.endsWith('"')) ||
        (argStr.startsWith("'") && argStr.endsWith("'"))) {
      return this.parseStringLiteral(argStr.substring(1, argStr.length - 1));
    }
    
    if (argStr === 'true') return true;
    if (argStr === 'false') return false;
    
    if (/^-?\d+(\.\d+)?$/.test(argStr)) {
      return Number(argStr);
    }
    
    return argStr;
  }
  
  private applySubstitution(str: string, substitutionContext?: SubstitutionContext): string {
    if (!substitutionContext) {
      return str;
    }
    
    let result = str;
    
    result = this.substituteBraceExpressions(result, substitutionContext);
    
    if (substitutionContext.args.length > 0) {
      const allArgs = substitutionContext.args.map(arg => this.formatArgumentForSubstitution(arg)).join(', ');
      result = result.replace(/\$\*/g, allArgs);
    } else {
      result = result.replace(/\$\*/g, '');
    }
    
    result = result.replace(/\$#/g, substitutionContext.args.length.toString());
    
    result = result.replace(/\$(\d+)/g, (match, indexStr) => {
      const index = parseInt(indexStr, 10) - 1;
      if (index >= 0 && index < substitutionContext.args.length) {
        return this.formatArgumentForSubstitution(substitutionContext.args[index]);
      }
      return match;
    });
    
    return result;
  }
  
  // REWRITTEN: substituteBraceExpressions with proper offset handling
  private substituteBraceExpressions(str: string, substitutionContext: SubstitutionContext): string {
    let result = str;
    let modified = true;
    
    while (modified) {
      modified = false;
      
      let braceStart = -1;
      let braceEnd = -1;
      let braceDepth = 0;
      
      // Track position in the current string (which may already be offset)
      let line = 1;
      let column = 1;
      let braceStartLine = 1;
      let braceStartColumn = 1;
      
      for (let i = 0; i < result.length; i++) {
        const char = result[i];
        
        // Update position tracking
        if (char === '\n') {
          line++;
          column = 1;
        } else {
          column++;
        }
        
        if (char === '{') {
          if (braceDepth === 0) {
            braceStart = i;
            braceStartLine = line;
            braceStartColumn = column;
          }
          braceDepth++;
        } else if (char === '}') {
          braceDepth--;
          if (braceDepth === 0 && braceStart !== -1) {
            braceEnd = i;
            break;
          }
        }
      }
      
      if (braceStart !== -1 && braceEnd !== -1) {
        const beforeBrace = result.substring(0, braceStart);
        const braceContent = result.substring(braceStart + 1, braceEnd);
        const afterBrace = result.substring(braceEnd + 1);
        
        // DEBUG LOGGING for brace detection
        console.error(`[DEBUG] Brace found: line=${braceStartLine}, column=${braceStartColumn}`);
        console.error(`[DEBUG] Brace content: "{${braceContent}}"`);
        
        try {
          const childState = substitutionContext.executionState.createChildState();
          
          // CALCULATE ACCUMULATED OFFSETS: Add current position to any existing offsets
          // Get current offsets from substitution context if we're in a nested brace
          const currentLineOffset = substitutionContext.currentLineOffset || 0;
          const currentColumnOffset = substitutionContext.currentColumnOffset || 0;
          
          // Calculate new accumulated offsets
          const newLineOffset = currentLineOffset + (braceStartLine - 1);
          const newColumnOffset = (braceStartLine === 1 
            ? currentColumnOffset + braceStartColumn  // Same line: add columns
            : braceStartColumn) - 1;                       // New line: reset to brace column
          
          console.error(`[DEBUG] Current offsets: line=${currentLineOffset}, column=${currentColumnOffset}`);
          console.error(`[DEBUG] Brace position: line=${braceStartLine}, column=${braceStartColumn}`);
          console.error(`[DEBUG] New accumulated offsets: line=${newLineOffset}, column=${newColumnOffset}`);
          
          const childSubstitutionContext: SubstitutionContext = {
            ...substitutionContext,
            executionState: childState,
            parentContext: substitutionContext,
            // PASS ACCUMULATED OFFSETS: Store in context for nested braces
            currentLineOffset: newLineOffset,
            currentColumnOffset: newColumnOffset
          };
          
          const executeResult = this.executeWithState(
            braceContent, 
            childState, 
            childSubstitutionContext,
            substitutionContext.macroContext?.definitionFile,
            newLineOffset,    // Pass accumulated line offset
            newColumnOffset   // Pass accumulated column offset
          );
          
          let executionValue = '';
          if (childState.hasResultValue()) {
            executionValue = String(childState.getResult());
          } else if (typeof executeResult === 'boolean') {
            executionValue = executeResult.toString();
          } else if (typeof executeResult === 'string' && !executeResult.startsWith('token_')) {
            executionValue = executeResult;
          }
          
          const assembledToken = beforeBrace + executionValue + afterBrace;
          result = this.reEvaluateToken(assembledToken, substitutionContext);
          modified = true;
          
        } catch (error) {
          // Error with proper accumulated position
          const actualLine = (substitutionContext.currentLineOffset || 0) + braceStartLine;
          const actualColumn = braceStartLine === 1 
            ? (substitutionContext.currentColumnOffset || 0) + braceStartColumn
            : braceStartColumn;
          
          const bracePosition: SourcePosition = {
            line: actualLine,
            column: actualColumn,
            length: braceContent.length + 2,
            originalText: `{${braceContent}}`,
            filename: substitutionContext.macroContext?.definitionFile,
            macroContext: substitutionContext.macroContext
          };
          
          this.logger.parseError(
            `Error evaluating brace expression {${braceContent}}: ${error}`,
            bracePosition
          );
          break;
        }
      }
    }
    
    return result;
  }
  
  private reEvaluateToken(token: string, substitutionContext: SubstitutionContext): string {
    let result = token;
    
    if (substitutionContext.args.length > 0) {
      const allArgs = substitutionContext.args.map(arg => this.formatArgumentForSubstitution(arg)).join(', ');
      result = result.replace(/\$\*/g, allArgs);
    } else {
      result = result.replace(/\$\*/g, '');
    }
    
    result = result.replace(/\$#/g, substitutionContext.args.length.toString());
    
    result = result.replace(/\$(\d+)/g, (match, indexStr) => {
      const index = parseInt(indexStr, 10) - 1;
      if (index >= 0 && index < substitutionContext.args.length) {
        return this.formatArgumentForSubstitution(substitutionContext.args[index]);
      }
      return match;
    });
    
    return result;
  }
  
  private formatArgumentForSubstitution(arg: any): string {
    if (typeof arg === 'string') {
      if (arg.includes(' ') || arg.includes(';') || arg.includes('&') || arg.includes('|') || arg.includes(',')) {
        return `'${arg.replace(/'/g, "\\'")}'`;
      }
      return arg;
    } else if (typeof arg === 'number' || typeof arg === 'boolean') {
      return String(arg);
    } else {
      return `'${JSON.stringify(arg).replace(/'/g, "\\'")}'`;
    }
  }
  
  private parseStringLiteral(str: string): string {
    return str
      .replace(/\\'/g, "'")
      .replace(/\\"/g, '"')
      .replace(/\\n/g, '\n')
      .replace(/\\r/g, '\r')
      .replace(/\\t/g, '\t')
      .replace(/\\\\/g, '\\');
  }

  private executeScriptError(message: string): void {
    const handler = this.commands.get('script_error');
    if (handler) {
      try {
        handler(this.createContext([message], new ExecutionState()));
      } catch (error) {
        console.error(`[SCRIPT ERROR] ${message}`);
      }
    } else {
      console.error(`[SCRIPT ERROR] ${message}`);
    }
  }

  private formatPosition(position: SourcePosition): string {
    return ` at line ${position.line}, column ${position.column}`;
  }

  private formatPositionError(error: PawScriptError): string {
    let message = error.message;
    
    if (error.position) {
      message += ` at line ${error.position.line}, column ${error.position.column}`;
      
      if (error.context && error.context.length > 0) {
        message += '\n';
        
        // Show context lines with highlighting
        const contextStart = Math.max(0, error.position.line - 2);
        const contextEnd = Math.min(error.context.length, error.position.line + 1);
        
        for (let i = contextStart; i < contextEnd; i++) {
          const lineNum = i + 1;
          const isErrorLine = lineNum === error.position.line;
          const prefix = isErrorLine ? '>' : ' ';
          const lineNumStr = lineNum.toString().padStart(3);
          
          message += `\n  ${prefix} ${lineNumStr} | ${error.context[i]}`;
          
          if (isErrorLine && error.position.column > 0) {
            // Add caret indicator
            const indent = '      | ' + ' '.repeat(error.position.column - 1);
            const caret = '^'.repeat(Math.max(1, error.position.length));
            message += `\n  ${indent}${caret}`;
          }
        }
      }
    }
    
    return message;
  }
}
