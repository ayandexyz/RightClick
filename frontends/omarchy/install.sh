#!/usr/bin/env python3
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"
FRONTEND = Path(__file__).parent


def main():
    build_dir = Path(tempfile.mkdtemp(prefix="file-converter-build-"))
    try:
        binary = build_dir / "fcd"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/fcd"], cwd=BACKEND, check=True)
        extension_dir = Path.home() / ".local/share/nautilus-python/extensions"
        extension_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(FRONTEND / "nautilus/file_converter.py", extension_dir / "file_converter.py")
        package = extension_dir / "fc_client"
        shutil.rmtree(package, ignore_errors=True)
        shutil.copytree(FRONTEND / "nautilus/fc_client", package)
        bin_dir = Path.home() / ".local/bin"
        bin_dir.mkdir(parents=True, exist_ok=True)
        # Copy beside the target and rename: a running fcd keeps its old inode, so this works while the service runs.
        staged = bin_dir / ".fcd.new"
        shutil.copy2(binary, staged)
        staged.chmod(0o755)
        os.replace(staged, bin_dir / "fcd")
        unit_dir = Path.home() / ".config/systemd/user"
        unit_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(FRONTEND / "systemd/fcd.service", unit_dir / "fcd.service")
        subprocess.run(["systemctl", "--user", "daemon-reload"], check=False)
        subprocess.run(["systemctl", "--user", "enable", "fcd.service"], check=False)
        subprocess.run(["systemctl", "--user", "restart", "fcd.service"], check=False)
        subprocess.run(["nautilus", "-q"], check=False)
        print("File Converter Nautilus integration installed")
    finally:
        shutil.rmtree(build_dir, ignore_errors=True)


if __name__ == "__main__":
    main()
