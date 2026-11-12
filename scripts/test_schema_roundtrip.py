"""Run the same fixture through independently compiled v15 and v16 test binaries."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


def run(binary, operation, root, *, archive=None, result=None, instance=None):
    env = os.environ.copy()
    env.update(FROSTAGENT_COMPAT_PROBE=operation, FROSTAGENT_COMPAT_ROOT=str(root))
    for key, value in [("ARCHIVE", archive), ("RESULT", result), ("INSTANCE", instance)]:
        if value is not None:
            env["FROSTAGENT_COMPAT_" + key] = str(value)
        else:
            env.pop("FROSTAGENT_COMPAT_" + key, None)
    completed = subprocess.run([str(binary), "-test.run=^TestReleaseProbe$", "-test.timeout=120s"],
                               env=env, text=True, encoding="utf-8", errors="replace", capture_output=True, timeout=150)
    if completed.returncode:
        raise RuntimeError(f"{binary.name} {operation} failed:\n{completed.stdout}\n{completed.stderr}")
    print(f"PASS {binary.name}: {operation}", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--v15-binary", required=True, type=Path)
    parser.add_argument("--v16-binary", required=True, type=Path)
    args = parser.parse_args()
    v15, v16 = args.v15_binary.resolve(), args.v16_binary.resolve()
    with tempfile.TemporaryDirectory(prefix="frostagent-version-roundtrip-") as temporary:
        base = Path(temporary)
        for origin in ["v16", "v15"]:
            root = base / origin
            run(v16 if origin == "v16" else v15, "seed" if origin == "v16" else "seed-legacy", root)
            if origin == "v15":
                run(v16, "upgrade", root)
            run(v15, "edit-old", root)
            run(v16, "verify", root)
            run(v15, "verify", root)
            archive = base / (origin + ".zip")
            run(v15, "backup", root, archive=archive)
            restored = base / (origin + "-restored")
            result = base / (origin + "-restored-id.json")
            run(v16, "init", restored)
            run(v15, "restore", restored, archive=archive, result=result)
            identifier = json.loads(result.read_text())
            run(v16, "verify-restored", restored, instance=identifier)
            run(v15, "verify-restored", restored, instance=identifier)
            # A backup produced by v16 must also decode and restore in v15.
            second_archive = base / (origin + "-v16.zip")
            run(v16, "backup", restored, archive=second_archive, instance=identifier)
            second = base / (origin + "-second")
            run(v16, "init", second)
            run(v15, "restore", second, archive=second_archive, result=result)
            identifier = json.loads(result.read_text())
            run(v16, "verify-restored", second, instance=identifier)
    print("PASS: both release directions, old edits, and full ZIP round trips", flush=True)


if __name__ == "__main__":
    main()
