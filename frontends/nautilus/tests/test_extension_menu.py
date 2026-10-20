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

    def activate(self):
        """Fire the activate signal the way Nautilus would."""
        if self.callback is None:
            raise AssertionError("no activate callback connected")
        callback, args = self.callback
        return callback(self, *args)


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
        path = Path(__file__).parents[1] / "file_converter.py"
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


def op(id, label, mode="instant", enabled=True, group="organise", reason=""):
    return types.SimpleNamespace(
        id=id, label=label, mode=mode, group=group, enabled=enabled, reason=reason, engine=""
    )


class PDFToolsMenuTests(ExtensionMenuTests):
    def provider_with(self, targets, operations):
        module = self.module
        patcher = mock.patch.object(module, "FcClient")
        self.addCleanup(patcher.stop)
        client = patcher.start().return_value
        client.targets.return_value = targets
        client.operations.return_value = operations
        return module.FileConverterMenuProvider()

    def test_single_pdf_offers_pdf_tools_after_convert_to(self):
        provider = self.provider_with(
            [types.SimpleNamespace(preset="pdf-to-png", label="PNG Image", enabled=True)],
            [op("pdf-rotate-cw", "Rotate 90° clockwise"),
             op("pdf-merge", "Merge", enabled=False, reason="needs at least 2 files")],
        )
        menu = provider.get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")])
        self.assertEqual([item.label for item in menu], ["Convert to", "PDF Tools"])
        # Merge needs two files, so the daemon disables it here and the menu drops it.
        self.assertEqual([i.label for i in menu[1].submenu.items], ["Rotate 90° clockwise"])

    def test_two_pdfs_include_merge(self):
        provider = self.provider_with(
            [], [op("pdf-merge", "Merge"), op("pdf-rotate-180", "Rotate 180°")]
        )
        menu = provider.get_file_items(
            [FakeFile("/tmp/a.pdf", "application/pdf"), FakeFile("/tmp/b.pdf", "application/pdf")]
        )
        self.assertEqual([item.label for item in menu], ["PDF Tools"])
        self.assertEqual([i.label for i in menu[0].submenu.items], ["Merge", "Rotate 180°"])

    def test_studio_operations_are_not_offered(self):
        provider = self.provider_with(
            [], [op("pdf-rotate", "Rotate", mode="studio"), op("pdf-extract", "Extract pages", mode="studio")]
        )
        self.assertEqual(provider.get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")]), [])

    def test_disabled_operation_is_hidden(self):
        provider = self.provider_with(
            [],
            [op("pdf-repair", "Repair", enabled=False, reason="qpdf is not installed"),
             op("pdf-reverse", "Reverse pages")],
        )
        menu = provider.get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")])
        self.assertEqual([i.label for i in menu[0].submenu.items], ["Reverse pages"])

    def test_images_build_a_pdf_tools_submenu(self):
        provider = self.provider_with(
            [], [op("pdf-from-images", "Images to PDF", group="import")]
        )
        menu = provider.get_file_items([FakeFile("/tmp/a.png"), FakeFile("/tmp/b.png")])
        self.assertEqual([item.label for item in menu], ["PDF Tools"])
        self.assertEqual([i.label for i in menu[0].submenu.items], ["Images to PDF"])

    def test_mixed_pdf_and_image_selection_has_no_pdf_tools(self):
        provider = self.provider_with([], [])
        self.assertEqual(
            provider.get_file_items([FakeFile("/tmp/a.pdf", "application/pdf"), FakeFile("/tmp/b.png")]),
            [],
        )

    def test_daemon_without_operations_still_shows_convert_to(self):
        module = self.module
        with mock.patch.object(module, "FcClient") as client:
            client.return_value.targets.return_value = [
                types.SimpleNamespace(preset="pdf-to-png", label="PNG Image", enabled=True)
            ]
            client.return_value.operations.side_effect = module.RequestError("unknown_method", "no such method")
            menu = module.FileConverterMenuProvider().get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")])
        self.assertEqual([item.label for item in menu], ["Convert to"])

    def test_activating_an_operation_spawns_the_helper_with_op_and_paths(self):
        module = self.module
        provider = self.provider_with([], [op("pdf-reverse", "Reverse pages")])
        menu = provider.get_file_items([FakeFile("/tmp/a.pdf", "application/pdf")])
        with mock.patch.object(module, "_spawn_helper") as spawn:
            menu[0].submenu.items[0].activate()
        spawn.assert_called_once_with("--operation", "pdf-reverse", "/tmp/a.pdf")


class OperationNotificationTests(ExtensionMenuTests):
    def notify(self, job, op_id="pdf-reverse"):
        module = self.module
        with mock.patch.object(module, "send") as send:
            module._notify_operation(job, op_id)
        return send.call_args

    def test_successful_operation_uses_its_title(self):
        send_args = self.notify(
            {"state": "completed", "files": [{"state": "done", "source": "/tmp/a.pdf", "outputs": ["/tmp/a-reversed.pdf"]}]},
            "pdf-reverse",
        )
        self.assertEqual(send_args.args[0], "PDF reversed")

    def test_invalid_pdf_gets_a_friendly_message(self):
        send_args = self.notify(
            {"state": "failed", "files": [{"state": "failed", "error": {"code": "invalid_pdf", "message": "no header"}}]},
            "pdf-repair",
        )
        self.assertIn("could not be read", send_args.args[1])

    def test_locked_pdf_gets_a_friendly_message(self):
        send_args = self.notify(
            {"state": "failed", "files": [{"state": "failed", "error": {"code": "password_required", "message": "encrypted"}}]},
            "pdf-merge",
        )
        self.assertIn("locked", send_args.args[1])

    def test_compress_reports_the_size_change(self):
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            before = Path(tmp) / "big.pdf"
            after = Path(tmp) / "small.pdf"
            before.write_bytes(b"x" * 4096)
            after.write_bytes(b"x" * 1024)
            send_args = self.notify(
                {"state": "completed", "files": [{"state": "done", "source": str(before), "outputs": [str(after)]}]},
                "pdf-compress-balanced",
            )
        body = send_args.args[1]
        self.assertIn("→", body)
        self.assertIn("big.pdf", body)
