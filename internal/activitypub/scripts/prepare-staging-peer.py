#!/usr/bin/env python3
"""Prepare an allowlisted disposable upload; no deploy, credentials or keys."""
from pathlib import Path
import shutil, sys
if len(sys.argv) not in (2,3): raise SystemExit("Usage: prepare-staging-peer.py NEW_UPLOAD_DIR [GO_PROBE_LINUX_AMD64]")
root=Path(__file__).resolve().parents[1]
out=Path(sys.argv[1]).resolve()
if out.exists(): raise SystemExit("Destination must not exist")
out.mkdir(parents=True)
for name in ["Dockerfile", ".dockerignore", "package.json", "package-lock.json"]: shutil.copy2(root/name,out/name)
for name in ["src", "migrations"]: shutil.copytree(root/name,out/name)
shutil.copy2(root/"deploy/staging/entrypoint.mjs",out/"staging-peer.mjs")
shutil.copy2(root/"test/rfc9421.fixture.ts",out/"rfc9421.fixture.ts")
shutil.copy2(root/"deploy/staging/go-runner.fixture.mjs",out/"go-runner.fixture.mjs")
with (out/"Dockerfile").open("a") as f: f.write('\nFROM runtime AS staging-peer\nCOPY staging-peer.mjs rfc9421.fixture.ts go-runner.fixture.mjs ./\nCMD ["node","staging-peer.mjs"]\n')
with (out/".dockerignore").open("a") as f: f.write("\n!staging-peer.mjs\n!rfc9421.fixture.ts\n!go-runner.fixture.mjs\n")
if len(sys.argv)==3:
    probe=Path(sys.argv[2]).resolve()
    header=probe.read_bytes()[:20]
    if header[:6]!=b"\x7fELF\x02\x01" or int.from_bytes(header[18:20],"little")!=62: raise SystemExit("Go probe must be Linux/amd64 ELF")
    shutil.copy2(probe,out/"staging-go-probe")
    (out/"staging-go-probe").chmod(0o755)
    with (out/"Dockerfile").open("a") as f: f.write("\nCOPY staging-go-probe ./staging-go-probe\n")
    with (out/".dockerignore").open("a") as f: f.write("!staging-go-probe\n")
print("Prepared explicit disposable peer upload:", out)
