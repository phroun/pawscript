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
  
  // Copy state from another ExecutionState
  copyFrom(other: ExecutionState): void {
    this.currentResult = other.currentResult;
    this.hasResult = other.hasResult;
  }
  
  // Merge result from child state (child result takes precedence if it has one)
  mergeFromChild(childState: ExecutionState): void {
    if (childState.hasResultValue()) {
      this.currentResult = childState.currentResult;
      this.hasResult = true;
    }
    // If child has no result, keep parent's result unchanged
  }
  
  // Create a completely independent state (no inheritance)
  static createIsolated(): ExecutionState {
    return new ExecutionState();
  }
  
  // Check if this state is equivalent to another
  isEquivalentTo(other: ExecutionState): boolean {
    return this.hasResult === other.hasResult && 
           this.currentResult === other.currentResult;
  }
  
  // Get a string representation for debugging
  toString(): string {
    if (this.hasResult) {
      return `ExecutionState(result: ${JSON.stringify(this.currentResult)})`;
    } else {
      return 'ExecutionState(no result)';
    }
  }
}
