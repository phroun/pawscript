# Fibonacci Benchmark (Recursive)
# Tests recursion performance
#
# Note: Recursive fibonacci is O(2^n), so values above 25 take a very long time.
# This benchmark demonstrates recursion, not optimal fibonacci computation.

puts "=== Fibonacci Benchmark (Recursive) ==="

# Define the recursive fibonacci function
def fib(n)
  return n if n <= 1
  fib(n - 1) + fib(n - 2)
end

# Benchmark fib(15)
puts "Computing fib(15) recursively..."
start = Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond)
result = fib(15)
elapsed = (Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond) - start) / 1000
puts "fib(15) = #{result} in #{elapsed.to_i} ms"

# Benchmark fib(20)
puts ""
puts "Computing fib(20) recursively..."
start = Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond)
result = fib(20)
elapsed = (Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond) - start) / 1000
puts "fib(20) = #{result} in #{elapsed.to_i} ms"

# Benchmark fib(25)
puts ""
puts "Computing fib(25) recursively (this may take a while)..."
start = Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond)
result = fib(25)
elapsed = (Process.clock_gettime(Process::CLOCK_MONOTONIC, :microsecond) - start) / 1000
puts "fib(25) = #{result} in #{elapsed.to_i} ms"

puts ""
puts "=== Benchmark Complete ==="

# Reference values:
# fib(15) = 610
# fib(20) = 6765
# fib(25) = 75025
