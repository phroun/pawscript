#!/usr/bin/env bash
../paw brace-edge-cases.paw 2>&1 | diff - brace-edge-cases.expected
../paw quote-edge-cases.paw 2>&1 | diff - quote-edge-cases.expected
../paw demo.paw 2>&1 | diff - demo.expected
../paw edge-case.paw | diff - edge-case.expected
../paw escape.paw | diff - escape.expected
../paw test_ret.paw | diff - test_ret.expected
../paw test_get_type.paw | diff - test_get_type.expected
../paw test_get_inferred_type.paw | diff - test_get_inferred_type.expected
../paw test_lists.paw | diff - test_lists.expected
../paw test_nested_lists.paw | diff - test_nested_lists.expected
../paw test_string_ops.paw | diff - test_string_ops.expected
