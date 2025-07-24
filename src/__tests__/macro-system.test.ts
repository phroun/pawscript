import { MacroSystem } from '../macro-system';
import { Logger } from '../logger';

describe('MacroSystem', () => {
  let macroSystem: MacroSystem;
  let logger: Logger;

  beforeEach(() => {
    logger = new Logger(false);
    macroSystem = new MacroSystem(logger);
  });

  test('should define macro', () => {
    const result = macroSystem.defineMacro('test_macro', 'command1; command2');
    expect(result).toBe(true);
    expect(macroSystem.hasMacro('test_macro')).toBe(true);
  });

  test('should execute macro', () => {
    const executeCallback = jest.fn().mockReturnValue(true);
    macroSystem.defineMacro('test_macro', 'test_command');
    
    const result = macroSystem.executeMacro('test_macro', executeCallback);
    expect(result).toBe(true);
    expect(executeCallback).toHaveBeenCalledWith('test_command');
  });

  test('should get macro definition', () => {
    macroSystem.defineMacro('test_macro', 'test_command');
    const definition = macroSystem.getMacro('test_macro');
    expect(definition).toBe('test_command');
  });

  test('should handle non-existent macro', () => {
    const definition = macroSystem.getMacro('non_existent');
    expect(definition).toBeNull();
    
    const executeCallback = jest.fn();
    const result = macroSystem.executeMacro('non_existent', executeCallback);
    expect(result).toBe(false);
    expect(executeCallback).not.toHaveBeenCalled();
  });
});
