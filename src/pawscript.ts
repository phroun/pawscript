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
        return this.macroSystem.executeMacro(cmdName, (commands) => {
          return this.executor.execute(commands, ...args);
        }, args);
      }
      return null;
    });
    
    // Register built-in macro commands if macros are enabled
    if (this.config.allowMacros) {
      this.registerBuiltInMacroCommands();
    }
    
    this.logger.debug('PawScript initialized');
  }
  
  private registerBuiltInMacroCommands(): void {
    this.logger.debug('Registering built-in macro commands');
    
    // Define macro command
    this.executor.registerCommand('macro', (context) => {
      if (context.args.length < 2) {
        if (context.host.updateStatus) {
          context.host.updateStatus('Usage: macro <name>, <commands>');
        }
        return false;
      }
      
      const name = context.args[0];
      const commands = context.args[1];
      
      const result = this.macroSystem.defineMacro(name, commands);
      if (result && context.host.updateStatus) {
        context.host.updateStatus(`PawScript macro "${name}" defined`);
      } else if (!result && context.host.updateStatus) {
        context.host.updateStatus(`Failed to define macro "${name}"`);
      }
      
      return result;
    });
    
    // Execute macro command
    this.executor.registerCommand('call', (context) => {
      if (context.args.length < 1) {
        if (context.host.updateStatus) {
          context.host.updateStatus('Usage: call <macro_name> [args...]');
        }
        return false;
      }
      
      const name = context.args[0];
      const macroArgs = context.args.slice(1);
      
      return this.macroSystem.executeMacro(name, (commands) => {
        return this.executor.execute(commands);
      }, macroArgs);
    });
    
    // List macros command
    this.executor.registerCommand('macro_list', (context) => {
      const macros = this.macroSystem.listMacros();
      
      if (macros.length === 0) {
        if (context.host.updateStatus) {
          context.host.updateStatus('No PawScript macros defined');
        }
        return true;
      }
      
      let macroList = 'Defined PawScript macros:\n\n';
      
      for (const name of macros) {
        const commands = this.macroSystem.getMacro(name);
        const displayCommands = commands && commands.length > 60 ? 
          commands.substring(0, 60) + '...' : 
          commands;
        
        macroList += `${name}:\n  ${displayCommands}\n\n`;
      }
      
      // Try to create a window if the host supports it
      if (context.host.createWindow) {
        try {
          const windowId = context.host.createWindow({
            id: 'macro_list',
            type: 'info',
            content: macroList,
            title: 'PawScript Macros',
            dock: 'top',
            priority: 100,
            minHeight: Math.min(15, macros.length * 3 + 5),
            maxHeight: Math.min(15, macros.length * 3 + 5)
          });
          
          if (context.host.render) {
            context.host.render();
          }
        } catch (error) {
          this.logger.warn('Host does not support window creation for macro list', error);
          // Fall back to status message
          if (context.host.updateStatus) {
            context.host.updateStatus(`${macros.length} macros defined. Use debug log to see details.`);
          }
          this.logger.debug('Macro list:\n' + macroList);
        }
      } else {
        // Fall back to status message
        if (context.host.updateStatus) {
          context.host.updateStatus(`${macros.length} macros defined. Use debug log to see details.`);
        }
        this.logger.debug('Macro list:\n' + macroList);
      }
      
      return true;
    });
    
    // Delete macro command
    this.executor.registerCommand('macro_delete', (context) => {
      if (context.args.length < 1) {
        if (context.host.updateStatus) {
          context.host.updateStatus('Usage: macro_delete <macro_name>');
        }
        return false;
      }
      
      const name = context.args[0];
      const result = this.macroSystem.deleteMacro(name);
      
      if (result && context.host.updateStatus) {
        context.host.updateStatus(`PawScript macro "${name}" deleted`);
      } else if (!result && context.host.updateStatus) {
        context.host.updateStatus(`PawScript macro "${name}" not found or could not be deleted`);
      }
      
      return result;
    });
    
    // Clear all macros command
    this.executor.registerCommand('macro_clear', (context) => {
      const count = this.macroSystem.clearMacros();
      if (context.host.updateStatus) {
        context.host.updateStatus(`Cleared ${count} PawScript macros`);
      }
      return true;
    });
    
    this.logger.debug('Built-in macro commands registered');
  }
  
  setHost(host: IPawScriptHost): void {
    this.executor.setHost(host);
  }
  
  configure(config: Partial<PawScriptConfig>): void {
    const oldAllowMacros = this.config.allowMacros;
    this.config = { ...this.config, ...config };
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
    this.logger.debug('Unregistering built-in macro commands');
    
    this.executor.unregisterCommand('macro');
    this.executor.unregisterCommand('call');
    this.executor.unregisterCommand('macro_list');
    this.executor.unregisterCommand('macro_delete');
    this.executor.unregisterCommand('macro_clear');
    
    this.logger.debug('Built-in macro commands unregistered');
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
    }, []);
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
