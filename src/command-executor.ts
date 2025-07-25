import { Logger } from './logger';
import { PawScriptHandler, TokenData, CommandSequence, IPawScriptHost } from './types';

export class CommandExecutor {
  private commands = new Map<string, PawScriptHandler>();
  private activeTokens = new Map<string, TokenData>();
  private nextTokenId = 1;
  private logger: Logger;
  private fallbackHandler: ((cmdName: string, args: any[]) => any) | null = null;
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
    
  setFallbackHandler(handler: (cmdName: string, args: any[]) => any): void {
    this.fallbackHandler = handler;
  }
  
  requestCompletionToken(
    cleanupCallback: ((tokenId: string) => void) | null = null, 
    parentTokenId: string | null = null, 
    timeoutMs: number = 300000
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
      timestamp: Date.now()
    });
    
    if (parentTokenId && this.activeTokens.has(parentTokenId)) {
      this.activeTokens.get(parentTokenId)!.children.add(tokenId);
    }
    
    this.logger.debug(`Created completion token: ${tokenId}, parent: ${parentTokenId || 'none'}`);
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
        age: Date.now() - data.timestamp
      }))
    };
  }
  
  pushCommandSequence(
    tokenId: string, 
    type: 'sequence' | 'conditional' | 'or', 
    remainingCommands: string[], 
    currentIndex: number, 
    originalCommand: string
  ): void {
    if (!this.activeTokens.has(tokenId)) {
      throw new Error(`Invalid completion token: ${tokenId}`);
    }
    
    const tokenData = this.activeTokens.get(tokenId)!;
    tokenData.commandSequence = {
      type,
      remainingCommands: [...remainingCommands],
      currentIndex,
      totalCommands: remainingCommands.length + currentIndex,
      originalCommand,
      timestamp: Date.now()
    };
    
    this.logger.debug(`Pushed command sequence onto token ${tokenId}. Type: ${type}, Remaining: ${remainingCommands.length}`);
  }
  
  popAndResumeCommandSequence(tokenId: string, result: boolean): boolean {
    if (!this.activeTokens.has(tokenId)) {
      this.logger.warn(`Attempted to resume with invalid token: ${tokenId}`);
      return false;
    }
    
    const tokenData = this.activeTokens.get(tokenId)!;
    
    this.logger.debug(`Popping command sequence from token ${tokenId}. Type: ${tokenData.commandSequence?.type}, Result: ${result}`);
    
    this.cleanupTokenChildren(tokenId);
    
    if (tokenData.timeoutId) {
      clearTimeout(tokenData.timeoutId);
    }
    
    let success = result;
    if (tokenData.commandSequence) {
      success = this.resumeCommandSequence(tokenData.commandSequence, result);
    }
    
    const chainedToken = tokenData.chainedToken;
    
    this.activeTokens.delete(tokenId);
    
    if (tokenData.parentToken && this.activeTokens.has(tokenData.parentToken)) {
      this.activeTokens.get(tokenData.parentToken)!.children.delete(tokenId);
    }
    
    if (chainedToken && this.activeTokens.has(chainedToken)) {
      this.logger.debug(`Triggering chained token ${chainedToken} with result ${success}`);
      return this.popAndResumeCommandSequence(chainedToken, success);
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
  
  private resumeCommandSequence(commandSequence: CommandSequence, result: boolean): boolean {
    switch (commandSequence.type) {
      case 'sequence':
        return this.resumeSequence(commandSequence, result);
      case 'conditional':
        return this.resumeConditional(commandSequence, result);
      case 'or':
        return this.resumeOr(commandSequence, result);
      default:
        this.logger.error(`Unknown command sequence type: ${commandSequence.type}`);
        return false;
    }
  }
  
  private resumeSequence(sequence: CommandSequence, result: boolean): boolean {
    let success = result;
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
    }
    
    return success;
  }

  private resumeConditional(sequence: CommandSequence, result: boolean): boolean {
    if (!result) {
      return false;
    }
    
    let success: boolean = result;
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd);
      
      if (typeof cmdResult === 'string' && cmdResult.startsWith('token_')) {
        this.logger.warn('Command returned token during resume - this may indicate a problem');
        return false;
      }
      
      success = cmdResult as boolean;
      if (!success) break; // Stop on failure for conditional
    }
    
    return success;
  }
  
  private resumeOr(sequence: CommandSequence, result: boolean): boolean {
    if (result) {
      return true;
    }
    
    let success = false;
    
    for (const cmd of sequence.remainingCommands) {
      if (!cmd.trim()) continue;
      
      const cmdResult = this.executeSingleCommandInternal(cmd);
      
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
    
    if (args.length > 0) {
      const handler = this.commands.get(commandStr);
      if (handler) {
        try {
          const result = handler(this.createContext(args));
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
    
    commandStr = this.applySyntacticSugar(commandStr);
    
    this.logger.debug(`Checking command: ${commandStr}`);
    
    const hasSequence = this.hasUnquotedChar(commandStr, ';');
    this.logger.debug(`hasSequence: ${hasSequence}`);
    if (hasSequence) {
      this.logger.debug('Executing as sequence');
      return this.executeSequence(commandStr);
    }
    
    const hasOr = this.hasUnquotedChar(commandStr, '|');
    this.logger.debug(`hasOr: ${hasOr}`);
    if (hasOr) {
      this.logger.debug('Executing as OR');
      return this.executeOr(commandStr);
    }
    
    const hasConditional = this.hasUnquotedChar(commandStr, '&');
    this.logger.debug(`hasConditional: ${hasConditional}`);
    if (hasConditional) {
      this.logger.debug('Executing as conditional');
      return this.executeConditional(commandStr);
    }
    
    this.logger.debug('Executing as single command');
    return this.executeSingleCommand(commandStr);
  }
  
  private createContext(args: any[]): any {
    if (!this.host) {
      throw new Error('No host set for command system');
    }
    
    return {
      host: this.host,
      args: args,
      state: this.host.getCurrentContext(),
      requestToken: (cleanup?: (tokenId: string) => void) => {
        return this.requestCompletionToken(cleanup);
      },
      resumeToken: (tokenId: string, result: boolean) => {
        return this.popAndResumeCommandSequence(tokenId, result);
      }
    };
  }

  private applySyntacticSugar(commandStr: string): string {
    const spaceIndex = commandStr.indexOf(' ');
    if (spaceIndex === -1) {
      return commandStr;
    }
    
    const commandPart = commandStr.substring(0, spaceIndex);
    const argsPart = commandStr.substring(spaceIndex + 1);
    
    const identifierParenMatch = argsPart.match(/^([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.+)\)$/);
    
    if (identifierParenMatch) {
      const identifier = identifierParenMatch[1];
      const content = identifierParenMatch[2];
      
      return `${commandPart} '${identifier}', (${content})`;
    }
    
    return commandStr;
  }
  
  private executeSequence(sequenceStr: string): boolean | string {
    this.logger.debug(`Executing command sequence: ${sequenceStr}`);
    const commands = this.splitBySemicolon(sequenceStr);
    
    let success: boolean = true;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd);
      
      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up sequence continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended sequence for token ${tokenId}`);
            }
          );
          
          this.pushCommandSequence(sequenceToken, 'sequence', remainingCommands, i + 1, sequenceStr);
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
  
  private executeConditional(conditionalStr: string): boolean | string {
    this.logger.debug(`Executing conditional AND: ${conditionalStr}`);
    const commands = this.splitByAmpersand(conditionalStr);
    
    let success: boolean = true;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd);
      
      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up conditional continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended conditional sequence for token ${tokenId}`);
            }
          );
          
          this.pushCommandSequence(sequenceToken, 'conditional', remainingCommands, i + 1, conditionalStr);
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
  
  private executeOr(orStr: string): boolean | string {
    this.logger.debug(`Executing conditional OR: ${orStr}`);
    const commands = this.splitByPipe(orStr);
    
    let success: boolean = false;
    for (let i = 0; i < commands.length; i++) {
      const cmd = commands[i];
      
      if (!cmd.trim()) continue;
      
      const result = this.executeSingleCommandInternal(cmd);
      
      if (result && typeof result === 'string' && result.startsWith('token_')) {
        this.logger.debug(`Command ${cmd} returned token ${result}, setting up OR continuation`);
        
        const remainingCommands = commands.slice(i + 1);
        if (remainingCommands.length > 0) {
          const sequenceToken = this.requestCompletionToken(
            (tokenId) => {
              this.logger.debug(`Cleaning up suspended OR sequence for token ${tokenId}`);
            }
          );
          
          this.pushCommandSequence(sequenceToken, 'or', remainingCommands, i + 1, orStr);
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
  
  private executeSingleCommandInternal(cmd: string): boolean | string {
    if (this.hasUnquotedChar(cmd, ';')) {
      return this.executeSequence(cmd);
    } else if (this.hasUnquotedChar(cmd, '|')) {
      return this.executeOr(cmd);
    } else if (this.hasUnquotedChar(cmd, '&')) {
      return this.executeConditional(cmd);
    } else {
      return this.executeSingleCommand(cmd);
    }
  }
  
  private executeSingleCommand(commandStr: string): boolean | string {
    commandStr = commandStr.trim();
    
    const parsed = this.parseCommand(commandStr);
    const cmdName = parsed.command;
    const args = parsed.arguments;
    
    this.logger.debug(`Looking for command: "${cmdName}", available commands: ${Array.from(this.commands.keys()).join(', ')}`);
    
    let handler = this.commands.get(cmdName);
    
    if (!handler && this.fallbackHandler) {
      const fallbackResult = this.fallbackHandler(cmdName, args);
      if (fallbackResult !== null) {
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
      return handler(this.createContext(args));
    } catch (error) {
      this.logger.error(`Error executing ${cmdName}: ${error}`, error);
      if (this.host) {
        this.host.updateStatus(`Error executing command: ${cmdName} - ${error}`);
      }
      return false;
    }
  }
  
  private parseCommand(commandStr: string): { command: string; arguments: any[] } {
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
      return { command: commandStr.trim(), arguments: [] };
    }
    
    const command = commandStr.substring(0, commandEnd).trim();
    const argsStr = commandStr.substring(commandEnd).trim();
    
    if (!argsStr) {
      return { command, arguments: [] };
    }
    
    const args = this.parseArguments(argsStr);
    
    return { command, arguments: args };
  }
  
  private parseArguments(argsStr: string): any[] {
    const args: any[] = [];
    let currentArg = '';
    let inQuote = false;
    let quoteChar: string | null = null;
    let parenCount = 0;
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
      
      if (!inQuote && parenCount === 0 && char === ',') {
        args.push(this.parseArgumentValue(currentArg.trim()));
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
      args.push(this.parseArgumentValue(currentArg.trim()));
    }
    
    return args;
  }
  
  private parseArgumentValue(argStr: string): any {
    if (!argStr) {
      return null;
    }
    
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
      
      if (c === char && !inQuote && parenCount === 0) {
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
      
      if (char === delimiter && !inQuote && parenCount === 0) {
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
