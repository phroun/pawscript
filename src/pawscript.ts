import { Logger } from './logger';
import { CommandExecutor } from './command-executor';
import { MacroSystem } from './macro-system';
import { IPawScriptHost, PawScriptHandler, PawScriptConfig } from './types';

export class PawScript {
  private logger: Logger;
  private executor: CommandExecutor;
  private macroSystem: MacroSystem;
  private config: Required<PawScriptConfig>;
  
  constructor(config: PawScriptConfig = {}) {
    this.config = {
      debug: config.debug ?? false,
      defaultTokenTimeout: config.defaultTokenTimeout ?? 300000,
      enableSyntacticSugar: config.enableSyntacticSugar ?? true,
      allowMacros: config.allowMacros ?? true,
      commandSeparators: {
        sequence: ';',
        conditional: '&',
        alternative: '|',
        ...config.commandSeparators
      }
    };
    
    this.logger = new Logger(this.config.debug);
    this.executor = new CommandExecutor(this.logger);
    this.macroSystem = new MacroSystem(this.logger);
    
    // Set up macro fallback handler
    this.executor.setFallbackHandler((cmdName: string, args: any[]) => {
      if (this.config.allowMacros && this.macroSystem.hasMacro(cmdName)) {
        if (args.length > 0) {
          this.logger.warn(`Macro ${cmdName} called with arguments, but macros don't support arguments yet`);
        }
        return this.macroSystem.executeMacro(cmdName, (commands) => {
          return this.executor.execute(commands);
        });
      }
      return null;
    });
    
    this.logger.debug('PawScript initialized');
  }
  
  setHost(host: IPawScriptHost): void {
    this.executor.setHost(host);
  }
  
  configure(config: Partial<PawScriptConfig>): void {
    this.config = { ...this.config, ...config };
    this.logger.setEnabled(this.config.debug);
  }
  
  registerCommand(name: string, handler: PawScriptHandler): void {
    this.executor.registerCommand(name, handler);
  }
  
  registerCommands(commands: Record<string, PawScriptHandler>): void {
    this.executor.registerCommands(commands);
  }
  
  execute(commandString: string, ...args: any[]): boolean | string {
    return this.executor.execute(commandString, ...args);
  }
  
  requestToken(cleanupCallback?: (tokenId: string) => void, parentToken?: string, timeout?: number): string {
    return this.executor.requestCompletionToken(
      cleanupCallback || null, 
      parentToken || null, 
      timeout || this.config.defaultTokenTimeout
    );
  }
  
  resumeToken(tokenId: string, result: boolean): boolean {
    return this.executor.popAndResumeCommandSequence(tokenId, result);
  }
  
  getTokenStatus(): any {
    return this.executor.getTokenStatus();
  }
  
  forceCleanupToken(tokenId: string): void {
    this.executor.forceCleanupToken(tokenId);
  }
  
  defineMacro(name: string, commandSequence: string): boolean {
    if (!this.config.allowMacros) {
      this.logger.warn('Macros are disabled in configuration');
      return false;
    }
    return this.macroSystem.defineMacro(name, commandSequence);
  }
  
  executeMacro(name: string): any {
    if (!this.config.allowMacros) {
      this.logger.warn('Macros are disabled in configuration');
      return false;
    }
    return this.macroSystem.executeMacro(name, (commands) => {
      return this.executor.execute(commands);
    });
  }
  
  listMacros(): string[] {
    return this.macroSystem.listMacros();
  }
  
  getMacro(name: string): string | null {
    return this.macroSystem.getMacro(name);
  }
  
  deleteMacro(name: string): boolean {
    return this.macroSystem.deleteMacro(name);
  }
  
  clearMacros(): number {
    return this.macroSystem.clearMacros();
  }
  
  hasMacro(name: string): boolean {
    return this.macroSystem.hasMacro(name);
  }
  
  setFallbackHandler(handler: (cmdName: string, args: any[]) => any): void {
    this.executor.setFallbackHandler(handler);
  }
}
