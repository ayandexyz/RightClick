import importlib.machinery
import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).parents[3]


def load_installer():
    path = ROOT / "frontends/nautilus/install.sh"
    loader = importlib.machinery.SourceFileLoader("install", str(path))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class InstallerTests(unittest.TestCase):
    def test_toolkit_check_is_false_when_gi_is_missing(self):
        installer = load_installer()
        with mock.patch.dict("sys.modules", {"gi": None}):
            self.assertFalse(installer.toolkit_available())

    def test_studio_paths_are_under_user_directories(self):
        installer = load_installer()
        with tempfile.TemporaryDirectory() as home:
            with mock.patch.object(installer.Path, "home", return_value=Path(home)):
                self.assertEqual(installer.Path.home() / ".local/bin/fc-pdf-studio", Path(home) / ".local/bin/fc-pdf-studio")
                self.assertEqual(installer.Path.home() / ".local/share/file-converter/studio", Path(home) / ".local/share/file-converter/studio")


    def test_every_studio_module_imports_from_the_installed_layout(self):
        """What install_studio copies is enough to run: no module reaches back into the repo."""
        installer = load_installer()
        if not installer.toolkit_available():
            self.skipTest("needs GTK 4.12+ and libadwaita 1.5+")
        with tempfile.TemporaryDirectory() as home:
            with mock.patch.object(installer.Path, "home", return_value=Path(home)):
                with mock.patch.object(installer.subprocess, "run"):
                    installer.install_studio()
            studio = Path(home) / ".local/share/file-converter/studio"
            modules = sorted(path.stem for path in (studio / "pdf_studio").glob("*.py") if path.stem not in ("__main__",))
            self.assertIn("tools_view", modules)
            self.assertIn("password_dialog", modules)
            program = "import importlib, sys\nfor name in sys.argv[3:]:\n    importlib.import_module('pdf_studio.' + name)\n"
            # -I drops the working directory and PYTHON* variables, so only the
            # installed directory can satisfy an import.
            result = subprocess.run([sys.executable, "-I", "-c", "import sys; sys.path.insert(0, sys.argv[1]); exec(sys.argv[2])", str(studio), program, *modules],
                                    capture_output=True, text=True, cwd=home)
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
