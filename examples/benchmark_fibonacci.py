#!/usr/bin/env python3
"""
Fibonacci Benchmark (Recursive)
Equivalent to benchmark_fibonacci.paw for performance comparison.

Note: Recursive fibonacci is O(2^n), so values above 25 take a very long time.
This benchmark demonstrates recursion overhead, not optimal fibonacci computation.
"""

import time

def fib(n):
    if n <= 1:
        return n
    return fib(n - 1) + fib(n - 2)

def benchmark(n):
    start = time.time()
    result = fib(n)
    elapsed_ms = int((time.time() - start) * 1000)
    return result, elapsed_ms

print("=== Fibonacci Benchmark (Recursive) ===")

# Benchmark fib(15)
print("Computing fib(15) recursively...")
result, elapsed = benchmark(15)
print(f"fib(15) = {result} in {elapsed} ms")

# Benchmark fib(20)
print()
print("Computing fib(20) recursively...")
result, elapsed = benchmark(20)
print(f"fib(20) = {result} in {elapsed} ms")

# Benchmark fib(25)
print()
print("Computing fib(25) recursively (this may take a while)...")
result, elapsed = benchmark(25)
print(f"fib(25) = {result} in {elapsed} ms")

print()
print("=== Benchmark Complete ===")

# Reference values:
# fib(15) = 610
# fib(20) = 6765
# fib(25) = 75025
