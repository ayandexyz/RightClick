#!/usr/bin/env python3
import glob
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"
FRONTEND = Path(__file__).parent

PACKAGES = {
    "arch": ["pacman", "-S", "--needed", "python-nautilus"],
    "debian": ["apt-get", "install", "-y", "python3-nautilus"],
}


def distro_family():
    info = {}
    try:
        for line in Path("/etc/os-release").read_text().splitlines():
            key, _, value = line.partition("=")
            info[key] = value.strip('"')
    except OSError:
        return None
    ids = [info.get("ID", "")] + info.get("ID_LIKE", "").split()
    for name in ids:
        if name in ("arch", "omarchy"):
            return "arch"
        if name in ("debian", "ubuntu"):
            return "debian"
    return None


def has_nautilus_python():
    return bool(glob.glob("/usr/lib*/**/nautilus/extensions-*/libnautilus-python.so", recursive=True))


def ensure_nautilus_python():
    if has_nautilus_python():
        return
    family = distro_family()
    if family is None:
        print("warning: nautilus-python not found; install it with your package manager")
        return
    command = ["sudo", *PACKAGES[family]]
    print("nautilus-python is missing; running: " + " ".join(command))
    if subprocess.run(command, check=False).returncode != 0:
        print("warning: could not install nautilus-python; run the command above manually")


def main():
    ensure_nautilus_python()
    build_dir = Path(tempfile.mkdtemp(prefix="file-converter-build-"))
    try:
        binary = build_dir / "fcd"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/fcd"], cwd=BACKEND, check=True)
        extension_dir = Path.home() / ".local/share/nautilus-python/extensions"
        extension_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(FRONTEND / "file_converter.py", extension_dir / "file_converter.py")
        package = extension_dir / "fc_client"
        shutil.rmtree(package, ignore_errors=True)
        shutil.copytree(FRONTEND / "fc_client", package)
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
        report_missing_engines()
    finally:
        shutil.rmtree(build_dir, ignore_errors=True)


# Each PDF engine is optional: a missing binary disables only the operations
# that use it. Nothing is installed automatically, because these are large
# packages a user may not want.
ENGINES = [
    ("qpdf", ["qpdf"], "Repair, Linearize, Flatten",
     "pacman -S qpdf / apt-get install qpdf"),
    ("gs", ["ghostscript"], "Compress, Convert to grayscale",
     "pacman -S ghostscript / apt-get install ghostscript"),
    ("pdftotext", ["pdftotext"], "Extract text",
     "pacman -S poppler / apt-get install poppler-utils"),
    ("pdfimages", ["pdfimages"], "Extract images",
     "pacman -S poppler / apt-get install poppler-utils"),
    # pdftocairo and tesseract are checked so they are in place when OCR lands;
    # the operation itself is disabled until then, so do not advertise it.
    ("pdftocairo", ["pdftocairo"], "OCR (not yet enabled)",
     "pacman -S poppler / apt-get install poppler-utils"),
    ("tesseract", ["tesseract"], "OCR (not yet enabled)",
     "pacman -S tesseract tesseract-data-eng / apt-get install tesseract-ocr"),
]


def report_missing_engines():
    """Print one line per missing PDF engine, or a short all-present note."""
    missing = [
        (name, operations, install)
        for name, binaries, operations, install in ENGINES
        if not any(shutil.which(binary) for binary in binaries)
    ]
    if not missing:
        print("All PDF engines are installed.")
        return
    print()
    print("Optional PDF engines are missing; the menu hides the operations that need them:")
    for name, operations, install in missing:
        print(f"  {name}: {operations}")
        print(f"    install with: {install}")


if __name__ == "__main__":
    main()
