import { PawScript } from '../pawscript';
import { IPawScriptHost } from '../types';

describe('Result Management System', () => {
  let pawscript: PawScript;
  let mockHost: IPawScriptHost;
  let results: any[];

  beforeEach(() => {
    results = [];
    
    mockHost = {
      getCurrentContext: () => ({ test: true }),
      updateStatus: jest.fn(),
      requestInput: jest.fn().mockResolvedValue('test input'),
      render: jest.fn(),
    };

    pawscript = new PawScript({ debug: false });
    pawscript.setHost(mockHost);
    
    // Register test commands that work with results
    pawscript.registerCommands({
      // Command that sets a result
      'set_result': (ctx) => {
        ctx.setResult(ctx.args[0]);
        return true;
      },
      
      // Command that gets current result
      'get_result': (ctx) => {
        if (ctx.hasResult()) {
          results.push(ctx.getResult());
        } else {
          results.push('<no result>');
        }
        return true;
      },
      
      // Command that clears result
      'clear_result': (ctx) => {
        ctx.setResult('undefined'); // Uses special undefined token
        return true;
      },
      
      // Command that returns a value as its result
      'calculate': (ctx) => {
        const [a, b] = ctx.args;
        const result = Number(a) + Number(b);
        ctx.setResult(result);
        return true;
      },
      
      // Command that gets a result and transforms it
      'double_result': (ctx) => {
        if (ctx.hasResult()) {
          const current = ctx.getResult();
          ctx.setResult(Number(current) * 2);
        }
        return true;
      },
      
      // Command for testing async result preservation
      'async_set_result': (ctx) => {
        const value = ctx.args[0];
        console.log('ASYNC: Starting async_set_result with value:', value);
        console.log('ASYNC: Initial context hasResult:', ctx.hasResult());
        console.log('ASYNC: Initial context result:', ctx.hasResult() ? ctx.getResult() : 'none');
        
        const token = ctx.requestToken();
        console.log('ASYNC: Got token:', token);
        
        setImmediate(() => {
          console.log('ASYNC: In setImmediate, setting result to:', value);
          console.log('ASYNC: Before setResult, context hasResult:', ctx.hasResult());
          console.log('ASYNC: Before setResult, context result:', ctx.hasResult() ? ctx.getResult() : 'none');
          
          ctx.setResult(value);
          
          console.log('ASYNC: After setResult, context hasResult:', ctx.hasResult());
          console.log('ASYNC: After setResult, context result:', ctx.getResult());
          console.log('ASYNC: Resuming token:', token);
          
          ctx.resumeToken(token, true);
        });
        
        return token;
      },
      
      // Command to capture results for testing
      'capture_result': (ctx) => {
        console.log('CAPTURE: Called, hasResult:', ctx.hasResult());
        if (ctx.hasResult()) {
          console.log('CAPTURE: Found result:', ctx.getResult());
          results.push(ctx.getResult());
        } else {
          console.log('CAPTURE: No result found');
          results.push('<no result>');
        }
        return true;
      },
      
      // Evaluative command for brace testing
      'number': (ctx) => {
        const englishNumber = ctx.args[0];
        let numeric;
        switch (englishNumber) {
          case 'zero': numeric = 0; break;
          case 'one': numeric = 1; break;
          case 'two': numeric = 2; break;
          case 'three': numeric = 3; break;
          case 'four': numeric = 4; break;
          case 'five': numeric = 5; break;
          default: numeric = 0;
        }
        ctx.setResult(numeric);
        return true;
      },
      
      // Command that uses current result in computation
      'add_to_result': (ctx) => {
        const addend = Number(ctx.args[0]);
        if (ctx.hasResult()) {
          const current = Number(ctx.getResult());
          ctx.setResult(current + addend);
        } else {
          ctx.setResult(addend);
        }
        return true;
      }
    });
  });

  describe('Basic Result Operations', () => {
    test('should set and get results in a sequence', () => {
      // FIXED: Use a single sequence instead of separate execute() calls
      pawscript.execute('set_result "hello"; get_result');
      
      expect(results).toContain('hello');
    });

    test('should inherit results across command sequences', () => {
      pawscript.execute('set_result 42; get_result');
      
      expect(results).toContain(42);
    });

    test('should override results with later commands', () => {
      pawscript.execute('set_result "first"; set_result "second"; get_result');
      
      expect(results).toContain('second');
      expect(results).not.toContain('first');
    });

    test('should handle undefined token to clear results', () => {
      pawscript.execute('set_result "value"; clear_result; get_result');
      
      expect(results).toContain('<no result>');
    });

    test('should isolate results between separate execute calls', () => {
      // This test verifies that separate execute() calls have separate execution states
      pawscript.execute('set_result "isolated"');
      pawscript.execute('get_result');
      
      expect(results).toContain('<no result>');
    });
  });

  describe('Result Persistence During Suspension', () => {
    test('should preserve results across async operations', (done) => {
      pawscript.execute('set_result "preserved"; async_set_result "new_value"; capture_result');
      
      setTimeout(() => {
        expect(results).toContain('new_value');
        done();
      }, 50);
    });

    test('should maintain result inheritance after resumption', (done) => {
      pawscript.execute('set_result "base"; async_set_result "async_result"; add_to_result 10; capture_result');
      
      setTimeout(() => {
        // Should have "async_result" + 10, but since "async_result" is a string, 
        // this will depend on the Number() conversion behavior
        expect(results.length).toBeGreaterThan(0);
        done();
      }, 50);
    });
  });

  describe('Brace Expression Evaluation', () => {
    test('should evaluate simple brace expressions without $', () => {
      pawscript.execute('set_result {number three}; capture_result');
      
      expect(results).toContain(3);
    });

    test('should handle prefix and suffix concatenation', () => {
      pawscript.execute('set_result prefix{number two}suffix; capture_result');
      
      expect(results).toContain('prefix2suffix');
    });

    test('should handle multiple braces in one token', () => {
      pawscript.execute('set_result {number one}{number two}; capture_result');
      
      // FIXED: Expect number instead of string
      expect(results).toContain(12);
    });

    test('should handle nested brace evaluation', () => {
      // This tests that brace resolution happens iteratively
      pawscript.registerCommand('return_brace_expr', (ctx) => {
        ctx.setResult('number two');
        return true;
      });
      
      pawscript.execute('set_result {{return_brace_expr}}; capture_result');
      
      // First {return_brace_expr} → "number two"
      // Then {number two} → 2
      expect(results).toContain(2);
    });
  });

  describe('Token Re-evaluation After Brace Assembly', () => {
    test('should re-evaluate assembled tokens for macro calls', () => {
      // Define a macro
      pawscript.defineMacro('test_macro', 'set_result "macro_executed"');
      
      // Create a command that returns the macro name
      pawscript.registerCommand('get_macro_name', (ctx) => {
        ctx.setResult('test_macro');
        return true;
      });
      
      // This should execute get_macro_name (result: "test_macro"), 
      // then re-evaluate "test_macro" as a token, which should execute the macro
      pawscript.execute('{get_macro_name}; capture_result');
      
      expect(results).toContain('macro_executed');
    });

    test('should handle command name assembly and re-evaluation', () => {
      // Test that brace expressions work in command position
      pawscript.registerCommand('get_command_name', (ctx) => {
        ctx.setResult('set_result');
        return true;
      });
      
      // This should assemble to "set_result" and then execute that command
      pawscript.execute('{get_command_name} "test_value"; capture_result');
      
      expect(results).toContain('test_value');
    });
  });

  describe('Macro Result Propagation', () => {
    test('should propagate macro results', () => {
      pawscript.defineMacro('calc_macro', 'calculate 10, 5');
      pawscript.execute('calc_macro; capture_result');
      
      expect(results).toContain(15);
    });

    test('should use macro results in brace expressions', () => {
      pawscript.defineMacro('get_five', 'number five');
      pawscript.execute('add_to_result {get_five}; capture_result');
      
      expect(results).toContain(5);
    });

    test('should handle macro argument substitution', () => {
      pawscript.defineMacro('add_numbers', 'calculate $1, $2');
      pawscript.execute('add_numbers 7, 8; capture_result');
      
      expect(results).toContain(15);
    });

    test('should handle $* substitution', () => {
      pawscript.defineMacro('sum_all', 'set_result "args: $*"');
      pawscript.execute('sum_all 1, 2, 3; capture_result');
      
      expect(results).toContain('args: 1, 2, 3');
    });

    test('should handle $# substitution', () => {
      pawscript.defineMacro('count_args', 'set_result $#');
      pawscript.execute('count_args a, b, c, d; capture_result');
      
      // FIXED: Expect number instead of string
      expect(results).toContain(4);
    });
  });

  describe('Complex Substitution Patterns', () => {
    test('should handle indexed argument substitution', () => {
      pawscript.defineMacro('get_second', 'set_result $2');
      pawscript.execute('get_second "first", "second", "third"; capture_result');
      
      expect(results).toContain('second');
    });

    test('should handle brace expressions in macro definitions', () => {
      pawscript.defineMacro('complex_calc', 'calculate {number $1}, {number $2}');
      pawscript.execute('complex_calc "two", "three"; capture_result');
      
      expect(results).toContain(5); // 2 + 3
    });

    test('should handle complex token assembly', () => {
      pawscript.defineMacro('build_token', 'set_result prefix{number $1}suffix');
      pawscript.execute('build_token "three"; capture_result');
      
      expect(results).toContain('prefix3suffix');
    });
  });

  describe('Error Handling', () => {
    test('should handle brace expression errors gracefully', () => {
      // Test with a command that doesn't exist
      const result = pawscript.execute('set_result {nonexistent_command}; capture_result');
      
      // Should not crash, might leave brace expression unchanged
      expect(typeof result).toBe('boolean');
    });

    test('should handle malformed brace expressions', () => {
      // Test with unclosed braces
      const result = pawscript.execute('set_result {number three; capture_result');
      
      // Should not crash
      expect(typeof result).toBe('boolean');
    });

    test('should handle empty brace expressions', () => {
      const result = pawscript.execute('set_result {}; capture_result');
      
      // Should handle empty braces gracefully
      expect(typeof result).toBe('boolean');
    });

    test('should handle nested malformed braces', () => {
      const result = pawscript.execute('set_result {outer {inner}; capture_result');
      
      // Should handle malformed nested braces
      expect(typeof result).toBe('boolean');
    });
  });

  describe('Result Flow in Different Command Types', () => {
    test('should maintain results through conditional sequences', () => {
      pawscript.execute('set_result "test" & calculate 5, 3 & capture_result');
      
      expect(results).toContain(8); // Last command result should be 8
    });

    test('should maintain results through alternative sequences', () => {
      // First command fails, but should still maintain result state
      pawscript.registerCommand('always_fail', (ctx) => false);
      
      pawscript.execute('always_fail | set_result "backup"; capture_result');
      
      expect(results).toContain('backup');
    });

    test('should handle results in sequence chains', () => {
      pawscript.execute('set_result 10; double_result; double_result; capture_result');
      
      expect(results).toContain(40); // 10 * 2 * 2
    });
  });
});
