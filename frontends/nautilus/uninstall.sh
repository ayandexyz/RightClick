#!/usr/bin/env python3
import shutil
import subprocess
from pathlib import Path


def main():
    extension_dir = Path.home() / ".local/share/nautilus-python/extensions"
    for path in (extension_dir / "file_converter.py", extension_dir / "fc_client"):
        if path.is_dir():
            shutil.rmtree(path)
        else:
            path.unlink(missing_ok=True)
    (Path.home() / ".local/bin/fcd").unlink(missing_ok=True)
    shutil.rmtree(Path.home() / ".local/share/file-converter/studio", ignore_errors=True)
    (Path.home() / ".local/bin/fc-pdf-studio").unlink(missing_ok=True)
    desktop = Path.home() / ".local/share/applications/io.fileconverter.PdfStudio.desktop"
    desktop.unlink(missing_ok=True)
    database = shutil.which("update-desktop-database")
    if database:
        subprocess.run([database, str(desktop.parent)], check=False)
    unit = Path.home() / ".config/systemd/user/fcd.service"
    subprocess.run(["systemctl", "--user", "disable", "--now", "fcd.service"], check=False)
    unit.unlink(missing_ok=True)
    subprocess.run(["systemctl", "--user", "daemon-reload"], check=False)
    subprocess.run(["nautilus", "-q"], check=False)
    print("File Converter Nautilus integration removed")


if __name__ == "__main__":
    main()
