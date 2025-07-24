export interface IPawScriptHost {
  // Core application state
  getCurrentContext(): any;
  updateStatus(message: string): void;
  requestInput(prompt: string, defaultValue?: string): Promise<string>;
  
  // UI operations
  render(): void;
  createWindow?(options: any): string;
  removeWindow?(id: string): void;
  
  // State management
  saveState?(): any;
  restoreState?(snapshot: any): void;
  
  // Event handling
  emit?(event: string, ...args: any[]): void;
  on?(event: string, handler: Function): void;
}

export interface PawScriptContext {
  // Host application reference
  host: IPawScriptHost;
  
  // Command arguments
  args: any[];
  
  // Current state info (provided by host)
  state: any;
  
  // Utility methods
  requestToken(cleanup?: (tokenId: string) => void): string;
  resumeToken(tokenId: string, result: boolean): void;
}

export type PawScriptHandler = (context: PawScriptContext) => boolean | string;

export interface PawScriptConfig {
  // Debug settings
  debug?: boolean;
  
  // Timeout settings
  defaultTokenTimeout?: number;
  
  // Syntax features
  enableSyntacticSugar?: boolean;
  allowMacros?: boolean;
  
  // Command parsing
  commandSeparators?: {
    sequence: string;    // default: ';'
    conditional: string; // default: '&'
    alternative: string; // default: '|'
  };
}

export interface TokenData {
  commandSequence: CommandSequence | null;
  parentToken: string | null;
  children: Set<string>;
  cleanupCallback: ((tokenId: string) => void) | null;
  timeoutId: NodeJS.Timeout | null;
  chainedToken: string | null;
  timestamp: number;
}

export interface CommandSequence {
  type: 'sequence' | 'conditional' | 'or';
  remainingCommands: string[];
  currentIndex: number;
  totalCommands: number;
  originalCommand: string;
  timestamp: number;
}
