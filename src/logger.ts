export class Logger {
  private enabled: boolean;
  
  constructor(enabled: boolean = false) {
    this.enabled = enabled;
  }
  
  debug(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.log(`[PawScript DEBUG] ${message}`, ...args);
    }
  }
  
  warn(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.warn(`[PawScript WARN] ${message}`, ...args);
    }
  }
  
  error(message: string, ...args: any[]): void {
    if (this.enabled) {
      console.error(`[PawScript ERROR] ${message}`, ...args);
    }
  }
  
  setEnabled(enabled: boolean): void {
    this.enabled = enabled;
  }
}
