import { PawScript } from '../pawscript';
import { IPawScriptHost } from '../types';

describe('Debug Simple Cases', () => {
  let pawscript: PawScript;
  let mockHost: IPawScriptHost;
  let capturedResults: any[] = [];

  beforeEach(() => {
    capturedResults = [];
    
    mockHost = {
      getCurrentContext: () => ({}),
      updateStatus: jest.fn(),
      requestInput: jest.fn(),
      render: jest.fn(),
    };

    pawscript = new PawScript({ debug: true }); // Enable debug to see what's happening
    pawscript.setHost(mockHost);
    
    pawscript.registerCommands({
      'set_value': (ctx) => {
        console.log('set_value called with:', ctx.args[0]);
        ctx.setResult(ctx.args[0]);
        console.log('set_value set result to:', ctx.getResult());
        return true;
      },

      'get_value': (ctx) => {
        console.log('GET_VALUE: hasResult:', ctx.hasResult());
        console.log('GET_VALUE: execution state object:', typeof ctx.state);
        if (ctx.hasResult()) {
          console.log('GET_VALUE: found result:', ctx.getResult());
          capturedResults.push(ctx.getResult());
        } else {
          console.log('GET_VALUE: found no result');
          capturedResults.push('<no result>');
        }
        return true;
      }
      
    });
  });

  test('simple result inheritance', () => {
    console.log('=== Starting simple test ===');
    const result = pawscript.execute('set_value "test"; get_value');
    console.log('Execute result:', result);
    console.log('Captured results:', capturedResults);
    
    expect(capturedResults).toContain('test');
  });

  test('single command result setting', () => {
    console.log('=== Starting single command test ===');
    pawscript.execute('set_value "hello"');
    pawscript.execute('get_value');
    console.log('Captured results:', capturedResults);
    
    // This should show '<no result>' because each execute() call gets its own execution state
    expect(capturedResults).toContain('<no result>');
  });

  test('simple brace evaluation', () => {
    console.log('=== Starting brace test ===');
    
    pawscript.registerCommand('return_three', (ctx) => {
      console.log('return_three called');
      ctx.setResult(3);
      console.log('return_three set result to:', ctx.getResult());
      return true;
    });
    
    const result = pawscript.execute('set_value {return_three}; get_value');
    console.log('Brace test result:', result);
    console.log('Captured results:', capturedResults);
    
    // This should show 3 if brace evaluation is working
    expect(capturedResults).toContain(3);
  });

  test('simple macro execution', () => {
    console.log('=== Starting macro test ===');
    
    const defineResult = pawscript.defineMacro('test_macro', 'set_value "from_macro"');
    console.log('Define macro result:', defineResult);
    console.log('Has macro:', pawscript.hasMacro('test_macro'));
    console.log('Macro definition:', pawscript.getMacro('test_macro'));
    
    const result = pawscript.execute('test_macro; get_value');
    console.log('Macro test result:', result);
    console.log('Captured results:', capturedResults);
    
    expect(capturedResults).toContain('from_macro');
  });

});
