import { Logger } from './logger';

export class MacroSystem {
  private macros = new Map<string, string>();
  private logger: Logger;
  
  constructor(logger: Logger) {
    this.logger = logger;
  }
  
  defineMacro(name: string, commands: string): boolean {
    this.logger.debug(`Defining macro: ${name} = ${commands}`);
    
    if (!name || !commands) {
      this.logger.error('Macro name and commands are required');
      return false;
    }
    
    this.macros.set(name, commands);
    this.logger.debug(`Macro "${name}" defined successfully`);
    
    return true;
  }
  
  executeMacro(name: string, executeCallback: (commands: string) => any): any {
    this.logger.debug(`Executing macro: ${name}`);
    
    if (!name) {
      this.logger.error('Macro name is required');
      return false;
    }
    
    if (!this.macros.has(name)) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    const commands = this.macros.get(name)!;
    this.logger.debug(`Executing macro "${name}" with commands: ${commands}`);
    
    try {
      const result = executeCallback(commands);
      this.logger.debug(`Macro "${name}" executed with result: ${result}`);
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
    this.logger.debug(`Macro "${name}" deleted successfully`);
    return true;
  }
  
  clearMacros(): number {
    const count = this.macros.size;
    this.macros.clear();
    this.logger.debug(`Cleared ${count} macros`);
    return count;
  }
  
  hasMacro(name: string): boolean {
    return this.macros.has(name);
  }
}
