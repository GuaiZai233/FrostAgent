"""Exercise pinned v15/v16 round trips and current chat-archive upgrades."""
import argparse
import json
import os
import sqlite3
from pathlib import Path
import subprocess
import tempfile


def run(binary, operation, root, *, archive=None, result=None, instance=None, rejection=None):
    env = os.environ.copy()
    env.update(FROSTAGENT_COMPAT_PROBE=operation, FROSTAGENT_COMPAT_ROOT=str(root))
    for key, value in [("ARCHIVE", archive), ("RESULT", result), ("INSTANCE", instance)]:
        if value is not None:
            env["FROSTAGENT_COMPAT_" + key] = str(value)
        else:
            env.pop("FROSTAGENT_COMPAT_" + key, None)
    completed = subprocess.run([str(binary), "-test.run=^TestReleaseProbe$", "-test.timeout=120s"],
                               env=env, text=True, encoding="utf-8", errors="replace", capture_output=True, timeout=150)
    if rejection is not None:
        if not completed.returncode or rejection not in completed.stdout + completed.stderr:
            raise RuntimeError(f"{binary.name} did not reject safely:\n{completed.stdout}\n{completed.stderr}")
        print(f"PASS {binary.name}: rejected {operation}", flush=True)
        return
    if completed.returncode:
        raise RuntimeError(f"{binary.name} {operation} failed:\n{completed.stdout}\n{completed.stderr}")
    print(f"PASS {binary.name}: {operation}", flush=True)


def database_snapshot(root):
    connection = sqlite3.connect(root / "frostagent.db")
    try:
        return tuple(connection.iterdump())
    finally:
        connection.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--v15-binary", required=True, type=Path)
    parser.add_argument("--v16-binary", required=True, type=Path)
    parser.add_argument("--current-binary", type=Path)
    args = parser.parse_args()
    v15, v16 = args.v15_binary.resolve(), args.v16_binary.resolve()
    current = args.current_binary.resolve() if args.current_binary else None
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
            if current:
                # Preserve the full historical test above, then migrate its
                # actual v16 database using the current release binary.
                run(current, "verify-restored", second, instance=identifier)
                run(current, "seed-history", second, instance=identifier)
                run(current, "verify-history", second, instance=identifier)
                snapshot = database_snapshot(second)
                for old in [v15, v16]:
                    run(old, "init", second, rejection="requires compatible schema")
                    if database_snapshot(second) != snapshot:
                        raise RuntimeError("rejected old startup changed current database")
                run(current, "verify-history", second, instance=identifier)
                current_archive = base / (origin + "-current.zip")
                run(current, "backup", second, archive=current_archive, instance=identifier)
                target = base / (origin + "-current-restored")
                run(current, "init", target)
                run(current, "restore", target, archive=current_archive, result=result)
                target_id = json.loads(result.read_text())
                run(current, "verify-restored", target, instance=target_id)
                run(current, "verify-history", target, instance=target_id)
    print("PASS: both release directions, old edits, and full ZIP round trips", flush=True)


if __name__ == "__main__":
    main()
