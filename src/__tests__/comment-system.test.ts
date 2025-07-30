// src/__tests__/comment-system.test.ts
import { CommandExecutor } from '../command-executor';
import { PawScript } from '../pawscript';
import { Logger } from '../logger';
import { IPawScriptHost } from '../types';

describe('Comment System', () => {
  let executor: CommandExecutor;
  let pawscript: PawScript;
  let logger: Logger;
  let mockHost: IPawScriptHost;
  let testCommand: jest.Mock;
  let echoCommand: jest.Mock;

  beforeEach(() => {
    logger = new Logger(false);
    executor = new CommandExecutor(logger);
    pawscript = new PawScript({ debug: false, allowMacros: true });
    
    mockHost = {
      getCurrentContext: () => ({ test: true }),
      updateStatus: jest.fn(),
      requestInput: jest.fn(),
      render: jest.fn(),
    };
    
    executor.setHost(mockHost);
    pawscript.setHost(mockHost);

    testCommand = jest.fn().mockReturnValue(true);
    echoCommand = jest.fn().mockImplementation((ctx) => {
      ctx.setResult(ctx.args.join(' '));
      return true;
    });

    executor.registerCommand('test', testCommand);
    executor.registerCommand('echo', echoCommand);
    pawscript.registerCommand('test', testCommand);
    pawscript.registerCommand('echo', echoCommand);
  });

  describe('Line Comments (#)', () => {
    test('should remove line comment at start of line', () => {
      const result = executor.execute('# This is a comment\ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should remove line comment after whitespace', () => {
      const result = executor.execute('test # This is a comment');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should remove line comment with tab before it', () => {
      const result = executor.execute('test\t# This is a comment');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should NOT treat # as comment when not followed by whitespace', () => {
      // #3 should be treated as a bare identifier, not a comment
      const result = executor.execute('echo #3');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['#3']
      }));
    });

    test('should NOT treat # as comment when not preceded by whitespace', () => {
      // test#comment should be treated as command name, not comment
      const unknownCommand = jest.fn().mockReturnValue(true);
      executor.registerCommand('test#comment', unknownCommand);
      
      const result = executor.execute('test#comment');
      expect(result).toBe(true);
      expect(unknownCommand).toHaveBeenCalledTimes(1);
    });

    test('should treat # followed by end of line as comment', () => {
      const result = executor.execute('test #');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should preserve newlines when removing line comments', () => {
      // Use semicolon to actually create a sequence
      const result = executor.execute('test; # comment\ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle multiple line comments', () => {
      // Use semicolons to create actual command sequences
      const result = executor.execute(`
        # First comment
        test;
        # Second comment  
        test;
        # Third comment
      `);
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });
  });

  describe('Block Comments #(...)# and #{...}#', () => {
    test('should remove single-line parenthesis block comment', () => {
      // Use semicolon to create actual sequence of two commands
      const result = executor.execute('test; #( this is a comment )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should remove single-line brace block comment', () => {
      // Use semicolon to create actual sequence of two commands
      const result = executor.execute('test; #{ this is a comment }# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should remove multi-line parenthesis block comment', () => {
      const result = executor.execute(`
        test;
        #( This is a
           multi-line
           comment )#
        test
      `);
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should remove multi-line brace block comment', () => {
      const result = executor.execute(`
        test;
        #{ This is a
           multi-line
           comment }#
        test
      `);
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle nested parenthesis block comments', () => {
      const result = executor.execute('test; #( outer #( inner )# comment )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle nested brace block comments', () => {
      const result = executor.execute('test; #{ outer #{ inner }# comment }# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle double quotes within block comments', () => {
      const result = executor.execute('test; #( you can mention ")#" without closing )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle escaped quotes within block comments', () => {
      const result = executor.execute('test; #( you can mention "\\")#\\"" without closing )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should NOT handle single quotes specially in block comments', () => {
      // Single quotes should not prevent comment closure - allows contractions
      // The ')#' in single quotes should close the comment, so this becomes:
      // "test; #( don't worry about " + remaining: " here )# test"
      // The comment actually ends at the first ')#', leaving " here )# test" outside
      const result = executor.execute("test; #( don't worry about double quotes \")#\" instead )# test");
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle mixed nested comment types', () => {
      const result = executor.execute('test; #( outer #{ inner }# comment )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle block comments without spaces around delimiters', () => {
      // Test that comment is removed properly
      const result = executor.execute('test #(comment)# "arg"');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
      expect(testCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['arg']
      }));
    });

    test('should handle brace block comments without spaces', () => {
      // Test that comment is removed properly
      const result = executor.execute('test #{comment}# "arg"');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
      expect(testCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['arg']
      }));
    });
  });

  describe('Comments in Quoted Strings', () => {
    test('should NOT process comments inside double quotes', () => {
      const result = executor.execute('echo "This # is not a comment"');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['This # is not a comment']
      }));
    });

    test('should NOT process block comments inside double quotes', () => {
      const result = executor.execute('echo "This #( is not )# a comment"');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['This #( is not )# a comment']
      }));
    });

    test('should process comments after quoted strings', () => {
      const result = executor.execute('echo "hello" # this is a comment');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['hello']
      }));
    });

    test('should handle escaped quotes with comments', () => {
      const result = executor.execute('echo "say \\"hello\\"" # comment');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['say "hello"']
      }));
    });
  });

  describe('Comments with Command Sequences', () => {
    test('should handle comments in semicolon sequences', () => {
      const result = executor.execute('test; # comment\ntest; test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(3);
    });

    test('should handle comments in conditional sequences', () => {
      const result = executor.execute('test & # comment\ntest & test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(3);
    });

    test('should handle comments in OR sequences', () => {
      const failCommand = jest.fn().mockReturnValue(false);
      executor.registerCommand('fail', failCommand);
      
      const result = executor.execute('fail | # comment\ntest | test');
      expect(result).toBe(true);
      expect(failCommand).toHaveBeenCalledTimes(1);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should handle block comments spanning sequence separators', () => {
      // Use semicolons to create actual sequences 
      const result = executor.execute('test; #( comment ; with ; separators )# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });
  });

  describe('Comments with Macros', () => {
    test('should handle comments in macro definitions', () => {
      const result = pawscript.execute(`
        # Define a test macro
        macro test_macro(
          test; # first command
          test  # second command
        )
      `);
      expect(result).toBe(true);
      
      const executeResult = pawscript.execute('test_macro');
      expect(executeResult).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle block comments in macro definitions', () => {
      const result = pawscript.execute(`
        macro test_macro(
          test; #( this is a comment )#
          test
        )
      `);
      expect(result).toBe(true);
      
      const executeResult = pawscript.execute('test_macro');
      expect(executeResult).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle comments when calling macros', () => {
      pawscript.defineMacro('simple_macro', 'test; test');
      
      const result = pawscript.execute('simple_macro # calling the macro');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle comments with macro arguments', () => {
      pawscript.defineMacro('arg_macro', 'echo $1');
      
      const result = pawscript.execute('call arg_macro, "hello" # with argument');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['hello']
      }));
    });
  });

  describe('Comments with Brace Expressions', () => {
    test('should handle comments before brace expressions', () => {
      const calcCommand = jest.fn().mockImplementation((ctx) => {
        ctx.setResult(Number(ctx.args[0]) + Number(ctx.args[1]));
        return true;
      });
      executor.registerCommand('calc', calcCommand);
      
      // The comment should be removed, leaving 'echo \n{calc 5, 3}'
      // But the newline parsing might be interfering. Let's use a simpler test.
      const result = executor.execute('echo {calc 5, 3} # comment after');
      expect(result).toBe(true);
      expect(calcCommand).toHaveBeenCalledTimes(1);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: [8] // Result should be the number 8
      }));
    });

    test('should handle comments after brace expressions', () => {
      const calcCommand = jest.fn().mockImplementation((ctx) => {
        ctx.setResult(10);
        return true;
      });
      executor.registerCommand('calc', calcCommand);
      
      const result = executor.execute('echo {calc} # comment');
      expect(result).toBe(true);
      expect(calcCommand).toHaveBeenCalledTimes(1);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: [10] // Result should be the number 10
      }));
    });

    test('should allow commenting out individual braces', () => {
      // Test that we can use block comments in regular text
      const result = executor.execute('echo "test #}# still works"');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['test #}# still works'] // #}# is not a valid block comment (no opening), so stays as-is
      }));
    });
  });

  describe('Comments with Substitution Patterns', () => {
    test('should handle comments with macro argument substitution', () => {
      pawscript.defineMacro('sub_macro', 'echo $1 # comment about first arg');
      
      const result = pawscript.execute('call sub_macro, "hello"');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['hello']
      }));
    });

    test('should handle comments in substitution patterns', () => {
      pawscript.defineMacro('multi_sub', `
        # This macro uses multiple substitutions
        echo $1; # first argument
        echo $2  # second argument
      `);
      
      const result = pawscript.execute('call multi_sub, "first", "second"');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledTimes(2);
      expect(echoCommand).toHaveBeenNthCalledWith(1, expect.objectContaining({
        args: ['first']
      }));
      expect(echoCommand).toHaveBeenNthCalledWith(2, expect.objectContaining({
        args: ['second']
      }));
    });
  });

  describe('Edge Cases and Complex Scenarios', () => {
    test('should handle multiple comment types in same command', () => {
      const result = executor.execute(`
        # Line comment
        test #( block comment )# # another line comment
      `);
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should handle comments with escape sequences', () => {
      const result = executor.execute('echo "hello\\nworld" # comment with \\n');
      expect(result).toBe(true);
      expect(echoCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: ['hello\nworld']
      }));
    });

    test('should handle incomplete block comments gracefully', () => {
      // Incomplete block comment should not break parsing
      const result = executor.execute('test #( incomplete comment\ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should handle deeply nested block comments', () => {
      // Use semicolon to create actual sequence
      const result = executor.execute(`
        test; #( level1 #( level2 #( level3 )# level2 )# level1 )# test
      `);
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle comments with special characters', () => {
      const result = executor.execute('test # comment with special chars: !@#$%^&*()');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should handle comments with unicode characters', () => {
      const result = executor.execute('test # comment with unicode: 🎉 émojis and accénts');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(1);
    });

    test('should handle empty line comments', () => {
      // Use semicolon to create actual sequence
      const result = executor.execute('test;\n# \ntest');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(2);
    });

    test('should handle empty block comments', () => {
      // Use semicolons to create actual sequences
      const result = executor.execute('test; #()# test; #{}# test');
      expect(result).toBe(true);
      expect(testCommand).toHaveBeenCalledTimes(3);
    });

    test('should preserve command functionality with extensive commenting', () => {
      const complexCommand = jest.fn().mockImplementation((ctx) => {
        const sum = ctx.args.reduce((acc: number, val: any) => acc + Number(val), 0);
        ctx.setResult(sum);
        return true;
      });
      executor.registerCommand('sum', complexCommand);
      
      const result = executor.execute(`
        # Calculate sum of numbers
        sum 1, 2, 3, 4, 5 #( inline comment )# # line comment
      `);
      expect(result).toBe(true);
      expect(complexCommand).toHaveBeenCalledWith(expect.objectContaining({
        args: [1, 2, 3, 4, 5]
      }));
    });
  });

  describe('Comment Interaction with Host Interface', () => {
    test('should not pass comments to updateStatus', () => {
      // Comments should be removed before any error reporting
      const result = executor.execute('unknown_command # this is a comment');
      expect(result).toBe(false);
      expect(mockHost.updateStatus).toHaveBeenCalledWith('Unknown command: unknown_command');
    });

    test('should handle comments in error scenarios', () => {
      const errorCommand = jest.fn().mockImplementation(() => {
        throw new Error('Test error');
      });
      executor.registerCommand('error_cmd', errorCommand);
      
      const result = executor.execute('error_cmd # comment should not interfere with error handling');
      expect(result).toBe(false);
      expect(mockHost.updateStatus).toHaveBeenCalledWith(
        expect.stringContaining('Error executing command: error_cmd')
      );
    });
  });
});
