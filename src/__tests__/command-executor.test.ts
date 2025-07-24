// src/__tests__/command-executor.test.ts
import { CommandExecutor } from '../command-executor';
import { Logger } from '../logger';

describe('CommandExecutor', () => {
  let executor: CommandExecutor;
  let logger: Logger;
  let mockHost: any;

  beforeEach(() => {
    logger = new Logger(true);
    executor = new CommandExecutor(logger);
    
    mockHost = {
      getCurrentContext: () => ({ test: true }),
      updateStatus: jest.fn(),
      requestInput: jest.fn(),
      render: jest.fn(),
    };
    
    executor.setHost(mockHost);
  });

  describe('Command Parsing', () => {
    test('should parse command without arguments', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute('test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: [],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
    });

    test('should parse quoted strings', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      executor.execute("test 'hello world', \"quoted string\"");
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: ['hello world', 'quoted string'],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
    });

    test('should parse numbers and booleans', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      executor.execute('test 42, 3.14, true, false');
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: [42, 3.14, true, false],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
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
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: ['grouped content'],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
    });
  });

  describe('Character Splitting', () => {
    test('should respect quotes when splitting', () => {
      const testCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test', testCommand);
      
      const result = executor.execute("test 'hello; world'");
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: ['hello; world'],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
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
      expect(testCommand).toHaveBeenCalledWith({
        host: expect.any(Object),
        args: ['command1; command2'],
        state: { test: true },
        requestToken: expect.any(Function),
        resumeToken: expect.any(Function),
      });
    });
  });
});
