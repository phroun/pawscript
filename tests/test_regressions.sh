#!/usr/bin/env bash
# Run all regression tests from the tests directory
# Only prints output for failing tests

cd "$(dirname "$0")"

passed=0
failed=0

for expected in *.expected; do
    base="${expected%.expected}"
    paw_file="${base}.paw"

    if [ -f "$paw_file" ]; then
        if ../paw "$paw_file" 2>&1 | diff -q - "$expected" > /dev/null 2>&1; then
            ((passed++))
        else
            echo "FAIL: $paw_file"
            ((failed++))
        fi
    fi
done

echo ""
echo "Passed: $passed, Failed: $failed"

if [ $failed -gt 0 ]; then
    exit 1
fi
