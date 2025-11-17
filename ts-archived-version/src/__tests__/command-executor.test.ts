// src/__tests__/command-executor.test.ts
import { CommandExecutor } from '../command-executor';
import { Logger } from '../logger';

describe('CommandExecutor', () => {
  let executor: CommandExecutor;
  let logger: Logger;

  beforeEach(() => {
    logger = new Logger(true);
    executor = new CommandExecutor(logger);
    
    // Register script_error command for error handling
    executor.registerCommand('script_error', (ctx) => {
      console.error(`[SCRIPT ERROR] ${ctx.args[0]}`);
      return true;
    });
  });

  describe('Command Parsing', () => {
    test('should parse command without arguments', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute('test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: [],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });

    test('should parse quoted strings', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      executor.execute("test 'hello world', \"quoted string\"");
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['hello world', 'quoted string'],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });

    test('should parse numbers and booleans', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      executor.execute('test 42, 3.14, true, false');
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: [42, 3.14, true, false],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });

    test('should handle parentheses grouping', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      // Debug: Let's see what the command parsing produces
      const parsed = (executor as any).parseCommand('test (grouped content)');
      console.log('Parsed command:', parsed);
      
      const result = executor.execute('test (grouped content)');
      console.log('Execution result:', result);
      console.log('Test command called:', testCommand.mock.calls.length, 'times');
      
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['grouped content'],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });
  });

  describe('Character Splitting', () => {
    test('should respect quotes when splitting', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute("test 'hello; world'");
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['hello; world'],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });

    test('should respect parentheses when splitting', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      // Debug logging
      const parsed = (executor as any).parseCommand('test (command1; command2)');
      console.log('Parsed command for parentheses splitting:', parsed);
      
      const result = executor.execute('test (command1; command2)');
      console.log('Parentheses splitting result:', result);
      console.log('Command called times:', testCommand.mock.calls.length);
      
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['command1; command2'],
          requestToken: expect.any(Function),
          resumeToken: expect.any(Function),
          setResult: expect.any(Function),
          getResult: expect.any(Function),
          hasResult: expect.any(Function),
          clearResult: expect.any(Function),
          position: expect.any(Object),
        })
      );
    });
  });

  describe('Newline-Aware Parsing', () => {
    test('should handle commands separated by newlines', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute('test\ntest\ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(3);
    });

    test('should handle operator continuation across lines', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute('test &\ntest &\ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(3);
    });

    test('should ignore newlines inside parentheses', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute('test (\nmulti\nline\ncontent\n)');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['\nmulti\nline\ncontent\n']
        })
      );
    });

    test('should ignore newlines inside braces', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      const calcCommand = jest.fn().mockImplementation((ctx) => {
        ctx.setResult('calculated');
        return true;
      });
      executor.registerCommand('test', testCommand);
      executor.registerCommand('calc', calcCommand);
      
      const result = executor.execute('test {\ncalc\n}');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          args: ['calculated']
        })
      );
    });
  });

  describe('Error Handling with Position Tracking', () => {
    test('should report position information for unknown commands', () => {
      const consoleSpy = jest.spyOn(console, 'error').mockImplementation();
      
      const result = executor.execute('unknown_command');
      expect(result).toBe(false);
      expect(consoleSpy).toHaveBeenCalledWith(expect.stringContaining('[SCRIPT ERROR] Unknown command: unknown_command'));
      
      consoleSpy.mockRestore();
    });

    test('should handle command execution errors with position tracking', () => {
      const consoleSpy = jest.spyOn(console, 'error').mockImplementation();
      
      const errorCommand = jest.fn().mockImplementation(() => {
        throw new Error('Test error');
      });
      executor.registerCommand('error_cmd', errorCommand);
      
      const result = executor.execute('error_cmd');
      expect(result).toBe(false);
      expect(consoleSpy).toHaveBeenCalledWith(
        expect.stringContaining('[SCRIPT ERROR]')
      );
      expect(consoleSpy).toHaveBeenCalledWith(
        expect.stringContaining('Error executing command: error_cmd')
      );
      
      consoleSpy.mockRestore();
    });
  });
});
