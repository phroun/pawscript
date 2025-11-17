import { Logger } from './logger';
import { CommandExecutor } from './command-executor';
import { MacroSystem } from './macro-system';
import { ExecutionState } from './execution-state';
import { PawScriptHandler, PawScriptConfig, SubstitutionContext } from './types';

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
      showErrorContext: config.showErrorContext ?? true,
      contextLines: config.contextLines ?? 2,
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
    this.executor.setFallbackHandler((cmdName: string, args: any[], executionState?: any) => {
      if (this.config.allowMacros && this.macroSystem.hasMacro(cmdName)) {
        // Use the provided execution state from the caller, or create new one as fallback
        const macroExecutionState = executionState || new ExecutionState();
        
        // Execute macro with the provided arguments
        const result = this.macroSystem.executeMacro(cmdName, (commands, macroState, substitutionContext) => {
          return this.executor.executeWithState(commands, macroState, substitutionContext);
        }, args, macroExecutionState);
        
        return result;
      }
      return null;
    });

    // Register built-in macro commands if macros are enabled
    if (this.config.allowMacros) {
      this.registerBuiltInMacroCommands();
    }
  }
  
  private registerBuiltInMacroCommands(): void {
    // Define macro command
    this.executor.registerCommand('macro', (context) => {
      if (context.args.length < 2) {
        this.executeScriptError('Usage: macro <name>, <commands>');
        return false;
      }
      
      const name = context.args[0];
      const commands = context.args[1];
      
      const result = this.macroSystem.defineMacro(name, commands);
      if (!result) {
        this.executeScriptError(`Failed to define macro "${name}"`);
      }
      
      return result;
    });
    
    // Execute macro command
    this.executor.registerCommand('call', (context) => {
      if (context.args.length < 1) {
        this.executeScriptError('Usage: call <macro_name> [args...]');
        return false;
      }
      
      const name = context.args[0];
      const macroArgs = context.args.slice(1);
      
      // Create execution state for macro call
      const executionState = new ExecutionState();
      
      return this.macroSystem.executeMacro(name, (commands, macroState, substitutionContext) => {
        return this.executor.executeWithState(commands, macroState, substitutionContext);
      }, macroArgs, executionState);
    });
    
    // List macros command - returns comma-separated list
    this.executor.registerCommand('macro_list', (context) => {
      const macros = this.macroSystem.listMacros();
      context.setResult(macros.join(', '));
      return true;
    });
    
    // Delete macro command
    this.executor.registerCommand('macro_delete', (context) => {
      if (context.args.length < 1) {
        this.executeScriptError('Usage: macro_delete <macro_name>');
        return false;
      }
      
      const name = context.args[0];
      const result = this.macroSystem.deleteMacro(name);
      
      if (!result) {
        this.executeScriptError(`PawScript macro "${name}" not found or could not be deleted`);
      }
      
      return result;
    });
    
    // Clear all macros command
    this.executor.registerCommand('macro_clear', (context) => {
      const count = this.macroSystem.clearMacros();
      context.setResult(`Cleared ${count} PawScript macros`);
      return true;
    });
  }
  
  configure(config: Partial<PawScriptConfig>): void {
    const oldAllowMacros = this.config.allowMacros;
    
    // Update config with new values, preserving existing defaults
    this.config = { 
      ...this.config, 
      ...config,
      commandSeparators: {
        ...this.config.commandSeparators,
        ...config.commandSeparators
      }
    };
    
    this.logger.setEnabled(this.config.debug);
    
    // Handle macro command registration/unregistration
    if (oldAllowMacros !== this.config.allowMacros) {
      if (this.config.allowMacros && !oldAllowMacros) {
        // Macros were enabled
        this.registerBuiltInMacroCommands();
      } else if (!this.config.allowMacros && oldAllowMacros) {
        // Macros were disabled - unregister commands
        this.unregisterBuiltInMacroCommands();
      }
    }
  }
  
  private unregisterBuiltInMacroCommands(): void {
    this.executor.unregisterCommand('macro');
    this.executor.unregisterCommand('call');
    this.executor.unregisterCommand('macro_list');
    this.executor.unregisterCommand('macro_delete');
    this.executor.unregisterCommand('macro_clear');
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
    
    // Create execution state for macro
    const executionState = new ExecutionState();
    
    return this.macroSystem.executeMacro(name, (commands, macroState, substitutionContext) => {
      return this.executor.executeWithState(commands, macroState, substitutionContext);
    }, [], executionState);
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
  
  // Get current configuration
  getConfig(): Required<PawScriptConfig> {
    return { ...this.config };
  }
  
  // Enable/disable error context reporting
  setErrorContextEnabled(enabled: boolean): void {
    this.config.showErrorContext = enabled;
  }
  
  // Set number of context lines for error reporting
  setContextLines(lines: number): void {
    this.config.contextLines = Math.max(0, Math.min(10, lines)); // Clamp between 0-10
  }

  private executeScriptError(message: string): void {
    // Try to execute script_error command if it exists
    const handler = this.executor['commands']?.get('script_error');
    if (handler) {
      try {
        handler({
          args: [message],
          requestToken: () => '',
          resumeToken: () => false,
          setResult: () => {},
          getResult: () => undefined,
          hasResult: () => false,
          clearResult: () => {}
        });
      } catch (error) {
        // Fallback if script_error itself fails
        console.error(`[SCRIPT ERROR] ${message}`);
      }
    } else {
      // Fallback if script_error command not registered
      console.error(`[SCRIPT ERROR] ${message}`);
    }
  }
}
