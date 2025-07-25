// src/__tests__/syntactic-sugar.test.ts
import { PawScript } from '../pawscript';
import { IPawScriptHost } from '../types';

describe('Syntactic Sugar', () => {
  let pawscript: PawScript;
  let mockHost: IPawScriptHost;

  beforeEach(() => {
    mockHost = {
      getCurrentContext: jest.fn().mockReturnValue({}),
      updateStatus: jest.fn(),
      requestInput: jest.fn(),
      render: jest.fn()
    };

    pawscript = new PawScript({ 
      debug: true, // Enable debug to see what's happening
      allowMacros: true 
    });
    pawscript.setHost(mockHost);
  });

  test('should transform identifier() syntax for macro command', () => {
    // Mock the macro command to capture what arguments it receives
    const mockMacroCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('macro', mockMacroCommand);
    
    // Execute the syntactic sugar version
    const result = pawscript.execute("macro doIt(print 1)");
    
    expect(result).toBe(true);
    expect(mockMacroCommand).toHaveBeenCalledTimes(1);
    
    // Check that the arguments were parsed correctly
    const callArgs = mockMacroCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(2);
    expect(callArgs.args[0]).toBe('doIt');
    expect(callArgs.args[1]).toBe('print 1');
  });

  test('should transform complex macro with multiple commands', () => {
    const mockMacroCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('macro', mockMacroCommand);
    
    const result = pawscript.execute("macro saveAndExit(save_file; update_status 'Saved'; exit)");
    
    expect(result).toBe(true);
    expect(mockMacroCommand).toHaveBeenCalledTimes(1);
    
    const callArgs = mockMacroCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(2);
    expect(callArgs.args[0]).toBe('saveAndExit');
    expect(callArgs.args[1]).toBe("save_file; update_status 'Saved'; exit");
  });

  test('should transform macro with arguments containing quotes', () => {
    const mockMacroCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('macro', mockMacroCommand);
    
    const result = pawscript.execute("macro greet(echo 'Hello $1!')");
    
    expect(result).toBe(true);
    expect(mockMacroCommand).toHaveBeenCalledTimes(1);
    
    const callArgs = mockMacroCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(2);
    expect(callArgs.args[0]).toBe('greet');
    expect(callArgs.args[1]).toBe("echo 'Hello $1!'");
  });

  test('should NOT transform regular command with parentheses', () => {
    // Register a test command that expects parenthetical content as a single argument
    const testCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('test', testCommand);
    
    const result = pawscript.execute("test (some grouped content)");
    
    expect(result).toBe(true);
    expect(testCommand).toHaveBeenCalledTimes(1);
    
    const callArgs = testCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(1);
    expect(callArgs.args[0]).toBe('some grouped content');
  });

  test('should work with the built-in macro commands', () => {
    // Don't override the built-in macro command, use it directly
    const result = pawscript.execute("macro testMacro(echo 'test')");
    
    expect(result).toBe(true);
    
    // Verify the macro was actually defined
    expect(pawscript.hasMacro('testMacro')).toBe(true);
    expect(pawscript.getMacro('testMacro')).toBe("echo 'test'");
  });

  test('should execute defined macro with arguments', () => {
    // Mock the echo command FIRST, before defining the macro
    const echoCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('echo', echoCommand);
    
    // First define a macro using syntactic sugar
    const macroResult = pawscript.execute("macro greetUser(echo 'Hello $1!')");
    expect(macroResult).toBe(true);
    expect(pawscript.hasMacro('greetUser')).toBe(true);
    
    // Execute the macro with a single argument (no comma needed for single arg)
    const result = pawscript.execute("greetUser 'Alice'");
    
    expect(result).toBe(true);
    expect(echoCommand).toHaveBeenCalledTimes(1);
    
    const callArgs = echoCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(1);
    expect(callArgs.args[0]).toBe('Hello Alice!');
  });

  test('should execute macro using call command with arguments', () => {
    // Mock the echo command FIRST
    const echoCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('echo', echoCommand);
    
    // Define macro
    const macroResult = pawscript.execute("macro greetUser(echo 'Hello $1!')");
    expect(macroResult).toBe(true);
    
    // Execute using call command with correct comma syntax
    const result = pawscript.execute("call greetUser, 'Bob'");
    
    expect(result).toBe(true);
    expect(echoCommand).toHaveBeenCalledTimes(1);
    
    const callArgs = echoCommand.mock.calls[0][0];
    expect(callArgs.args).toHaveLength(1);
    expect(callArgs.args[0]).toBe('Hello Bob!');
  });

  test('should debug argument parsing for direct macro calls', () => {
    // Mock the echo command FIRST
    const echoCommand = jest.fn().mockReturnValue(true);
    pawscript.registerCommand('echo', echoCommand);
    
    // Define a macro
    pawscript.execute("macro greetUser(echo 'Hello $1!')");
    
    // Capture console output
    const consoleSpy = jest.spyOn(console, 'log').mockImplementation();
    
    // Test direct macro execution
    console.log('=== Testing direct macro execution ===');
    const result1 = pawscript.execute("greetUser 'Alice'");
    console.log('Direct macro result:', result1);
    
    // Test call command
    console.log('=== Testing call command ===');
    const result2 = pawscript.execute("call greetUser, 'Bob'");
    console.log('Call command result:', result2);
    
    // Show debug logs
    const debugLogs = consoleSpy.mock.calls
      .filter(call => call[0] && call[0].includes('[PawScript DEBUG]'))
      .map(call => call.join(' '));
    
    console.log('All debug logs:');
    debugLogs.forEach(log => console.log(log));
    
    consoleSpy.mockRestore();
    
    // Both should work
    expect(result1).toBe(true);
    expect(result2).toBe(true);
  });
});
