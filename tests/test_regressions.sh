#!/usr/bin/env bash
echo "bad-command-in-brace:"
../paw bad-command-in-brace.paw 2>&1 | diff - bad-command-in-brace.expected
echo "brace-edge-cases:"
../paw brace-edge-cases.paw 2>&1 | diff - brace-edge-cases.expected
echo "demo:"
../paw demo.paw 2>&1 | diff - demo.expected
echo "edge-case:"
../paw edge-case.paw 2>&1 | diff - edge-case.expected
echo "escape:"
../paw escape.paw 2>&1 | diff - escape.expected
echo "exampls_keys:"
../paw example_keys.paw 2>&1 | diff - example_keys.expected
echo "leading-operators:"
../paw leading-operators.paw 2>&1 | diff - leading-operators.expected
echo "macro-parse-error:"
../paw macro-parse-error.paw 2>&1 | diff - macro-parse-error.expected
echo "mixed-quote-error:"
../paw mixed-quote-error.paw 2>&1 | diff - mixed-quote-error.expected
echo "nested-brace-error:"
../paw nested-brace-error.paw 2>&1 | diff - nested-brace-error.expected
echo "nested_macros:"
../paw nested_macros.paw 2>&1 | diff - nested_macros.expected
echo "operator-start-of-file:"
../paw operator-start-of-file.paw 2>&1 | diff - operator-start-of-file.expected
echo "test_chain_operators:"
../paw test_chain_operators.paw 2>&1 | diff - test_chain_operators.expected
echo "test_concat_unified:"
../paw test_concat_unified.paw 2>&1 | diff - test_concat_unified.expected
echo "test_get_inferred_type:"
../paw test_get_inferred_type.paw 2>&1 | diff - test_get_inferred_type.expected
echo "test_get_type:"
../paw test_get_type.paw 2>&1 | diff - test_get_type.expected
echo "test_if_then_else:"
../paw test_if_then_else.paw 2>&1 | diff - test_if_then_else.expected
echo "test_keys_getval:"
../paw test_keys_getval.paw 2>&1 | diff - test_keys_getval.expected
echo "test_lists:"
../paw test_lists.paw 2>&1 | diff - test_lists.expected
echo "test_log:"
../paw test_log.paw 2>&1 | diff - test_log.expected
echo "test_macro_ownership:"
../paw test_macro_ownership.paw 2>&1 | diff - test_macro_ownership.expected
echo "test_named_args:"
../paw test_named_args.paw 2>&1 | diff - test_named_args.expected
echo "test_named_args_comprehensive:"
../paw test_named_args_comprehensive.paw 2>&1 | diff - test_named_args_comprehensive.expected
echo "test_named_args_macro:"
../paw test_named_args_macro.paw 2>&1 | diff - test_named_args_macro.expected
echo "test_nested_lists:"
../paw test_nested_lists.paw 2>&1 | diff - test_nested_lists.expected
echo "test_ret:"
../paw test_ret.paw 2>&1 | diff - test_ret.expected
echo "test_ret_in_braces:"
../paw test_ret_in_braces.paw|diff - test_ret_in_braces.expected
echo "test_refcounting:"
../paw test_refcounting.paw 2>&1 | diff - test_refcounting.expected
echo "test_simple_scoping:"
../paw test_simple_scoping.paw 2>&1 | diff - test_simple_scoping.expected
echo "test_string_block_storage:"
../paw test_string_block_storage.paw 2>&1 | diff - test_string_block_storage.expected
echo "test_string_ops:"
../paw test_string_ops.paw 2>&1 | diff - test_string_ops.expected
echo "test_string_ops_refcount:"
../paw test_string_ops_refcount.paw 2>&1 | diff - test_string_ops_refcount.expected
echo "test_symbols:"
../paw test_symbols.paw 2>&1 | diff - test_symbols.expected
echo "test_tilde_interpolation:"
../paw test_tilde_interpolation.paw 2>&1 | diff - test_tilde_interpolation.expected
echo "test_type_edge_cases"
../paw test_type_edge_cases.paw 2>&1 | diff - test_type_edge_cases.expected
echo "test_type_system"
../paw test_type_system.paw 2>&1 | diff - test_type_system.expected
echo "test_unpacking:"
../paw test_unpacking.paw 2>&1 | diff - test_unpacking.expected
echo "test_variables_while:"
../paw test_variables_while.paw 2>&1 | diff - test_variables_while.expected
echo "quote-edge-cases:"
../paw quote-edge-cases.paw 2>&1 | diff - quote-edge-cases.expected
echo "scope:"
../paw scope.paw 2>&1 | diff - scope.expected
echo "trailing-operators:"
../paw trailing-operators.paw 2>&1 | diff - trailing-operators.expected
echo "test_anonymous_macros:"
../paw test_anonymous_macros.paw 2>&1 | diff - test_anonymous_macros.expected
echo "test_command_ref:"
../paw test_command_ref.paw 2>&1 | diff - test_command_ref.expected
echo "test_stored_objects_call:"
../paw test_stored_objects_call.paw 2>&1 | diff - test_stored_objects_call.expected
echo "test_channels:"
../paw test_channels.paw 2>&1 | diff - test_channels.expected
echo "test_fibers:"
../paw test_fibers.paw 2>&1 | diff - test_fibers.expected
echo "test_msleep:"
../paw test_msleep.paw 2>&1 | diff - test_msleep.expected
echo "test_multi_unit_args:"
../paw test_multi_unit_args.paw 2>&1 | diff - test_multi_unit_args.expected
echo "test_include:"
../paw test_include.paw 2>&1 | diff - test_include.expected
echo "test_io_channels:"
../paw test_io_channels.paw 2>&1 | diff - test_io_channels.expected
echo "test_sort:"
../paw test_sort.paw 2>&1 | diff - test_sort.expected
echo "test_terminal_color:"
../paw test_terminal_color.paw 2>&1 | diff - test_terminal_color.expected
echo "test_terminal_cursor:"
../paw test_terminal_cursor.paw 2>&1 | diff - test_terminal_cursor.expected
echo "test_terminal_clear:"
../paw test_terminal_clear.paw 2>&1 | diff - test_terminal_clear.expected
echo "test_math:"
../paw test_math.paw 2>&1 | diff - test_math.expected
