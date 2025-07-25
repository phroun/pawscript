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
  
  executeMacro(name: string, executeCallback: (commands: string) => any, args: any[] = []): any {
    this.logger.debug(`Executing macro: ${name} with args: ${JSON.stringify(args)}`);
    
    if (!name) {
      this.logger.error('Macro name is required');
      return false;
    }
    
    if (!this.macros.has(name)) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    let commands = this.macros.get(name)!;
    
    // Substitute arguments if provided
    if (args.length > 0) {
      commands = this.substituteArguments(commands, args);
      this.logger.debug(`Macro "${name}" after argument substitution: ${commands}`);
    }
    
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
  
  private substituteArguments(commands: string, args: any[]): string {
    let result = commands;
    
    // Replace $1, $2, $3, etc. with the corresponding arguments
    for (let i = 0; i < args.length; i++) {
      const placeholder = `${i + 1}`;
      const argValue = this.formatArgumentForSubstitution(args[i]);
      result = result.replace(new RegExp('\\' + placeholder, 'g'), argValue);
    }
    
    // Replace $* with all arguments
    if (args.length > 0) {
      const allArgs = args.map(arg => this.formatArgumentForSubstitution(arg)).join(', ');
      result = result.replace(/\$\*/g, allArgs);
    }
    
    return result;
  }
  
  private formatArgumentForSubstitution(arg: any): string {
    if (typeof arg === 'string') {
      // If the string contains spaces or special characters, quote it
      if (arg.includes(' ') || arg.includes(';') || arg.includes('&') || arg.includes('|')) {
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
