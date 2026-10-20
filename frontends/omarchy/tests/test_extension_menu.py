import importlib.util
import sys
import types
import unittest
from pathlib import Path
from unittest import mock


class FakeFile:
    def __init__(self, path, mime="image/png", scheme="file", directory=False):
        self.path, self.mime = path, mime
        self.scheme, self.directory = scheme, directory

    def get_uri_scheme(self):
        return self.scheme

    def is_directory(self):
        return self.directory

    def get_location(self):
        return types.SimpleNamespace(get_path=lambda: self.path)

    def get_mime_type(self):
        return self.mime


class Menu:
    def __init__(self):
        self.items = []

    def append_item(self, item):
        self.items.append(item)


class MenuItem:
    def __init__(self, name, label):
        self.name, self.label = name, label
        self.callback = None
        self.submenu = None

    def connect(self, _signal, callback, *args):
        self.callback = (callback, args)

    def set_submenu(self, submenu):
        self.submenu = submenu


class ExtensionMenuTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        gi = types.ModuleType("gi")
        setattr(gi, "require_version", lambda *_args: None)
        repository = types.ModuleType("gi.repository")
        class FakeGObject:
            pass
        class FakeMenuProvider:
            pass
        setattr(repository, "GObject", types.SimpleNamespace(GObject=FakeGObject))
        setattr(repository, "Nautilus", types.SimpleNamespace(Menu=Menu, MenuItem=MenuItem, MenuProvider=FakeMenuProvider))
        sys.modules["gi"] = gi
        sys.modules["gi.repository"] = repository
        path = Path(__file__).parents[1] / "nautilus/file_converter.py"
        spec = importlib.util.spec_from_file_location("file_converter", path)
        assert spec is not None and spec.loader is not None
        cls.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.module)

    def test_unsupported_selection_has_no_menu(self):
        provider = self.module.FileConverterMenuProvider()
        self.assertEqual(provider.get_file_items([FakeFile("/tmp/a.txt", "text/plain")]), [])

    def test_video_selection_builds_convert_menu(self):
        targets = [
            types.SimpleNamespace(preset="video-to-mp4", label="MP4 Video", enabled=True),
            types.SimpleNamespace(preset="video-to-gif", label="GIF Animation", enabled=True),
        ]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.mkv", "video/x-matroska")])
        self.assertEqual(menu[0].label, "Convert to")
        self.assertEqual(menu[0].submenu.items[0].label, "MP4 Video")

    def test_menu_is_built_from_daemon_menu_targets(self):
        # The daemon owns the menu policy: ask it for menu targets and show exactly what it returns.
        targets = [types.SimpleNamespace(preset="video-to-webm", label="WebM Video", enabled=True)]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.mkv", "video/x-matroska")])
        client.return_value.targets.assert_called_once_with(["/tmp/a.mkv"], menu=True)
        self.assertEqual([item.label for item in menu[0].submenu.items], ["WebM Video"])

    def test_mixed_image_audio_selection_has_no_menu_when_daemon_offers_nothing(self):
        files = [FakeFile("/tmp/a.png"), FakeFile("/tmp/b.flac", "audio/flac")]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = []
            self.assertEqual(self.module.FileConverterMenuProvider().get_file_items(files), [])
        client.return_value.targets.assert_called_once_with(["/tmp/a.png", "/tmp/b.flac"], menu=True)

    def test_every_supported_category_passes_the_mime_filter(self):
        for mime in ("image/png", "image/svg+xml", "video/mp4", "audio/mpeg", "application/pdf"):
            self.assertIn(mime, self.module.SUPPORTED_MIME_TYPES)
        # No office engine yet, so office documents must not get a menu.
        self.assertNotIn(
            "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            self.module.SUPPORTED_MIME_TYPES,
        )

    def test_audio_selection_builds_convert_menu(self):
        targets = [types.SimpleNamespace(preset="audio-to-mp3", label="MP3 Audio", enabled=True)]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.flac", "audio/flac")])
        self.assertEqual(menu[0].label, "Convert to")
        self.assertEqual(menu[0].submenu.items[0].label, "MP3 Audio")

    def test_svg_selection_exposes_enabled_svg_targets(self):
        targets = [
            types.SimpleNamespace(preset="svg-to-png", label="SVG to PNG", enabled=True),
            types.SimpleNamespace(preset="svg-to-jpeg", label="SVG to JPEG", enabled=True),
            types.SimpleNamespace(preset="svg-to-webp", label="SVG to WebP", enabled=True),
            types.SimpleNamespace(preset="image-to-png", label="PNG Image", enabled=False),
        ]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.svg", "image/svg+xml")])
        self.assertEqual(menu[0].label, "Convert to")
        self.assertEqual(
            [item.label for item in menu[0].submenu.items],
            ["SVG to PNG", "SVG to JPEG", "SVG to WebP"],
        )

    def test_pdf_selection_exposes_enabled_pdf_targets(self):
        targets = [
            types.SimpleNamespace(preset="pdf-to-png", label="PDF to PNG", enabled=True),
            types.SimpleNamespace(preset="pdf-to-jpeg", label="PDF to JPEG", enabled=True),
            types.SimpleNamespace(preset="pdf-to-webp", label="PDF to WebP", enabled=True),
            types.SimpleNamespace(preset="image-to-png", label="PNG Image", enabled=False),
        ]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")])
        self.assertEqual(menu[0].label, "Convert to")
        self.assertEqual(
            [item.label for item in menu[0].submenu.items],
            ["PDF to PNG", "PDF to JPEG", "PDF to WebP"],
        )

    def test_enabled_targets_are_ordered_and_disabled_dropped(self):
        targets = [
            types.SimpleNamespace(preset="image-to-webp", label="WebP", enabled=True),
            types.SimpleNamespace(preset="image-to-png", label="PNG", enabled=False),
            types.SimpleNamespace(preset="image-to-jpeg", label="JPEG", enabled=True),
        ]
        with mock.patch.object(self.module, "FcClient") as client:
            client.return_value.targets.return_value = targets
            menu = self.module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.png")])
        self.assertEqual(menu[0].label, "Convert to")
        self.assertEqual([item.label for item in menu[0].submenu.items], ["WebP", "JPEG"])

    def test_mixed_selection_has_no_menu_without_socket(self):
        provider = self.module.FileConverterMenuProvider()
        files = [FakeFile("/tmp/a.png"), FakeFile("/tmp/a.jpg", "image/jpeg", scheme="smb")]
        with mock.patch.object(self.module, "FcClient") as client:
            self.assertEqual(provider.get_file_items(files), [])
            client.assert_not_called()

    def test_relative_path_has_no_menu(self):
        provider = self.module.FileConverterMenuProvider()
        with mock.patch.object(self.module, "FcClient") as client:
            self.assertEqual(provider.get_file_items([FakeFile("relative.png")]), [])
            client.assert_not_called()

    def test_completed_with_errors_reports_failed_count(self):
        with mock.patch.object(self.module, "send") as send:
            self.module._notify_job({
                "state": "completed_with_errors",
                "files": [
                    {"state": "done"},
                    {"state": "done"},
                    {"state": "failed", "error": {"message": "bad image"}},
                ],
            })
        self.assertEqual(send.call_args.args, ("Conversion finished with errors", "2 converted, 1 failed: bad image"))

    def test_daemon_unavailable_uses_recovery_instruction(self):
        error = self.module.DaemonUnavailable("socket missing")
        with mock.patch.object(self.module, "FcClient") as client, mock.patch.object(self.module, "send") as send:
            client.return_value.convert.side_effect = error
            self.module.FileConverterMenuProvider._convert(["/tmp/a.png"], "image-to-jpeg")
        self.assertEqual(send.call_args.args, ("File Converter is not running", "Start it with: systemctl --user start fcd"))


if __name__ == "__main__":
    unittest.main()
