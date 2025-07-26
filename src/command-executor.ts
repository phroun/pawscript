import { Logger } from './logger';
import { ExecutionState } from './execution-state';
import { PawScriptHandler, TokenData, CommandSequence, IPawScriptHost, SubstitutionContext } from './types';

export class CommandExecutor {
  private commands = new Map<string, PawScriptHandler>();
  private activeTokens = new Map<string, TokenData>();
  private nextTokenId = 1;
  private logger: Logger;
  private fallbackHandler: ((cmdName: string, args: any[], executionState?: any) => any) | null = null;
  private host: IPawScriptHost | null = null;
  
  constructor(logger: Logger) {
    this.logger = logger;
  }
  
  setHost(host: IPawScriptHost): void {
    this.host = host;
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

  setFallbackHandler(handler: (cmdName: string, args: any[], executionState?: any) => any): void {
    this.fallbackHandler = handler;
  }

  requestCompletionToken(
    cleanupCallback: ((tokenId: string) => void) | null = null, 
    parentTokenId: string | null = null, 
    timeoutMs: number = 300000,
    executionState?: ExecutionState
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
      // NEW: Store the actual execution state reference
      executionState: executionState,
      // Keep the old snapshot fields for backwards compatibility
      suspendedResult: executionState?.getResult(),
      hasSuspendedResult: executionState?.hasResultValue()
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
        hasSuspendedResult: data.hasSuspendedResult
      }))
    };
  }
  
  pushCommandSequence(
    tokenId: string, 
    type: 'sequence' | 'conditional' | 'or', 
    remainingCommands: string[], 
    currentIndex: number, 
    originalCommand: string,
    executionState?: ExecutionState
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
      hasInheritedResult: stateSnapshot.hasResult
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
    
    // FIXED: Use the stored execution state instead of creating a new one
    const executionState = tokenData.executionState || new ExecutionState();
    
    // Remove the old logic that was overriding the execution state
    // We don't need to restore snapshots anymore since we're using the actual state
    
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
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd, executionState);
      
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
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd, executionState);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
      if (!success) break; // Stop on failure for conditional
    }
    
    return success;
  }
  
  private resumeOr(sequence: CommandSequence, result: boolean, executionState: ExecutionState): boolean {
    if (result) {
      return true;
    }
    
    let success = false;
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd, executionState);
      
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
    
    // Create new execution state for this command
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
          this.logger.error(`Error executing command ${commandStr}: ${error}`, error);
          if (this.host) {
            this.host.updateStatus(`Error executing command: ${commandStr} - ${error}`);
          }
          return false;
        }
      }
      this.logger.warn(`Unknown command: ${commandStr}`);
      if (this.host) {
        this.host.updateStatus(`Unknown command: ${commandStr}`);
      }
      return false;
    }
    
    // Create default substitution context with empty args
    const substitutionContext = {
      args: [],
      executionState: executionState
    };
    
    return this.executeWithState(commandStr, executionState, substitutionContext);
  }
  
  executeWithState(commandStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    const hasSequence = this.hasUnquotedChar(commandStr, ';');
    this.logger.debug(`hasSequence: ${hasSequence}`);
    if (hasSequence) {
      this.logger.debug('Executing as sequence');
      return this.executeSequence(commandStr, executionState, substitutionContext);
    }
    
    const hasOr = this.hasUnquotedChar(commandStr, '|');
    this.logger.debug(`hasOr: ${hasOr}`);
    if (hasOr) {
      this.logger.debug('Executing as OR');
      return this.executeOr(commandStr, executionState, substitutionContext);
    }
    
    const hasConditional = this.hasUnquotedChar(commandStr, '&');
    this.logger.debug(`hasConditional: ${hasConditional}`);
    if (hasConditional) {
      this.logger.debug('Executing as conditional');
      return this.executeConditional(commandStr, executionState, substitutionContext);
    }
    
    this.logger.debug('Executing as single command');
    return this.executeSingleCommand(commandStr, executionState, substitutionContext);
  }
  
  private createContext(args: any[], executionState: ExecutionState): any {
    if (!this.host) {
      throw new Error('No host set for command system');
    }
    
    return {
      host: this.host,
      args: args,
      state: this.host.getCurrentContext(),
      requestToken: (cleanup?: (tokenId: string) => void) => {
        return this.requestCompletionToken(cleanup, undefined, 300000, executionState);
      },
      resumeToken: (tokenId: string, result: boolean) => {
        return this.popAndResumeCommandSequence(tokenId, result, executionState.getResult(), executionState.hasResultValue());
      },
      // NEW: Result management methods
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
  
  private executeSequence(sequenceStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    this.logger.debug(`Executing command sequence: ${sequenceStr}`);
    const commands = this.splitBySemicolon(sequenceStr);
    
    let success: boolean = true;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd, executionState, substitutionContext);

      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up sequence continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended sequence for token ${tokenId}`);
            },
            undefined,
            300000,
            executionState
          );
          
          this.pushCommandSequence(sequenceToken, 'sequence', remainingCommands, i + 1, sequenceStr, executionState);
          this.chainTokens(result, sequenceToken);
          
          return sequenceToken;
        } else {
          return result;
        }
      }
      
      success = result as boolean;
    }
    
    return success;
  }
  
  private executeConditional(conditionalStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    this.logger.debug(`Executing conditional AND: ${conditionalStr}`);
    const commands = this.splitByAmpersand(conditionalStr);
    
    let success: boolean = true;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd, executionState, substitutionContext);
      
      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up conditional continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended conditional sequence for token ${tokenId}`);
            },
            undefined,
            300000,
            executionState
          );
          
          this.pushCommandSequence(sequenceToken, 'conditional', remainingCommands, i + 1, conditionalStr, executionState);
          this.chainTokens(result, sequenceToken);
          
          return sequenceToken;
        } else {
          return result;
        }
      }
      
      success = result as boolean;
      if (!success) break;
    }
    
    return success;
  }
  
  private executeOr(orStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    this.logger.debug(`Executing conditional OR: ${orStr}`);
    const commands = this.splitByPipe(orStr);
    
    let success: boolean = false;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd, executionState, substitutionContext);
      
      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up OR continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended OR sequence for token ${tokenId}`);
            },
            undefined,
            300000,
            executionState
          );
          
          this.pushCommandSequence(sequenceToken, 'or', remainingCommands, i + 1, orStr, executionState);
          this.chainTokens(result, sequenceToken);
          
          return sequenceToken;
        } else {
          return result;
        }
      }
      
      success = result as boolean;
      if (success) break;
    }
    
    return success;
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
  
  private executeSingleCommandInternal(cmd: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    if (this.hasUnquotedChar(cmd, ';')) {
      return this.executeSequence(cmd, executionState, substitutionContext);
    } else if (this.hasUnquotedChar(cmd, '|')) {
      return this.executeOr(cmd, executionState, substitutionContext);
    } else if (this.hasUnquotedChar(cmd, '&')) {
      return this.executeConditional(cmd, executionState, substitutionContext);
    } else {
      return this.executeSingleCommand(cmd, executionState, substitutionContext);
    }
  }
  
  private executeSingleCommand(commandStr: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext): boolean | string {
    commandStr = commandStr.trim();

    // Apply syntactic sugar to the individual command
    commandStr = this.applySyntacticSugar(commandStr);
    
    this.logger.debug(`executeSingleCommand called with: "${commandStr}"`);
    
    // Apply substitution (including brace evaluation) first
    if (substitutionContext) {
      commandStr = this.applySubstitution(commandStr, substitutionContext);
      this.logger.debug(`After substitution: "${commandStr}"`);
    }
    
    // After substitution, check if the entire command string is now a macro or different command
    // This handles cases where brace evaluation changes the command name
    if (commandStr.trim() !== commandStr || this.hasUnquotedChar(commandStr, ' ')) {
      // Re-parse if the command changed or has arguments
      const parsed = this.parseCommand(commandStr, substitutionContext);
      const cmdName = parsed.command;
      const args = parsed.arguments;
      
      this.logger.debug(`Re-parsed as - Command: "${cmdName}", Args: ${JSON.stringify(args)}`);
      
      // Check if this is now a different command or macro
      let handler = this.commands.get(cmdName);
      
      if (!handler && this.fallbackHandler) {
        this.logger.debug(`Command "${cmdName}" not found, trying fallback handler`);
        const fallbackResult = this.fallbackHandler(cmdName, args, executionState);
        if (fallbackResult !== null) {
          this.logger.debug(`Fallback handler returned: ${fallbackResult}`);
          return fallbackResult;
        }
      }
      
      if (!handler) {
        this.logger.warn(`Unknown command: ${cmdName}`);
        if (this.host) {
          this.host.updateStatus(`Unknown command: ${cmdName}`);
        }
        return false;
      }
      
      try {
        this.logger.debug(`Executing ${cmdName} with args: ${JSON.stringify(args)}`);
        return handler(this.createContext(args, executionState));
      } catch (error) {
        this.logger.error(`Error executing ${cmdName}: ${error}`, error);
        if (this.host) {
          this.host.updateStatus(`Error executing command: ${cmdName} - ${error}`);
        }
        return false;
      }
    } else {
      // No substitution needed or command didn't change, use original parsing
      const parsed = this.parseCommand(commandStr, substitutionContext);
      const cmdName = parsed.command;
      const args = parsed.arguments;
      
      this.logger.debug(`Parsed as - Command: "${cmdName}", Args: ${JSON.stringify(args)}`);
      this.logger.debug(`Looking for command: "${cmdName}", available commands: ${Array.from(this.commands.keys()).join(', ')}`);
      
      let handler = this.commands.get(cmdName);
      
      if (!handler && this.fallbackHandler) {
        this.logger.debug(`Command "${cmdName}" not found, trying fallback handler`);
        const fallbackResult = this.fallbackHandler(cmdName, args, executionState);
        if (fallbackResult !== null) {
          this.logger.debug(`Fallback handler returned: ${fallbackResult}`);
          return fallbackResult;
        }
      }
      
      if (!handler) {
        this.logger.warn(`Unknown command: ${cmdName}`);
        if (this.host) {
          this.host.updateStatus(`Unknown command: ${cmdName}`);
        }
        return false;
      }
      
      try {
        this.logger.debug(`Executing ${cmdName} with args: ${JSON.stringify(args)}`);
        return handler(this.createContext(args, executionState));
      } catch (error) {
        this.logger.error(`Error executing ${cmdName}: ${error}`, error);
        if (this.host) {
          this.host.updateStatus(`Error executing command: ${cmdName} - ${error}`);
        }
        return false;
      }
    }
  }
  
  private parseCommand(commandStr: string, substitutionContext?: SubstitutionContext): { command: string; arguments: any[] } {
    if (!commandStr.trim()) {
      return { command: '', arguments: [] };
    }
    
    // First, find the command name (everything up to the first unquoted space)
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
      // No arguments, but we still need to check for substitution in command name
      const substitutedCommand = this.applySubstitution(commandStr.trim(), substitutionContext);
      return { command: substitutedCommand, arguments: [] };
    }
    
    const command = this.applySubstitution(commandStr.substring(0, commandEnd).trim(), substitutionContext);
    const argsStr = commandStr.substring(commandEnd).trim();
    
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
      
      // NEW: Handle braces
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
      
      // Only use comma as separator (correct PawScript syntax)
      if (!inQuote && parenCount === 0 && braceCount === 0 && char === ',') {
        args.push(this.parseArgumentValue(currentArg.trim(), substitutionContext));
        currentArg = '';
        
        // Skip whitespace after comma
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
    
    // Apply substitution first, before checking for other patterns
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
  
  // NEW: Apply substitution patterns with two-stage process
  private applySubstitution(str: string, substitutionContext?: SubstitutionContext): string {
    if (!substitutionContext) {
      return str;
    }
    
    let result = str;
    
    // Stage 1: Token Assembly - resolve all brace expressions
    result = this.substituteBraceExpressions(result, substitutionContext);
    
    // Stage 2: Token Re-evaluation - apply substitution patterns to the complete token
    // Handle $* (all arguments)
    if (substitutionContext.args.length > 0) {
      const allArgs = substitutionContext.args.map(arg => this.formatArgumentForSubstitution(arg)).join(', ');
      result = result.replace(/\$\*/g, allArgs);
    } else {
      result = result.replace(/\$\*/g, '');
    }
    
    // Handle $# (argument count)
    result = result.replace(/\$#/g, substitutionContext.args.length.toString());
    
    // Handle $1, $2, etc. (indexed arguments)
    result = result.replace(/\$(\d+)/g, (match, indexStr) => {
      const index = parseInt(indexStr, 10) - 1; // Convert to 0-based
      if (index >= 0 && index < substitutionContext.args.length) {
        return this.formatArgumentForSubstitution(substitutionContext.args[index]);
      }
      return match; // Leave unchanged if out of bounds
    });
    
    return result;
  }
  
  // NEW: Handle brace expressions {...} with any surrounding text
  private substituteBraceExpressions(str: string, substitutionContext: SubstitutionContext): string {
    let result = str;
    let modified = true;
    
    // Keep processing until no more braces are found (handles nested cases)
    while (modified) {
      modified = false;
      
      // Find the first complete brace pair (not counting nested pairs)
      let braceStart = -1;
      let braceEnd = -1;
      let braceDepth = 0;
      
      for (let i = 0; i < result.length; i++) {
        const char = result[i];
        
        if (char === '{') {
          if (braceDepth === 0) {
            braceStart = i;
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
      
      // If we found a complete brace pair, process it
      if (braceStart !== -1 && braceEnd !== -1) {
        const beforeBrace = result.substring(0, braceStart);
        const braceContent = result.substring(braceStart + 1, braceEnd);
        const afterBrace = result.substring(braceEnd + 1);
        
        try {
          // Create child execution state that inherits current result
          const childState = substitutionContext.executionState.createChildState();
          
          // Execute the brace content as a command
          const executeResult = this.executeWithState(braceContent, childState, substitutionContext);
          
          // Get the execution result
          let executionValue = '';
          if (childState.hasResultValue()) {
            executionValue = String(childState.getResult());
          } else if (typeof executeResult === 'boolean') {
            executionValue = executeResult.toString();
          } else if (typeof executeResult === 'string' && !executeResult.startsWith('token_')) {
            executionValue = executeResult;
          }
          
          // Assemble the new token: beforeBrace + executionValue + afterBrace
          const assembledToken = beforeBrace + executionValue + afterBrace;
          
          // Now re-evaluate the assembled token for further substitution patterns
          result = this.reEvaluateToken(assembledToken, substitutionContext);
          modified = true;
          
        } catch (error) {
          this.logger.error(`Error evaluating brace expression {${braceContent}}: ${error}`);
          // Leave the expression unchanged on error and continue
          break;
        }
      }
    }
    
    return result;
  }
  
  // NEW: Re-evaluate an assembled token for macro calls and substitution patterns
  private reEvaluateToken(token: string, substitutionContext: SubstitutionContext): string {
    let result = token;
    
    // First check if the token (or part of it) is a macro name
    // We need to be careful here - we only want to re-evaluate if it's a complete token
    // that would make sense as a command, not arbitrary text
    
    // For now, let's implement basic re-evaluation for argument substitution patterns
    // Handle $* (all arguments)
    if (substitutionContext.args.length > 0) {
      const allArgs = substitutionContext.args.map(arg => this.formatArgumentForSubstitution(arg)).join(', ');
      result = result.replace(/\$\*/g, allArgs);
    } else {
      result = result.replace(/\$\*/g, '');
    }
    
    // Handle $# (argument count)
    result = result.replace(/\$#/g, substitutionContext.args.length.toString());
    
    // Handle $1, $2, etc. (indexed arguments)
    result = result.replace(/\$(\d+)/g, (match, indexStr) => {
      const index = parseInt(indexStr, 10) - 1; // Convert to 0-based
      if (index >= 0 && index < substitutionContext.args.length) {
        return this.formatArgumentForSubstitution(substitutionContext.args[index]);
      }
      return match; // Leave unchanged if out of bounds
    });
    
    return result;
  }
  
  private formatArgumentForSubstitution(arg: any): string {
    if (typeof arg === 'string') {
      // If the string contains spaces or special characters, quote it
      if (arg.includes(' ') || arg.includes(';') || arg.includes('&') || arg.includes('|') || arg.includes(',')) {
        return `'${arg.replace(/'/g, "\\'")}'`;
      }
      return arg;
    } else if (typeof arg === 'number' || typeof arg === 'boolean') {
      return String(arg);
    } else {
      // For other types, convert to JSON string
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
  
  private hasUnquotedChar(str: string, char: string): boolean {
    let inQuote = false;
    let quoteChar: string | null = null;
    let parenCount = 0;
    let braceCount = 0;
    
    for (let i = 0; i < str.length; i++) {
      const c = str[i];
      
      if (c === '\\' && i + 1 < str.length) {
        const nextChar = str[i + 1];
        if (nextChar === '"' || nextChar === "'" || nextChar === '\\') {
          i++;
          continue;
        }
      }
      
      if ((c === "'" || c === '"') && !inQuote) {
        inQuote = true;
        quoteChar = c;
        continue;
      }
      
      if (c === quoteChar && inQuote) {
        inQuote = false;
        quoteChar = null;
        continue;
      }
      
      if (!inQuote && c === '(') {
        parenCount++;
        continue;
      }
      
      if (!inQuote && c === ')') {
        parenCount--;
        continue;
      }
      
      if (!inQuote && c === '{') {
        braceCount++;
        continue;
      }
      
      if (!inQuote && c === '}') {
        braceCount--;
        continue;
      }
      
      if (c === char && !inQuote && parenCount === 0 && braceCount === 0) {
        return true;
      }
    }
    
    return false;
  }
  
  private splitBySemicolon(str: string): string[] {
    return this.splitByChar(str, ';');
  }
  
  private splitByAmpersand(str: string): string[] {
    return this.splitByChar(str, '&');
  }
  
  private splitByPipe(str: string): string[] {
    return this.splitByChar(str, '|');
  }
  
  private splitByChar(str: string, delimiter: string): string[] {
    const commands: string[] = [];
    let inQuote = false;
    let quoteChar: string | null = null;
    let parenCount = 0;
    let braceCount = 0;
    let currentCmd = '';
    
    for (let i = 0; i < str.length; i++) {
      const char = str[i];
      
      if (char === '\\' && i + 1 < str.length) {
        currentCmd += char + str[i + 1];
        i++;
        continue;
      }
      
      if ((char === "'" || char === '"') && !inQuote) {
        inQuote = true;
        quoteChar = char;
        currentCmd += char;
        continue;
      }
      
      if (char === quoteChar && inQuote) {
        inQuote = false;
        quoteChar = null;
        currentCmd += char;
        continue;
      }
      
      if (!inQuote && char === '(') {
        parenCount++;
        currentCmd += char;
        continue;
      }
      
      if (!inQuote && char === ')') {
        parenCount--;
        currentCmd += char;
        continue;
      }
      
      if (!inQuote && char === '{') {
        braceCount++;
        currentCmd += char;
        continue;
      }
      
      if (!inQuote && char === '}') {
        braceCount--;
        currentCmd += char;
        continue;
      }
      
      if (char === delimiter && !inQuote && parenCount === 0 && braceCount === 0) {
        commands.push(currentCmd.trim());
        currentCmd = '';
        continue;
      }
      
      currentCmd += char;
    }
    
    if (currentCmd.trim() || commands.length > 0) {
      commands.push(currentCmd.trim());
    }
    
    return commands;
  }
}
