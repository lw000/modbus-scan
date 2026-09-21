#!/usr/bin/env python3
"""Backup the project tree while keeping its directory layout.

Every ``*.go`` file is stored with an extra ``.txt`` suffix (``main.go``
becomes ``main.go.txt``). Use ``--rename-back`` if the backup tree should
contain plain ``*.go`` names again.
"""

from __future__ import annotations

import argparse
import os
import shutil
import sys
import zipfile
from pathlib import Path

# Default source root: the directory that contains this script.
DEFAULT_SOURCE = Path(__file__).resolve().parent
# Default backup root requested by the user.
DEFAULT_DEST = Path(r"D:\work\go_work\src\backup\modbus-scan")

# Temporary suffix used for *.go files during the copy step.
GO_TXT_SUFFIX = ".go.txt"

# Directories that never need to be backed up (vcs, caches, runtime data).
EXCLUDED_DIRS = {
    ".agents",
    ".git",
    ".gocache",
    ".codebuddy",
    ".idea",
    ".vscode",
    "__pycache__",
    "node_modules",
    "bin",
    "obj",
    "data",
    "logs",
}


def collect_files(source: Path, excluded: set[str]) -> list[Path]:
    """Return every regular file under ``source``, skipping excluded dirs."""
    files: list[Path] = []
    for root, dirs, names in os.walk(source):
        dirs[:] = sorted(d for d in dirs if d not in excluded)
        for name in sorted(names):
            path = Path(root) / name
            if path.is_file():
                files.append(path)
    return files


def backup_name(path: Path, txt_suffix: bool = True) -> str:
    """Return the destination file name for ``path``."""
    if txt_suffix and path.suffix == ".go":
        return path.name + ".txt"
    return path.name


def is_within(child: Path, parent: Path) -> bool:
    """Return True when ``child`` is located inside ``parent``."""
    try:
        child.relative_to(parent)
    except ValueError:
        return False
    return True


def backup(
    source: Path, dest: Path, dry_run: bool, excluded: set[str], direct_go: bool = False
) -> tuple[int, int, list[str]]:
    """Copy the tree from ``source`` to ``dest`` and report the result."""
    files = collect_files(source, excluded)
    txt_suffix = not direct_go
    copied = 0
    skipped = 0
    errors: list[str] = []

    for src_file in files:
        rel = src_file.relative_to(source)
        dst_file = dest / rel.parent / backup_name(src_file, txt_suffix)
        if dry_run:
            copied += 1
            continue
        try:
            dst_file.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src_file, dst_file)
            copied += 1
        except OSError as exc:
            skipped += 1
            errors.append(f"{rel}: {exc}")

    return copied, skipped, errors


def clean_dest(dest: Path) -> tuple[int, list[str]]:
    """Remove every file and sub directory inside the backup root."""
    removed = 0
    errors: list[str] = []
    if dest == Path(dest.anchor):
        return removed, ["refuse to clean a drive root"]
    if not dest.is_dir():
        return removed, errors

    for entry in sorted(dest.iterdir()):
        try:
            if entry.is_dir() and not entry.is_symlink():
                shutil.rmtree(entry)
            else:
                entry.unlink()
            removed += 1
        except OSError as exc:
            errors.append(f"{entry.name}: {exc}")

    return removed, errors


def zip_backup(
    source: Path, zip_path: Path, excluded: set[str], txt_suffix: bool
) -> tuple[int, list[str]]:
    """Write the whole tree into a single zip archive.

    Files inside a zip are not touched by the transparent encryption driver,
    so ``*.go`` members stay plain text until they are extracted.
    """
    packed = 0
    errors: list[str] = []
    tmp_path = zip_path.with_suffix(zip_path.suffix + ".tmp")

    try:
        with zipfile.ZipFile(tmp_path, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as arc:
            for src_file in collect_files(source, excluded):
                rel = src_file.relative_to(source)
                arc_name = rel.parent / backup_name(src_file, txt_suffix)
                try:
                    arc.write(src_file, arc_name.as_posix())
                    packed += 1
                except OSError as exc:
                    errors.append(f"{rel}: {exc}")
        os.replace(tmp_path, zip_path)
    except OSError as exc:
        errors.append(f"{zip_path.name}: {exc}")

    return packed, errors


def rename_go_txt(dest: Path) -> tuple[int, list[str]]:
    """Rename ``*.go.txt`` files in the backup tree back to ``*.go``."""
    renamed = 0
    errors: list[str] = []
    if not dest.is_dir():
        return renamed, errors

    for root, _dirs, names in os.walk(dest):
        for name in sorted(names):
            if not name.endswith(GO_TXT_SUFFIX):
                continue
            src_file = Path(root) / name
            dst_file = Path(root) / name[: -len(".txt")]
            try:
                # NOTE: overwrite a stale *.go left over by an earlier run.
                os.replace(src_file, dst_file)
                renamed += 1
            except OSError as exc:
                errors.append(f"{src_file.name}: {exc}")

    return renamed, errors


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Backup the project directory, *.go files get a .txt suffix."
    )
    parser.add_argument(
        "--src",
        type=Path,
        default=DEFAULT_SOURCE,
        help="source directory, defaults to the script directory",
    )
    parser.add_argument(
        "--dest",
        type=Path,
        default=DEFAULT_DEST,
        help="backup root, defaults to D:\\work\\go_work\\src\\backup",
    )
    parser.add_argument(
        "--exclude",
        action="append",
        default=[],
        metavar="DIR",
        help="extra directory name to skip, repeatable",
    )
    parser.add_argument(
        "--exclude-all-off",
        action="store_true",
        help="disable the built-in exclusion list",
    )
    parser.add_argument(
        "--direct-go",
        action="store_true",
        help="write *.go with its real name in one step, no .txt stage and no rename",
    )
    parser.add_argument(
        "--rename-back",
        action="store_true",
        help="opt in: rename *.go.txt in the backup back to *.go (disabled by default)",
    )
    parser.add_argument(
        "--zip",
        nargs="?",
        const="auto",
        default=None,
        metavar="FILE",
        help="pack the tree into one zip (default: <dest>.zip) instead of a folder",
    )
    parser.add_argument(
        "--zip-txt",
        action="store_true",
        help="keep the .txt suffix on *.go members inside the zip",
    )
    # NOTE: 生成 *.go 会被本机透明加密软件接管，默认只产出 *.go.txt。
    parser.add_argument(
        "--allow-go",
        action="store_true",
        help="required by --direct-go/--rename-back, acknowledges the encryption risk",
    )
    parser.add_argument(
        "--clean",
        action="store_true",
        help="delete every file in the backup root before copying",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="only print what would be copied",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    source = args.src.resolve()
    dest = args.dest.resolve()

    if not source.is_dir():
        print(f"源目录不存在: {source}")
        return 1
    if is_within(dest, source):
        print(f"备份目录不能位于源目录内部: {dest}")
        return 1
    if source == dest:
        print("源目录与备份目录不能相同")
        return 1

    excluded = set() if args.exclude_all_off else set(EXCLUDED_DIRS)
    excluded.update(args.exclude)

    if args.zip is not None:
        zip_path = (
            dest.parent / (dest.name + ".zip") if args.zip == "auto" else Path(args.zip).resolve()
        )
        if args.dry_run:
            print(f"预演: 源目录 {source}")
            print(f"预演: 压缩包 {zip_path}")
            print(f"文件数 {len(collect_files(source, excluded))}")
            return 0
        zip_path.parent.mkdir(parents=True, exist_ok=True)
        packed, zip_errors = zip_backup(source, zip_path, excluded, args.zip_txt)
        print(f"完成: 压缩包 {zip_path}")
        print(f"打包文件 {packed}，失败 {len(zip_errors)}")
        for line in zip_errors:
            print(f"  失败 {line}")
        return 0 if not zip_errors else 2

    if not args.dry_run:
        dest.mkdir(parents=True, exist_ok=True)
        if args.clean:
            removed, clean_errors = clean_dest(dest)
            print(f"清理备份目录 {removed} 项，失败 {len(clean_errors)}")
            for line in clean_errors:
                print(f"  失败 {line}")

    produce_go = args.direct_go or args.rename_back
    if produce_go and not args.allow_go:
        print("提示: 该备份目录下的 *.go 会被透明加密软件接管。")
        print("      确认要生成 *.go 时请追加 --allow-go；默认备份为 *.go.txt。")
        return 1

    copied, skipped, errors = backup(source, dest, args.dry_run, excluded, args.direct_go)
    mode = "预演" if args.dry_run else "完成"
    print(f"{mode}: 源目录 {source}")
    print(f"{mode}: 备份目录 {dest}")
    print(f"复制文件 {copied}，失败 {skipped}")
    for line in errors:
        print(f"  失败 {line}")

    if args.dry_run or not args.rename_back:
        return 0 if skipped == 0 else 2

    renamed, rename_errors = rename_go_txt(dest)
    print(f"重命名 .go.txt -> .go {renamed}，失败 {len(rename_errors)}")
    for line in rename_errors:
        print(f"  失败 {line}")

    return 0 if skipped == 0 and not rename_errors else 2


if __name__ == "__main__":
    sys.exit(main())
