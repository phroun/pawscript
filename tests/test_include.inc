# This file is included by test_include.paw

# Define a macro in the included file
macro included_greeting (
    echo "Hello from included file!"
)

# Define a variable
included_var: "I was set in the included file"

echo "test_include.inc loaded"
