export class ExecutionState {
  private currentResult: any = undefined;
  private hasResult: boolean = false;
  
  constructor(inheritFrom?: ExecutionState) {
    if (inheritFrom) {
      this.currentResult = inheritFrom.currentResult;
      this.hasResult = inheritFrom.hasResult;
    }
  }
  
  setResult(value: any): void {
    // Handle the special "undefined" bare identifier token
    if (value === "undefined") {
      this.clearResult();
    } else {
      this.currentResult = value;
      this.hasResult = true;
    }
  }
  
  getResult(): any {
    return this.currentResult;
  }
  
  hasResultValue(): boolean {
    return this.hasResult;
  }
  
  clearResult(): void {
    this.currentResult = undefined;
    this.hasResult = false;
  }
  
  // Create a child state that inherits current result
  createChildState(): ExecutionState {
    return new ExecutionState(this);
  }
  
  // Get a snapshot for suspension
  getSnapshot(): { result: any; hasResult: boolean } {
    return {
      result: this.currentResult,
      hasResult: this.hasResult
    };
  }
  
  // Restore from a snapshot during resumption
  restoreSnapshot(snapshot: { result: any; hasResult: boolean }): void {
    this.currentResult = snapshot.result;
    this.hasResult = snapshot.hasResult;
  }
}
