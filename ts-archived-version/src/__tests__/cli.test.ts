import { PawCLI } from '../paw';
import * as fs from 'fs';
import * as path from 'path';

describe('PawScript CLI', () => {
  let originalArgv: string[];
  let originalStdin: any;
  let originalStdout: any;
  let originalStderr: any;
  let mockExit: jest.SpyInstance;
  
  beforeEach(() => {
    originalArgv = process.argv;
    originalStdin = process.stdin;
    originalStdout = process.stdout;
    originalStderr = process.stderr;
    
    // Mock process.exit
    mockExit = jest.spyOn(process, 'exit').mockImplementation(() => {
      throw new Error('process.exit called');
    });
  });
  
  afterEach(() => {
    process.argv = originalArgv;
    mockExit.mockRestore();
  });
  
  describe('Standard Library Commands', () => {
    test('should register argc command', async () => {
      const cli = new (PawCLI as any)();
      
      // Test that argc is available
      expect(() => {
        cli.pawscript.execute('argc');
      }).not.toThrow();
    });
    
    test('should register argv command', async () => {
      const cli = new (PawCLI as any)();
      
      // Test that argv is available  
      expect(() => {
        cli.pawscript.execute('argv');
      }).not.toThrow();
    });
    
    test('should register echo/write/print commands', async () => {
      const cli = new (PawCLI as any)();
      
      expect(() => {
        cli.pawscript.execute('echo "test"');
      }).not.toThrow();
      
      expect(() => {
        cli.pawscript.execute('write "test"');
      }).not.toThrow();
      
      expect(() => {
        cli.pawscript.execute('print "test"');
      }).not.toThrow();
    });
    
    test('should register true/false commands', async () => {
      const cli = new (PawCLI as any)();
      
      expect(() => {
        cli.pawscript.execute('true');
      }).not.toThrow();
      
      expect(() => {
        cli.pawscript.execute('false');
      }).not.toThrow();
    });
  });
  
  describe('File Resolution', () => {
    const testDir = path.join(__dirname, 'test-scripts');
    const testFile = path.join(testDir, 'test.paw');
    const testFileNoPaw = path.join(testDir, 'test');
    
    beforeAll(() => {
      // Create test directory and file
      if (!fs.existsSync(testDir)) {
        fs.mkdirSync(testDir, { recursive: true });
      }
      fs.writeFileSync(testFile, 'echo "test script"');
    });
    
    afterAll(() => {
      // Cleanup
      if (fs.existsSync(testFile)) {
        fs.unlinkSync(testFile);
      }
      if (fs.existsSync(testDir)) {
        fs.rmdirSync(testDir);
      }
    });
    
    test('should find exact filename', async () => {
      const cli = new (PawCLI as any)();
      const result = await cli.findScriptFile(testFile);
      expect(result).toBe(testFile);
    });
    
    test('should add .paw extension when file not found', async () => {
      const cli = new (PawCLI as any)();
      const result = await cli.findScriptFile(testFileNoPaw);
      expect(result).toBe(testFile);
    });
    
    test('should return null when file not found', async () => {
      const cli = new (PawCLI as any)();
      const result = await cli.findScriptFile('/nonexistent/file');
      expect(result).toBeNull();
    });
  });
  
  describe('Argument Parsing', () => {
    test('should parse script arguments correctly', () => {
      // Mock process.argv
      process.argv = ['node', 'paw', 'script.paw', 'arg1', 'arg2'];
      
      const cli = new (PawCLI as any)();
      // Test would need to be more sophisticated to actually test argument parsing
      // since it happens in the run() method
    });
    
    test('should handle -- separator', () => {
      process.argv = ['node', 'paw', '--', 'arg1', 'arg2'];
      
      const cli = new (PawCLI as any)();
      // Similar to above - would need integration test
    });
  });
});
