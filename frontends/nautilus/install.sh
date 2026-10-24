#!/usr/bin/env python3
import fnmatch
import glob
import hashlib
import json
import os
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"
FRONTEND = Path(__file__).parent

PACKAGES = {
    "arch": ["pacman", "-S", "--needed", "python-nautilus"],
    "debian": ["apt-get", "install", "-y", "python3-nautilus"],
}

GITHUB_RELEASE_API = "https://api.github.com/repos/ayandexyz/RightClick/releases/latest"
# Matches the asset names published alongside each release; add an entry
# here once a release ships binaries for another architecture.
ARCH_SUFFIXES = {"x86_64": "amd64", "aarch64": "arm64"}


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


def toolkit_available():
    try:
        import gi

        gi.require_version("Gtk", "4.0")
        gi.require_version("Adw", "1")
        from gi.repository import Adw, Gtk
        return (Gtk.get_major_version(), Gtk.get_minor_version()) >= (4, 12) and (Adw.get_major_version(), Adw.get_minor_version()) >= (1, 5)
    except (ImportError, ValueError):
        return False


def install_studio():
    if not toolkit_available():
        print("PDF Studio skipped: GTK 4.12+ and libadwaita 1.5+ with PyGObject are required")
        print("  Arch: install gtk4 libadwaita python-gobject")
        print("  Debian/Ubuntu: install gir1.2-gtk-4.0 gir1.2-adw-1 python3-gi")
        return
    studio_dir = Path.home() / ".local/share/file-converter/studio"
    studio_dir.mkdir(parents=True, exist_ok=True)
    for name in ("pdf_studio",):
        target = studio_dir / name
        shutil.rmtree(target, ignore_errors=True)
        shutil.copytree(FRONTEND.parent / "pdf-studio" / name, target)
    client = studio_dir / "fc_client"
    shutil.rmtree(client, ignore_errors=True)
    shutil.copytree(ROOT / "frontends" / "common" / "fc_client", client)
    launcher = Path.home() / ".local/bin/fc-pdf-studio"
    launcher.parent.mkdir(parents=True, exist_ok=True)
    launcher.write_text(
        f"#!{sys.executable}\n"
        "import os, sys\n"
        "sys.path.insert(0, os.path.expanduser('~/.local/share/file-converter/studio'))\n"
        "from pdf_studio.cli import main\n"
        "sys.exit(main())\n"
    )
    launcher.chmod(0o755)
    applications = Path.home() / ".local/share/applications"
    applications.mkdir(parents=True, exist_ok=True)
    shutil.copy2(FRONTEND.parent / "pdf-studio" / "io.fileconverter.PdfStudio.desktop", applications / "io.fileconverter.PdfStudio.desktop")
    if shutil.which("update-desktop-database"):
        subprocess.run(["update-desktop-database", str(applications)], check=False)
    print("PDF Studio installed")


def _github_get(url, timeout=10):
    request = urllib.request.Request(url, headers={"User-Agent": "file-converter-installer"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return response.read()


def _fetch_prebuilt_binaries(build_dir):
    """Download fcd/fcctl from the latest GitHub release for this machine.

    Returns {"fcd": path, "fcctl": path} in build_dir, or None if there is
    no matching release asset (unsupported architecture, no network, or a
    checksum mismatch) -- callers should fall back to building from source.
    """
    suffix = ARCH_SUFFIXES.get(platform.machine())
    if suffix is None:
        return None
    try:
        release = json.loads(_github_get(GITHUB_RELEASE_API))
        assets = {asset["name"]: asset["browser_download_url"] for asset in release.get("assets", [])}
        tarball_name = next(
            (name for name in assets if fnmatch.fnmatch(name, f"file-converter-*-linux-{suffix}.tar.gz")), None
        )
        if tarball_name is None or "SHA256SUMS" not in assets:
            return None
        tarball = _github_get(assets[tarball_name], timeout=60)
        checksums = _github_get(assets["SHA256SUMS"]).decode()
        wanted = next((line.split()[0] for line in checksums.splitlines() if line.endswith(tarball_name)), None)
        if wanted is None or hashlib.sha256(tarball).hexdigest() != wanted:
            print(f"warning: checksum mismatch for {tarball_name}; building from source instead")
            return None
        archive_path = build_dir / tarball_name
        archive_path.write_bytes(tarball)
        with tarfile.open(archive_path) as tar:
            wanted_members = [m for m in tar.getmembers() if m.name in ("fcd", "fcctl") and m.isfile()]
            if {m.name for m in wanted_members} != {"fcd", "fcctl"}:
                return None
            tar.extractall(build_dir, members=wanted_members, filter="data")
        print(f"Using prebuilt fcd/fcctl from {release.get('tag_name', 'the latest release')}")
        return {"fcd": build_dir / "fcd", "fcctl": build_dir / "fcctl"}
    except (urllib.error.URLError, OSError, ValueError, KeyError, tarfile.TarError):
        return None


def _build_from_source(build_dir):
    fcd = build_dir / "fcd"
    fcctl = build_dir / "fcctl"
    subprocess.run(["go", "build", "-o", str(fcd), "./cmd/fcd"], cwd=BACKEND, check=True)
    subprocess.run(["go", "build", "-o", str(fcctl), "./cmd/fcctl"], cwd=BACKEND, check=True)
    return {"fcd": fcd, "fcctl": fcctl}


def _install_binary(source, bin_dir, name):
    bin_dir.mkdir(parents=True, exist_ok=True)
    # Copy beside the target and rename: a running fcd keeps its old inode, so this works while the service runs.
    staged = bin_dir / f".{name}.new"
    shutil.copy2(source, staged)
    staged.chmod(0o755)
    os.replace(staged, bin_dir / name)


def main():
    ensure_nautilus_python()
    build_dir = Path(tempfile.mkdtemp(prefix="file-converter-build-"))
    try:
        binaries = _fetch_prebuilt_binaries(build_dir) or _build_from_source(build_dir)
        extension_dir = Path.home() / ".local/share/nautilus-python/extensions"
        extension_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(FRONTEND / "file_converter.py", extension_dir / "file_converter.py")
        package = extension_dir / "fc_client"
        shutil.rmtree(package, ignore_errors=True)
        # The client is shared with the PDF Studio and lives outside this
        # frontend, but the installed layout is unchanged: extensions/fc_client/.
        shutil.copytree(ROOT / "frontends" / "common" / "fc_client", package)
        bin_dir = Path.home() / ".local/bin"
        _install_binary(binaries["fcd"], bin_dir, "fcd")
        _install_binary(binaries["fcctl"], bin_dir, "fcctl")
        unit_dir = Path.home() / ".config/systemd/user"
        unit_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(FRONTEND / "systemd/fcd.service", unit_dir / "fcd.service")
        subprocess.run(["systemctl", "--user", "daemon-reload"], check=False)
        subprocess.run(["systemctl", "--user", "enable", "fcd.service"], check=False)
        subprocess.run(["systemctl", "--user", "restart", "fcd.service"], check=False)
        subprocess.run(["nautilus", "-q"], check=False)
        print("File Converter Nautilus integration installed")
        install_studio()
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

# onnxruntime isn't a binary on PATH, so it's checked separately: as a shared
# library at one of the paths bgremove's resolveLibraryPath also checks (or
# ONNXRUNTIME_LIB_PATH, if set).
ONNXRUNTIME_LIB_PATHS = [
    "/usr/lib/libonnxruntime.so",
    "/usr/lib64/libonnxruntime.so",
    "/usr/local/lib/libonnxruntime.so",
    "/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
]


def onnxruntime_available():
    env_path = os.environ.get("ONNXRUNTIME_LIB_PATH")
    if env_path:
        return os.path.exists(env_path)
    return any(os.path.exists(path) for path in ONNXRUNTIME_LIB_PATHS)


def report_missing_engines():
    """Print one line per missing PDF engine, or a short all-present note."""
    missing = [
        (name, operations, install)
        for name, binaries, operations, install in ENGINES
        if not any(shutil.which(binary) for binary in binaries)
    ]
    onnx_missing = not onnxruntime_available()
    if not missing and not onnx_missing:
        print("All PDF engines are installed.")
        return
    print()
    print("Optional engines are missing; the menu hides the operations that need them:")
    for name, operations, install in missing:
        print(f"  {name}: {operations}")
        print(f"    install with: {install}")
    if onnx_missing:
        print("  onnxruntime: Remove Background")
        print("    install with: pacman -S onnxruntime / see https://onnxruntime.ai for other distros")


if __name__ == "__main__":
    main()
