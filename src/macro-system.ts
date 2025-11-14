import { Logger } from './logger';
import { ExecutionState } from './execution-state';
import { SourcePosition, MacroContext, SubstitutionContext, MacroDefinition } from './types';

export class MacroSystem {
  private macros = new Map<string, MacroDefinition>();
  private logger: Logger;
  
  constructor(logger: Logger) {
    this.logger = logger;
  }
  
  defineMacro(
    name: string, 
    commands: string,
    definitionPosition?: SourcePosition
  ): boolean {
    if (!name || !commands) {
      this.logger.error('Macro name and commands are required');
      return false;
    }

    const macro: MacroDefinition = {
      name,
      commands,
      definitionFile: definitionPosition?.filename || '<unknown>',
      definitionLine: definitionPosition?.line || 1,
      definitionColumn: definitionPosition?.column || 1,
      timestamp: Date.now()
    };

    this.macros.set(name, macro);
    this.logger.debug(`Defined macro "${name}" at ${macro.definitionFile}:${macro.definitionLine}`);

    return true;
  }
  
  executeMacro(
    name: string, 
    executeCallback: (commands: string, executionState: ExecutionState, substitutionContext?: SubstitutionContext) => any, 
    args: any[] = [],
    executionState?: ExecutionState,
    invocationPosition?: SourcePosition
  ): any {
    if (!name) {
      this.logger.error('Macro name is required');
      return false;
    }
    
    const macroDef = this.macros.get(name);
    if (!macroDef) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    // Create macro context for error tracking
    const macroContext: MacroContext = {
      macroName: name,
      definitionFile: macroDef.definitionFile,
      definitionLine: macroDef.definitionLine,
      definitionColumn: macroDef.definitionColumn,
      invocationFile: invocationPosition?.filename,
      invocationLine: invocationPosition?.line,
      invocationColumn: invocationPosition?.column,
      parentMacro: invocationPosition?.macroContext  // Chain nested macros
    };
    
    this.logger.debug(`Executing macro "${name}" defined at ${macroDef.definitionFile}:${macroDef.definitionLine}${
      invocationPosition ? `, called from ${invocationPosition.filename || '<unknown>'}:${invocationPosition.line}` : ''
    }`);

    // Create execution state if not provided
    const macroExecutionState = executionState || new ExecutionState();
    
    // Create substitution context for macro arguments with macro context
    const substitutionContext: SubstitutionContext = {
      args: args,
      executionState: macroExecutionState,
      macroContext: macroContext
    };
    
    try {
      // Execute the macro commands with enhanced context
      // The executeCallback should create a new parser with the macro's definition file context
      const result = executeCallback(macroDef.commands, macroExecutionState, substitutionContext);

      this.logger.debug(`Macro "${name}" execution completed with result: ${result}`);
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
    const macroDef = this.macros.get(name);
    return macroDef ? macroDef.commands : null;
  }
  
  getMacroDefinition(name: string): MacroDefinition | null {
    return this.macros.get(name) || null;
  }
  
  deleteMacro(name: string): boolean {
    if (!this.macros.has(name)) {
      this.logger.error(`Macro "${name}" not found`);
      return false;
    }
    
    this.macros.delete(name);
    this.logger.debug(`Deleted macro "${name}"`);
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
  
  // Get macro information for debugging
  getMacroInfo(name: string): {
    definition: MacroDefinition | null;
    exists: boolean;
  } {
    const definition = this.macros.get(name) || null;
    return {
      definition,
      exists: !!definition
    };
  }
  
  // List all macros with their source locations
  listMacrosWithSources(): Array<{
    name: string;
    file: string;
    line: number;
    column: number;
  }> {
    return Array.from(this.macros.values()).map(macro => ({
      name: macro.name,
      file: macro.definitionFile,
      line: macro.definitionLine,
      column: macro.definitionColumn
    }));
  }
}
