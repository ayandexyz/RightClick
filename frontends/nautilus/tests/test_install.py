import importlib.machinery
import importlib.util
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


if __name__ == "__main__":
    unittest.main()
