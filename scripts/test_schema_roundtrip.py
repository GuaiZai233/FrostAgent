"""Verify current upgrades from pinned legacy databases and restores legacy ZIPs."""
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
    parser.add_argument("--current-binary", required=True, type=Path)
    parser.add_argument("--future-module-binary", required=True, type=Path)
    args = parser.parse_args()
    v15, v16 = args.v15_binary.resolve(), args.v16_binary.resolve()
    current = args.current_binary.resolve()
    future = args.future_module_binary.resolve()

    with tempfile.TemporaryDirectory(prefix="frostagent-version-roundtrip-") as temporary:
        base = Path(temporary)
        for origin, legacy, seed_operation in (
            ("v15", v15, "seed-legacy"),
            ("v16", v16, "seed"),
        ):
            # Each historical release creates its own database and ZIP. No old
            # binary is asked to read, edit, or restore the other release's data.
            root = base / origin
            run(legacy, seed_operation, root)
            legacy_archive = base / f"{origin}.zip"
            run(legacy, "backup", root, archive=legacy_archive)

            # The PR's current binary must migrate each historical schema itself.
            run(current, "init", root)
            run(current, "verify", root)
            snapshot = database_snapshot(root)

            # Once upgraded, both historical binaries must fail closed and leave
            # the Schema 20 database byte-for-byte logically unchanged.
            for old in (v15, v16):
                run(old, "init", root, rejection="requires compatible schema")
                if database_snapshot(root) != snapshot:
                    raise RuntimeError(f"{old.name} changed the upgraded {origin} database")
            run(current, "verify", root)

            # Import each old release's ZIP directly with the current restore path.
            restored = base / f"{origin}-restored-current"
            result = base / f"{origin}-restored-id.json"
            run(current, "init", restored)
            run(current, "restore", restored, archive=legacy_archive, result=result)
            restored_id = json.loads(result.read_text())
            run(current, "verify-restored", restored, instance=restored_id)

            # Exercise current ZIPs and opaque future-module data once; repeating
            # this for both legacy origins would duplicate the same current path.
            if origin == "v16":
                run(current, "seed-history", restored, instance=restored_id)
                run(current, "verify-history", restored, instance=restored_id)

                current_archive = base / "current.zip"
                run(current, "backup", restored, archive=current_archive, instance=restored_id)
                target = base / "current-restored"
                run(current, "init", target)
                run(current, "restore", target, archive=current_archive, result=result)
                target_id = json.loads(result.read_text())
                run(current, "verify-restored", target, instance=target_id)
                run(current, "verify-history", target, instance=target_id)

                run(future, "seed-module", target, instance=target_id)
                run(current, "edit-known", target, instance=target_id)
                run(future, "verify-module", target, instance=target_id)

                for generation in range(2):
                    opaque_archive = base / f"opaque-{generation}.zip"
                    run(current, "backup", target, archive=opaque_archive, instance=target_id)
                    target = base / f"opaque-{generation}-restored"
                    run(current, "init", target)
                    run(current, "restore", target, archive=opaque_archive, result=result)
                    target_id = json.loads(result.read_text())
                    run(current, "edit-known", target, instance=target_id)
                    run(current, "verify-history", target, instance=target_id)
                    run(future, "verify-module-restored", target, instance=target_id)

                run(current, "delete", target, instance=target_id)
                run(future, "verify-deleted", target, instance=target_id)

    print(
        "PASS: current upgrades v15/v16 databases, restores both legacy ZIPs, "
        "and preserves future module payloads",
        flush=True,
    )

if __name__ == "__main__":
    main()
