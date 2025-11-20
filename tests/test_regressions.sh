#!/usr/bin/env bash
echo "brace-edge-cases:"
../paw brace-edge-cases.paw 2>&1 | diff - brace-edge-cases.expected
echo "demo:"
../paw demo.paw 2>&1 | diff - demo.expected
echo "edge-case:"
../paw edge-case.paw | diff - edge-case.expected
echo "escape:"
../paw escape.paw | diff - escape.expected
echo "leading-operators:"
../paw leading-operators.paw 2>&1 | diff - leading-operators.expected
echo "test_concat_unified:"
../paw test_concat_unified.paw 2>&1 | diff - test_concat_unified.expected
echo "test_get_inferred_type:"
../paw test_get_inferred_type.paw | diff - test_get_inferred_type.expected
echo "test_get_type:"
../paw test_get_type.paw | diff - test_get_type.expected
echo "test_lists:"
../paw test_lists.paw | diff - test_lists.expected
echo "test_macro_ownership:"
../paw test_macro_ownership.paw | diff - test_macro_ownership.expected
echo "test_nested_lists:"
../paw test_nested_lists.paw | diff - test_nested_lists.expected
echo "test_ret:"
../paw test_ret.paw | diff - test_ret.expected
echo "test_simple_scoping:"
../paw test_simple_scoping.paw 2>&1 | diff - test_simple_scoping.expected
echo "test_string_block_storage:"
../paw test_string_block_storage.paw | diff - test_string_block_storage.expected
echo "test_string_ops:"
../paw test_string_ops.paw | diff - test_string_ops.expected
echo "test_unpacking:"
../paw test_unpacking.paw | diff - test_unpacking.expected
echo "quote-edge-cases:"
../paw quote-edge-cases.paw 2>&1 | diff - quote-edge-cases.expected

