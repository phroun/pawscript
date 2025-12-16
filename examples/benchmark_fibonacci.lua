-- Fibonacci Benchmark (Recursive)
-- Tests recursion performance
--
-- Note: Recursive fibonacci is O(2^n), so values above 25 take a very long time.
-- This benchmark demonstrates recursion, not optimal fibonacci computation.

print("=== Fibonacci Benchmark (Recursive) ===")

-- Define the recursive fibonacci function
local function fib(n)
    if n <= 1 then
        return n
    end
    return fib(n - 1) + fib(n - 2)
end

-- Helper to get time in microseconds
local function microtime()
    -- os.clock() returns CPU time in seconds
    return os.clock() * 1000000
end

-- Benchmark fib(15)
print("Computing fib(15) recursively...")
local start = microtime()
local result = fib(15)
local elapsed = math.floor((microtime() - start) / 1000)
print(string.format("fib(15) = %d in %d ms", result, elapsed))

-- Benchmark fib(20)
print("")
print("Computing fib(20) recursively...")
start = microtime()
result = fib(20)
elapsed = math.floor((microtime() - start) / 1000)
print(string.format("fib(20) = %d in %d ms", result, elapsed))

-- Benchmark fib(25)
print("")
print("Computing fib(25) recursively (this may take a while)...")
start = microtime()
result = fib(25)
elapsed = math.floor((microtime() - start) / 1000)
print(string.format("fib(25) = %d in %d ms", result, elapsed))

print("")
print("=== Benchmark Complete ===")

-- Reference values:
-- fib(15) = 610
-- fib(20) = 6765
-- fib(25) = 75025
