#!/usr/bin/env python3
"""Deterministic packager for IDUNA's browser extensions (SECTION 593).

  build_extension.py <extension-dir> <version> <out.zip>

Copies the extension's files (sorted, fixed timestamps, fixed permissions, no tests/READMEs-only noise excluded:
*_test.mjs are left out) into a zip with manifest.json's "version" set to <version> (Chrome wants dotted integers).
Same input + version => byte-identical zip. Also writes <out.zip>.sha256.
"""
import hashlib, json, os, sys, zipfile

def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    src, version, out = sys.argv[1:]
    parts = version.split(".")
    if not (1 <= len(parts) <= 4 and all(p.isdigit() and 0 <= int(p) <= 65535 for p in parts)):
        sys.exit("version must be 1-4 dot-separated integers 0..65535, got %r" % version)
    files = []
    for root, dirs, names in os.walk(src):
        dirs.sort()
        for n in sorted(names):
            if n.endswith("_test.mjs"):
                continue
            full = os.path.join(root, n)
            files.append((os.path.relpath(full, src).replace(os.sep, "/"), full))
    files.sort()
    if not any(r == "manifest.json" for r, _ in files):
        sys.exit("no manifest.json in %s" % src)
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
        for rel, full in files:
            data = open(full, "rb").read()
            if rel == "manifest.json":
                m = json.loads(data)
                m["version"] = version
                data = (json.dumps(m, indent=2) + "\n").encode()
            zi = zipfile.ZipInfo(rel, date_time=(2020, 1, 1, 0, 0, 0))
            zi.compress_type = zipfile.ZIP_DEFLATED
            zi.external_attr = 0o644 << 16
            z.writestr(zi, data)
    digest = hashlib.sha256(open(out, "rb").read()).hexdigest()
    open(out + ".sha256", "w").write("%s  %s\n" % (digest, os.path.basename(out)))
    print("%s  %d files  sha256 %s" % (out, len(files), digest))

main()
