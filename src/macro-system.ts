import { Logger } from './logger';
import { ExecutionState } from './execution-state';
import { SubstitutionContext } from './types';

export class MacroSystem {
  private macros = new Map<string, string>();
  private logger: Logger;
  
  constructor(logger: Logger) {
    this.logger = logger;
  }
  
  defineMacro(name: string, commands: string): boolean {

    if (!name || !commands) {
      this.logger.error('Macro name and commands are required');
      return false;
    }

    this.macros.set(name, commands);

    return true;
  }
  
  executeMacro(
    name: string, 
    executeCallback: (commands: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext) => any, 
    args: any[] = [],
    executionState?: ExecutionState
  ): any {

    if (!name) {
      this.logger.error('Macro name is required');
      return false;
    }
    
    if (!this.macros.has(name)) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    let commands = this.macros.get(name)!;

    // Create execution state if not provided
    const macroExecutionState = executionState || new ExecutionState();
    
    // Create substitution context for macro arguments
    const substitutionContext: SubstitutionContext = {
      args: args,
      executionState: macroExecutionState
    };
    
    try {
      // The substitution now happens during parsing, not here
      // Just execute the commands with the substitution context
      const result = executeCallback(commands, macroExecutionState, substitutionContext);

      // The macro's formal result is whatever the execution state contains
      // This gets propagated back to the caller
      return result;
    } catch (error) {
      this.logger.error(`Error executing macro "${name}": ${error}`, error);
      return false;
    }
  }
  
  listMacros(): string[] {
    return Array.from(this.macros.keys());
  }
  
  getMacro(name: string): string | null {
    return this.macros.get(name) || null;
  }
  
  deleteMacro(name: string): boolean {
    if (!this.macros.has(name)) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    this.macros.delete(name);
    return true;
  }
  
  clearMacros(): number {
    const count = this.macros.size;
    this.macros.clear();
    return count;
  }
  
  hasMacro(name: string): boolean {
    return this.macros.has(name);
  }
}
