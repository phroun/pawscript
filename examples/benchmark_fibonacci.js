#!/usr/bin/env node
/**
 * Fibonacci Benchmark (Recursive)
 * Equivalent to benchmark_fibonacci.paw for performance comparison.
 *
 * Note: Recursive fibonacci is O(2^n), so values above 25 take a very long time.
 * This benchmark demonstrates recursion overhead, not optimal fibonacci computation.
 */

function fib(n) {
    if (n <= 1) {
        return n;
    }
    return fib(n - 1) + fib(n - 2);
}

function benchmark(n) {
    const start = performance.now();
    const result = fib(n);
    const elapsedMs = Math.floor(performance.now() - start);
    return { result, elapsedMs };
}

console.log("=== Fibonacci Benchmark (Recursive) ===");

// Benchmark fib(15)
console.log("Computing fib(15) recursively...");
let { result, elapsedMs } = benchmark(15);
console.log(`fib(15) = ${result} in ${elapsedMs} ms`);

// Benchmark fib(20)
console.log();
console.log("Computing fib(20) recursively...");
({ result, elapsedMs } = benchmark(20));
console.log(`fib(20) = ${result} in ${elapsedMs} ms`);

// Benchmark fib(25)
console.log();
console.log("Computing fib(25) recursively (this may take a while)...");
({ result, elapsedMs } = benchmark(25));
console.log(`fib(25) = ${result} in ${elapsedMs} ms`);

console.log();
console.log("=== Benchmark Complete ===");

// Reference values:
// fib(15) = 610
// fib(20) = 6765
// fib(25) = 75025
