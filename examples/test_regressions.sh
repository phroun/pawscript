#!/usr/bin/env bash
../paw brace-edge-cases.paw 2>&1 | diff - brace-edge-cases.expected
../paw quote-edge-cases.paw 2>&1 | diff - quote-edge-cases.expected
../paw demo.paw | diff - demo.expected
../paw edge-case.paw | diff - edge-case.expected
