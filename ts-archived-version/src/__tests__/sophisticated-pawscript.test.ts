// src/__tests__/sophisticated-pawscript.test.ts
import { PawScript } from '../pawscript';

describe('Sophisticated PawScript Examples', () => {
  let pawscript: PawScript;
  let memory: Map<string, any>;
  let output: string[];

  beforeEach(() => {
    memory = new Map();
    output = [];
    
    pawscript = new PawScript({ debug: false });
    
    // Register arithmetic commands
    pawscript.registerCommands({
      // Basic arithmetic
      'add': (ctx) => {
        const [a, b] = ctx.args;
        // If argument looks like a variable name, get its value from memory
        const valueA = memory.has(a) ? memory.get(a) : Number(a);
        const valueB = memory.has(b) ? memory.get(b) : Number(b);
        const result = Number(valueA) + Number(valueB);
        memory.set('result', result);
        return true;
      },

      'sub': (ctx) => {
        const [a, b] = ctx.args;
        const valueA = memory.has(a) ? memory.get(a) : Number(a);
        const valueB = memory.has(b) ? memory.get(b) : Number(b);
        const result = Number(valueA) - Number(valueB);
        memory.set('result', result);
        return true;
      },   

      'mul': (ctx) => {
        const [a, b] = ctx.args;
        const valueA = memory.has(a) ? memory.get(a) : Number(a);
        const valueB = memory.has(b) ? memory.get(b) : Number(b);
        const result = Number(valueA) * Number(valueB);
        memory.set('result', result);
        return true;
      },

      'div': (ctx) => {
        const [a, b] = ctx.args;
        const valueA = memory.has(a) ? memory.get(a) : Number(a);
        const valueB = memory.has(b) ? memory.get(b) : Number(b);
        if (Number(valueB) === 0) return false;
        const result = Number(valueA) / Number(valueB);
        memory.set('result', result);
        return true;
      },

      // Memory operations
      'set': (ctx) => {
        const [varName, value] = ctx.args;
        // If value is "result", get the actual result value
        const actualValue = value === 'result' ? memory.get('result') : value;
        memory.set(varName, actualValue);
        return true;
      },
      
      'get': (ctx) => {
        const [varName] = ctx.args;
        const value = memory.get(varName) || 0;
        memory.set('result', value);
        return true;
      },
      
      'copy': (ctx) => {
        const [fromVar, toVar] = ctx.args;
        const value = memory.get(fromVar);
        memory.set(toVar, value);
        return true;
      },
      
      // Control flow
      'if_gt': (ctx) => {
       const [a, b] = ctx.args;
       const valueA = memory.has(a) ? memory.get(a) : Number(a);
       const valueB = memory.has(b) ? memory.get(b) : Number(b);
       return Number(valueA) > Number(valueB);
      },

      'if_eq': (ctx) => {
       const [a, b] = ctx.args;
       const valueA = memory.has(a) ? memory.get(a) : Number(a);
       const valueB = memory.has(b) ? memory.get(b) : Number(b);
       return Number(valueA) === Number(valueB);
      },

      'if_lt': (ctx) => {
       const [a, b] = ctx.args;
       const valueA = memory.has(a) ? memory.get(a) : Number(a);
       const valueB = memory.has(b) ? memory.get(b) : Number(b);
       return Number(valueA) < Number(valueB);
      },
      
      // String operations
      'str_concat': (ctx) => {
       const [a, b] = ctx.args;
       const valueA = memory.has(a) ? memory.get(a) : a;
       const valueB = memory.has(b) ? memory.get(b) : b;
       const result = String(valueA) + String(valueB);
       memory.set('result', result);
       return true;
      },

      'str_len': (ctx) => {
       const [str] = ctx.args;
       const actualValue = memory.has(str) ? memory.get(str) : str;
       const result = String(actualValue).length;
       memory.set('result', result);
       return true;
      },

      'str_upper': (ctx) => {
       const [str] = ctx.args;
       const actualValue = memory.has(str) ? memory.get(str) : str;
       const result = String(actualValue).toUpperCase();
       memory.set('result', result);
       return true;
      },

      'str_repeat': (ctx) => {
       const [str, times] = ctx.args;
       const actualStr = memory.has(str) ? memory.get(str) : str;
       const actualTimes = memory.has(times) ? memory.get(times) : times;
       const result = String(actualStr).repeat(Number(actualTimes));
       memory.set('result', result);
       return true;
      },
      
      // Output operations
      'print': (ctx) => {
        const [value] = ctx.args;
        output.push(String(value));
        return true;
      },
      
      'print_var': (ctx) => {
        const [varName] = ctx.args;
        const value = memory.get(varName);
        output.push(`${varName} = ${value}`);
        return true;
      },
      
      // Advanced operations with async simulation
      'async_compute': (ctx) => {
        const [operation, a, b] = ctx.args;
        
        const token = ctx.requestToken((tokenId) => {
          console.log(`Async computation ${operation} was interrupted`);
        });
        
        setTimeout(() => {
          let result;
          const valueA = memory.has(a) ? memory.get(a) : Number(a);
          const valueB = b ? (memory.has(b) ? memory.get(b) : Number(b)) : 0;
          
          switch (operation) {
            case 'power':
              result = Math.pow(valueA, valueB);
              break;
            case 'sqrt':
              result = Math.sqrt(valueA);
              break;
            case 'factorial':
              result = Array.from({length: valueA}, (_, i) => i + 1)
                           .reduce((acc, val) => acc * val, 1);
              break;
            default:
              result = 0;
          }
          
          memory.set('async_result', result);
          ctx.resumeToken(token, true);
        }, 10);
        
        return token;
      },
    });
  });

  describe('Basic Arithmetic and Memory', () => {
    test('should perform basic calculations', () => {
      const result1 = pawscript.execute('add 5, 3');
      expect(result1).toBe(true);
      expect(memory.get('result')).toBe(8);
      
      const result2 = pawscript.execute('mul 4, 7');
      expect(result2).toBe(true);
      expect(memory.get('result')).toBe(28);
    });

    test('should use memory variables', () => {
      pawscript.execute('set x, 10');
      pawscript.execute('set y, 5');
      pawscript.execute('get x; copy result, temp; get y; add temp, result');
      
      expect(memory.get('result')).toBe(15);
    });

  });

  describe('Command Sequences and Conditionals', () => {
    test('should execute conditional sequences', () => {
      // Test: if 10 > 5, then set result to "big", otherwise set to "small" 
      const result1 = pawscript.execute('if_gt 10, 5 & set result, "big"');
      expect(result1).toBe(true);
      expect(memory.get('result')).toBe('big');
      
      const result2 = pawscript.execute('if_gt 3, 10 & set result, "big" | set result, "small"');
      expect(result2).toBe(true);
      expect(memory.get('result')).toBe('small');
    });

    test('should chain complex operations', () => {
      // Calculate: (5 + 3) * (10 - 2) = 8 * 8 = 64
      pawscript.execute('add 5, 3; set a, result; sub 10, 2; set b, result; mul a, b');
      
      expect(memory.get('result')).toBe(64);
    });
  });

  describe('String Manipulation', () => {
    test('should manipulate strings', () => {
      pawscript.execute('str_concat "Hello", " World"');
      expect(memory.get('result')).toBe('Hello World');
      
      pawscript.execute('str_upper "hello world"');
      expect(memory.get('result')).toBe('HELLO WORLD');
      
      pawscript.execute('str_repeat "*", 5');
      expect(memory.get('result')).toBe('*****');
    });

    test('should create formatted output', () => {
      pawscript.execute('set name, "PawScript"; str_concat "Welcome to ", name; set greeting, result');
      pawscript.execute('str_upper greeting; print_var result');
      
      expect(output).toContain('result = WELCOME TO PAWSCRIPT');
    });
  });

  describe('Macros for Complex Operations', () => {
    test('should define and use macros for fibonacci calculation', () => {
      pawscript.defineMacro('fib_step', 
        'copy b, temp; add a, b; set b, result; copy temp, a'
      );
      
      // Calculate 10th fibonacci number (55)
      pawscript.execute('set a, 0; set b, 1; set i, 0'); // Initialize
      
      // Execute fib_step 9 times (to get to F(10))
      pawscript.execute('fib_step; fib_step; fib_step; fib_step; fib_step; fib_step; fib_step; fib_step; fib_step');
      
      expect(memory.get('b')).toBe(55); // F(10) = 55
    });

    test('should create complex string processing macro', () => {
      // Macro to create a bordered text
      pawscript.defineMacro('make_border',
        'str_len text; set width, result; add width, 4; str_repeat "-", result; set border, result; print_var border; str_concat "| ", text; str_concat result, " |"; print_var result; print_var border'
      );
      
      pawscript.execute('set text, "Hello PawScript"');
      pawscript.execute('make_border');
      
      expect(output).toEqual([
        'border = -------------------', // 15 + 4 = 19 dashes (FIXED: was 18)
        'result = | Hello PawScript |',
        'border = -------------------'  // 15 + 4 = 19 dashes (FIXED: was 18)
      ]);
    });
  });

  describe('Async Operations with Token Suspension', () => {
    test('should handle async computation with continuation', (done) => {
      const result = pawscript.execute('async_compute power, 2, 8; print_var async_result');
      
      // Should return a token (string starting with "token_")
      expect(typeof result).toBe('string');
      expect(result).toMatch(/^token_/);
      
      // Wait for async operation to complete
      setTimeout(() => {
        expect(memory.get('async_result')).toBe(256); // 2^8
        expect(output).toContain('async_result = 256');
        done();
      }, 50);
    });

    test('should chain async operations', (done) => {
      // Calculate factorial of 5, then square root of result
      const result = pawscript.execute('async_compute factorial, 5; copy async_result, temp; async_compute sqrt, temp; print_var async_result');
      
      expect(typeof result).toBe('string');
      expect(result).toMatch(/^token_/);
      
      setTimeout(() => {
        // 5! = 120, sqrt(120) ≈ 10.95
        const finalResult = memory.get('async_result');
        expect(finalResult).toBeCloseTo(10.95, 1);
        done();
      }, 100);
    });
  });

  describe('Complex Real-World Example', () => {
    test('should implement a simple calculator with history', () => {
      // Define macros for a calculator with history
      pawscript.defineMacro('calc_add',
        'add a, b; set last_result, result; str_concat a, " + "; str_concat result, b; str_concat result, " = "; str_concat result, last_result; print_var result'
      );
      
      pawscript.defineMacro('calc_mul', 
        'mul a, b; set last_result, result; str_concat a, " × "; str_concat result, b; str_concat result, " = "; str_concat result, last_result; print_var result'
      );
      
      // Perform calculations with formatted output
      pawscript.execute('set a, 15; set b, 7; calc_add');
      pawscript.execute('set a, 12; set b, 4; calc_mul');
      
      expect(output).toEqual([
        'result = 15 + 7 = 22',
        'result = 12 × 4 = 48'
      ]);
      
      expect(memory.get('last_result')).toBe(48);
    });

    test('should implement text processing pipeline', () => {
      // Text processing pipeline: input -> uppercase -> add prefix -> repeat -> output
      pawscript.defineMacro('process_text',
        'str_upper input; set processed, result; str_concat "[PROCESSED] ", processed; set processed, result; str_repeat processed, count; print_var result'
      );
      
      pawscript.execute('set input, "hello world"; set count, 2; process_text');
      
      expect(output).toContain('result = [PROCESSED] HELLO WORLD[PROCESSED] HELLO WORLD');
    });

    test('should demonstrate error handling with conditionals', () => {
      // Safe division with error handling - FIXED: removed incorrect parentheses
      pawscript.defineMacro('safe_divide',
        'if_eq b, 0 & print "Error: Division by zero" | div a, b & print_var result'
      );
      
      // Test normal division
      pawscript.execute('set a, 20; set b, 4; safe_divide');
      expect(output).toContain('result = 5');
      
      // Test division by zero
      output.length = 0; // Clear output
      pawscript.execute('set a, 20; set b, 0; safe_divide');
      expect(output).toContain('Error: Division by zero');
    });
  });

  describe('Performance and Complexity', () => {
    test('should handle deeply nested command sequences', () => {
      // Create a sequence that builds up a number through multiple operations
      const sequence = Array.from({length: 10}, (_, i) => `add result, ${i + 1}`).join('; ');
      
      pawscript.execute(`set result, 0; ${sequence}`);
      
      // Should equal sum of 1+2+3+...+10 = 55
      expect(memory.get('result')).toBe(55);
    });

    test('should manage multiple variables efficiently', () => {
      // Create and manipulate multiple variables
      const commands = [];
      for (let i = 0; i < 20; i++) {
        commands.push(`set var${i}, ${i * 2}`);
      }
      
      pawscript.execute(commands.join('; '));
      
      // Verify all variables were set correctly
      for (let i = 0; i < 20; i++) {
        expect(memory.get(`var${i}`)).toBe(i * 2);
      }
    });
  });

  afterEach(() => {
    // Clean up any active tokens
    const tokenStatus = pawscript.getTokenStatus();
    tokenStatus.tokens.forEach((token: any) => {
      pawscript.forceCleanupToken(token.id);
    });
  });
});
