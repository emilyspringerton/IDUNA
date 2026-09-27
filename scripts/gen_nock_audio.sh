#!/usr/bin/env bash
# gen_nock_audio.sh -- regenerate NOCK's audio engine math from PARENA.
#
# frontend/nock/src/generated/AudioDsp.ts is TypeScript emitted by the real `parena` compiler
# from PARENA/stdlib/audio/{dsp,mixer4,deck,sampler,jive_rules}.prn -- the same files that
# compile to C for SHANKPIT and jivetalking. NOCK's Sounds tab (filter chains) and Booth tab
# (4 decks + mixer + pads, running in an AudioWorklet) call only these functions for the math.
# Committed; never hand-edit -- edit the .prn and rerun this.
#
# Usage: PARENA_DIR=../PARENA scripts/gen_nock_audio.sh   (default ../PARENA)
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
parena_dir="${PARENA_DIR:-$root/../PARENA}"
[ -x "$parena_dir/parena" ] || { echo "gen_nock_audio: build $parena_dir/parena first (make parena)" >&2; exit 1; }
a="$parena_dir/stdlib/audio"
out="$root/frontend/nock/src/generated/AudioDsp.ts"
mkdir -p "$(dirname "$out")"
"$parena_dir/parena" build "$a/dsp.prn" "$a/mixer4.prn" "$a/deck.prn" "$a/sampler.prn" "$a/jive_rules.prn" -o "$out" >/dev/null
sed -i "1a// Source: PARENA stdlib/audio/{dsp,mixer4,deck,sampler,jive_rules}.prn @ $(git -C "$parena_dir" rev-parse --short HEAD 2>/dev/null || echo unknown) (scripts/gen_nock_audio.sh)." "$out"
echo "gen_nock_audio: wrote $out"
