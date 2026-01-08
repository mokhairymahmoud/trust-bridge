#!/usr/bin/env python3
"""
Generate deterministic test data for E2E testing.

This script creates a plaintext "weights" file with a repeating pattern
that produces consistent, reproducible SHA256 hashes for verification.

Usage:
    python generate.py [--size SIZE_MB] [--output OUTPUT_PATH]

Example:
    python generate.py --size 16 --output plain.weights
"""

import argparse
import hashlib
import os
import sys


def generate_test_data(size_bytes: int) -> bytes:
    """Generate deterministic test data with a repeating pattern."""
    pattern = b"TRUSTBRIDGE_E2E_"
    repetitions = size_bytes // len(pattern)
    remainder = size_bytes % len(pattern)
    return pattern * repetitions + pattern[:remainder]


def main():
    parser = argparse.ArgumentParser(
        description="Generate deterministic test data for E2E testing"
    )
    parser.add_argument(
        "--size",
        type=int,
        default=16,
        help="Size in MiB (default: 16)",
    )
    parser.add_argument(
        "--output",
        type=str,
        default="plain.weights",
        help="Output file path (default: plain.weights)",
    )
    args = parser.parse_args()

    size_bytes = args.size * 1024 * 1024
    output_path = args.output

    # Create parent directory if needed
    parent_dir = os.path.dirname(output_path)
    if parent_dir and not os.path.exists(parent_dir):
        os.makedirs(parent_dir, exist_ok=True)

    print(f"Generating {args.size} MiB test data...")
    data = generate_test_data(size_bytes)

    # Calculate SHA256 before writing
    sha256_hash = hashlib.sha256(data).hexdigest()

    # Write to file
    with open(output_path, "wb") as f:
        f.write(data)

    print(f"Output: {output_path}")
    print(f"Size: {len(data)} bytes ({args.size} MiB)")
    print(f"SHA256: {sha256_hash}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
