#!/usr/bin/env bash
# build_nle_wasm.sh -- builds src/video/nle.wasm, the "MIXFORGE EDITOR" (NOCK's video editor)
# non-linear-editing DSP, from PARENA/stdlib/video/nle.prn. Founder real-time: "ensure NOCK tools
# video editor is PARENA wasm powered and shares components with MIXFORGE" -- same real pipeline
# MIXFORGE/scripts/build_dsp_wasm.sh already establishes (parena build -> LLVM IR ->
# llc -mtriple=wasm32-unknown-unknown -> wasm-ld), copy-adjusted for this repo's own layout
# (IDUNA is a sibling of PARENA, not MIXFORGE -- PARENA_ROOT defaults two levels up from here).
#
# Finds llc/wasm-ld under LLVM_TOOLCHAIN_ROOT (the no-sudo extracted toolchain PARENA's own
# docs/LLVM_BACKEND_NORTHSTAR.md describes) if present, else on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."

PARENA_ROOT="${PARENA_ROOT:-../../../PARENA}"
LLVM_TOOLCHAIN_ROOT="${LLVM_TOOLCHAIN_ROOT:-$HOME/.local/opt/llvm-toolchain}"
if [ -x "$LLVM_TOOLCHAIN_ROOT/usr/lib/llvm-18/bin/llc" ]; then
  LLC="$LLVM_TOOLCHAIN_ROOT/usr/lib/llvm-18/bin/llc"
  WASM_LD="$LLVM_TOOLCHAIN_ROOT/usr/lib/llvm-18/bin/wasm-ld"
  export LD_LIBRARY_PATH="$LLVM_TOOLCHAIN_ROOT/usr/lib/x86_64-linux-gnu:$LLVM_TOOLCHAIN_ROOT/usr/lib/llvm-18/lib:${LD_LIBRARY_PATH:-}"
else
  LLC="$(command -v llc || true)"
  WASM_LD="$(command -v wasm-ld || true)"
fi

if [ ! -x "$PARENA_ROOT/parena" ]; then
  echo "build_nle_wasm.sh: $PARENA_ROOT/parena not found -- build PARENA first (cd $PARENA_ROOT && make build)" >&2
  exit 1
fi
if [ -z "$LLC" ] || [ -z "$WASM_LD" ]; then
  echo "build_nle_wasm.sh: llc/wasm-ld not found (LLVM_TOOLCHAIN_ROOT or PATH)" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
"$PARENA_ROOT/parena" build "$PARENA_ROOT/stdlib/video/nle.prn" -o "$tmp/nle.ll"
"$LLC" -O2 -mtriple=wasm32-unknown-unknown -filetype=obj "$tmp/nle.ll" -o "$tmp/nle.o"
"$WASM_LD" --no-entry --export-all --allow-undefined -o src/video/nle.wasm "$tmp/nle.o"

node scripts/nle_test.mjs
echo "build_nle_wasm.sh: src/video/nle.wasm built and verified"
