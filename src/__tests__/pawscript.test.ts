import { PawScript } from '../pawscript';
import { IPawScriptHost } from '../types';

describe('PawScript', () => {
  let pawscript: PawScript;
  let mockHost: IPawScriptHost;
  let mockCommands: Record<string, jest.Mock>;

  beforeEach(() => {
    mockCommands = {
      test_sync: jest.fn().mockReturnValue(true),
      test_fail: jest.fn().mockReturnValue(false),
      test_async: jest.fn().mockReturnValue('token_1'),
    };

    mockHost = {
      getCurrentContext: jest.fn().mockReturnValue({ cursor: { x: 0, y: 0 } }),
      updateStatus: jest.fn(),
      requestInput: jest.fn().mockResolvedValue('test input'),
      render: jest.fn(),
      createWindow: jest.fn().mockReturnValue('window-1'),
      removeWindow: jest.fn(),
      saveState: jest.fn().mockReturnValue({}),
      restoreState: jest.fn(),
      emit: jest.fn(),
      on: jest.fn(),
    };

    pawscript = new PawScript({ debug: false });
    pawscript.setHost(mockHost);
    pawscript.registerCommands(mockCommands);
  });

  describe('Basic Command Execution', () => {
    test('should execute simple command', () => {
      const result = pawscript.execute('test_sync');
      expect(result).toBe(true);
      expect(mockCommands.test_sync).toHaveBeenCalledWith({
        host: mockHost,
        args: [],
        state: { cursor: { x: 0, y: 0 } },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
        // NEW: Result management methods
        setResult: expect.any(Function),
        getResult: expect.any(Function),
        hasResult: expect.any(Function),
        clearResult: expect.any(Function),
      });
    });

    test('should execute command with arguments', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      pawscript.registerCommand('test_args', testCommand);
      
      const result = pawscript.execute("test_args 'hello', 42, true");
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith({
        host: mockHost,
        args: ['hello', 42, true],
        state: { cursor: { x: 0, y: 0 } },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
        // NEW: Result management methods
        setResult: expect.any(Function),
        getResult: expect.any(Function),
        hasResult: expect.any(Function),
        clearResult: expect.any(Function),
      });
    });

    test('should handle unknown command', () => {
      const result = pawscript.execute('unknown_command');
      expect(result).toBe(false);
      expect(mockHost.updateStatus).toHaveBeenCalledWith('Unknown command: unknown_command');
    });
  });

  describe('Command Sequences', () => {
    test('should execute sequence with semicolon', () => {
      const result = pawscript.execute('test_sync; test_sync');
      expect(result).toBe(true);
      expect(mockCommands.test_sync).toHaveBeenCalledTimes(2);
    });

    test('should execute conditional with ampersand', () => {
      const result = pawscript.execute('test_sync & test_sync');
      expect(result).toBe(true);
      expect(mockCommands.test_sync).toHaveBeenCalledTimes(2);
    });

    test('should stop conditional on failure', () => {
      const result = pawscript.execute('test_fail & test_sync');
      expect(result).toBe(false);
      expect(mockCommands.test_fail).toHaveBeenCalledTimes(1);
      expect(mockCommands.test_sync).not.toHaveBeenCalled();
    });

    test('should execute OR with pipe', () => {
      const result = pawscript.execute('test_fail | test_sync');
      expect(result).toBe(true);
      expect(mockCommands.test_fail).toHaveBeenCalledTimes(1);
      expect(mockCommands.test_sync).toHaveBeenCalledTimes(1);
    });

    test('should stop OR on success', () => {
      const result = pawscript.execute('test_sync | test_sync');
      expect(result).toBe(true);
      expect(mockCommands.test_sync).toHaveBeenCalledTimes(1);
    });
  });

  describe('Token Management (Paws Feature)', () => {
    test('should handle async command tokens', () => {
      const result = pawscript.execute('test_async');
      expect(typeof result).toBe('string');
      expect(result).toMatch(/^token_/);
    });

    test('should resume token with result', () => {
      const tokenId = pawscript.requestToken();
      const status = pawscript.getTokenStatus();
      
      expect(status.activeCount).toBe(1);
      expect(status.tokens[0].id).toBe(tokenId);

      const result = pawscript.resumeToken(tokenId, true);
      expect(result).toBe(true);

      const newStatus = pawscript.getTokenStatus();
      expect(newStatus.activeCount).toBe(0);
    });

    test('should cleanup token on timeout', (done) => {
      const cleanupCallback = jest.fn();
      const tokenId = pawscript.requestToken(cleanupCallback, undefined, 100);
      
      setTimeout(() => {
        expect(cleanupCallback).toHaveBeenCalledWith(tokenId);
        done();
      }, 150);
    });
  });

  describe('Macro System', () => {
    test('should define and execute macro', () => {
      const result1 = pawscript.defineMacro('test_macro', 'test_sync; test_sync');
      expect(result1).toBe(true);

      const result2 = pawscript.execute('test_macro');
      expect(result2).toBe(true);
      expect(mockCommands.test_sync).toHaveBeenCalledTimes(2);
    });

    test('should list macros', () => {
      pawscript.defineMacro('macro1', 'test_sync');
      pawscript.defineMacro('macro2', 'test_fail');
      
      const macros = pawscript.listMacros();
      expect(macros).toContain('macro1');
      expect(macros).toContain('macro2');
      expect(macros.length).toBe(2);
    });

    test('should delete macro', () => {
      pawscript.defineMacro('temp_macro', 'test_sync');
      expect(pawscript.hasMacro('temp_macro')).toBe(true);
      
      const result = pawscript.deleteMacro('temp_macro');
      expect(result).toBe(true);
      expect(pawscript.hasMacro('temp_macro')).toBe(false);
    });

    test('should clear all macros', () => {
      pawscript.defineMacro('macro1', 'test_sync');
      pawscript.defineMacro('macro2', 'test_fail');
      
      const count = pawscript.clearMacros();
      expect(count).toBe(2);
      expect(pawscript.listMacros().length).toBe(0);
    });
  });

  describe('Syntactic Sugar', () => {
    test('should transform identifier parentheses syntax', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      pawscript.registerCommand('test_sugar', testCommand);
      
      const result = pawscript.execute("test_sugar hello(world)");
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith({
        host: mockHost,
        args: ['hello', 'world'],
        state: { cursor: { x: 0, y: 0 } },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
        // NEW: Result management methods
        setResult: expect.any(Function),
        getResult: expect.any(Function),
        hasResult: expect.any(Function),
        clearResult: expect.any(Function),
      });
    });
  });

  describe('Configuration', () => {
    test('should respect debug configuration', () => {
      const debugPawScript = new PawScript({ debug: true });
      const spy = jest.spyOn(console, 'log').mockImplementation();
      
      debugPawScript.setHost(mockHost);
      debugPawScript.registerCommand('test', mockCommands.test_sync);
      debugPawScript.execute('test');
      
      expect(spy).toHaveBeenCalled();
      spy.mockRestore();
    });

    test('should disable macros when configured', () => {
      const noMacroPawScript = new PawScript({ allowMacros: false });
      noMacroPawScript.setHost(mockHost);
      
      const result1 = noMacroPawScript.defineMacro('test', 'test_sync');
      expect(result1).toBe(false);
      
      const result2 = noMacroPawScript.executeMacro('test');
      expect(result2).toBe(false);
    });
  });
});
