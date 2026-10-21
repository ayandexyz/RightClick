import os
import re
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))
from pdf_studio.tools import GROUP_ORDER, TOOLS, RequestError, available_tools, tools_by_id

PROTOCOL = Path(__file__).parents[3] / "docs" / "PROTOCOL.md"
CONTEXT = {"page_count": 5}


def build(tool_id, **values):
    tool = tools_by_id()[tool_id]
    merged = {**tool.defaults(), **values}
    return tool.build(merged, CONTEXT)


class ToolSchemaTests(unittest.TestCase):
    def test_every_tool_is_declared_once_with_fields_and_a_known_group(self):
        ids = [tool.id for tool in TOOLS]
        self.assertEqual(len(ids), len(set(ids)))
        self.assertEqual(set(ids), {
            "pdf-stamp-text", "pdf-stamp-image", "pdf-page-numbers", "pdf-header-footer", "pdf-bates",
            "pdf-crop", "pdf-resize", "pdf-nup", "pdf-booklet", "pdf-encrypt", "pdf-permissions",
            "pdf-decrypt", "pdf-metadata",
        })
        for tool in TOOLS:
            self.assertTrue(tool.fields, tool.id)
            self.assertIn(tool.group, GROUP_ORDER, tool.id)
            self.assertEqual(tool.op, tool.id)

    def test_a_choice_default_is_one_of_its_choices(self):
        for tool in TOOLS:
            for f in tool.fields:
                if f.kind == "choice":
                    self.assertIn(f.default, [v for v, _ in f.choices], f"{tool.id}.{f.key}")
                if f.kind == "flags":
                    self.assertLessEqual(set(f.default), {v for v, _ in f.choices}, f"{tool.id}.{f.key}")

    def test_every_default_form_either_builds_or_names_a_required_field(self):
        """No tool starts out in a state that raises something other than a form error."""
        image = tempfile.NamedTemporaryFile(suffix=".png", delete=False)
        image.close()
        self.addCleanup(os.unlink, image.name)
        for tool in TOOLS:
            values = tool.defaults()
            if tool.id == "pdf-stamp-image":
                values["image"] = image.name
            try:
                tool.build(values, CONTEXT)
            except RequestError as error:
                self.assertIn(error.key, {f.key for f in tool.fields}, tool.id)

    def test_parameter_keys_are_documented_in_the_protocol(self):
        text = PROTOCOL.read_text()
        for tool in TOOLS:
            if f"`{tool.op}`" not in text:
                continue  # the daemon side is not documented yet
            op, params = build(tool.id, **self.required_for(tool))
            keys = set(params)
            for value in params.values():
                if isinstance(value, dict):
                    keys |= set(value)
            for key in keys:
                self.assertRegex(text, rf"\b{key}\b", f"{tool.id}: {key} is not in docs/PROTOCOL.md")

    def required_for(self, tool):
        values = {}
        if tool.id == "pdf-stamp-text":
            values["text"] = "x"
        if tool.id == "pdf-stamp-image":
            values["image"] = __file__
        if tool.id == "pdf-header-footer":
            values["left"] = "x"
        if tool.id == "pdf-crop":
            values["top"] = 1
        if tool.id in ("pdf-encrypt",):
            values["user"] = "x"
        if tool.id == "pdf-permissions":
            values["owner"] = "x"
        if tool.id == "pdf-decrypt":
            values["password"] = "x"
        return values


class BuilderTests(unittest.TestCase):
    def test_text_stamp(self):
        op, params = build("pdf-stamp-text", text="DRAFT", color="#ABCDEF", opacity=40, rotation=45, pages="1,3")
        self.assertEqual(op, "pdf-stamp-text")
        self.assertEqual(params, {"stamp": {"text": "DRAFT", "position": "mc", "size": 48, "color": "#abcdef",
                                            "opacity": 40, "rotation": 45, "pages": "1,3", "on_top": True}})

    def test_image_stamp_needs_a_real_absolute_file(self):
        with tempfile.NamedTemporaryFile(suffix=".png") as image:
            op, params = build("pdf-stamp-image", image=image.name)
            self.assertEqual(op, "pdf-stamp-image")
            self.assertEqual(params["stamp"]["image"], image.name)
        for bad in ("logo.png", "/no/such/file.png", tempfile.gettempdir()):
            with self.assertRaises(RequestError) as caught:
                build("pdf-stamp-image", image=bad)
            self.assertEqual(caught.exception.key, "image", bad)

    def test_page_numbers(self):
        _, params = build("pdf-page-numbers", format="of-total", start=5, position="br")
        self.assertEqual(params, {"stamp": {"format": "of-total", "start": 5, "position": "br"}})

    def test_header_footer_keeps_only_the_zones_that_were_filled(self):
        _, params = build("pdf-header-footer", edge="bottom", right="Page %p of %P")
        self.assertEqual(params, {"stamp": {"edge": "bottom", "right": "Page %p of %P"}})

    def test_bates(self):
        _, params = build("pdf-bates", prefix="CASE-", start=99)
        self.assertEqual(params, {"stamp": {"prefix": "CASE-", "start": 99, "position": "br"}})

    def test_crop_and_resize(self):
        _, params = build("pdf-crop", unit="percent", top=5, left=2.5, pages="2-3")
        self.assertEqual(params, {"crop": {"unit": "percent", "top": 5, "right": 0, "bottom": 0, "left": 2.5, "pages": "2-3"}})
        _, params = build("pdf-resize", to="Letter")
        self.assertEqual(params, {"resize": {"to": "Letter"}})

    def test_layouts(self):
        _, params = build("pdf-nup", per_sheet=6, orientation="landscape", margin=10, border=True)
        self.assertEqual(params, {"layout": {"per_sheet": 6, "orientation": "landscape", "margin": 10, "border": True}})
        _, params = build("pdf-booklet", per_sheet=4)
        self.assertEqual(params, {"layout": {"per_sheet": 4}})

    def test_security_tools(self):
        _, params = build("pdf-encrypt", user="open", owner="manage", allow=["print"])
        self.assertEqual(params, {"encrypt_user": "open", "encrypt_owner": "manage", "permissions": ["print"]})
        _, params = build("pdf-encrypt", user="open")
        self.assertEqual(params["encrypt_owner"], "open", "a blank owner password reuses the opening one")
        self.assertEqual(sorted(params["permissions"]), ["annotate", "copy", "modify", "print"])
        _, params = build("pdf-permissions", owner="boss", allow=[])
        self.assertEqual(params, {"permissions_owner": "boss", "permissions": []})
        _, params = build("pdf-decrypt", password="secret")
        self.assertEqual(params, {"password": "secret"})

    def test_decrypt_starts_with_the_password_the_document_was_opened_with(self):
        self.assertEqual(tools_by_id()["pdf-decrypt"].defaults({"password": "opened"})["password"], "opened")
        self.assertEqual(tools_by_id()["pdf-decrypt"].defaults()["password"], "")


class ValidationTests(unittest.TestCase):
    def assertBlames(self, key, tool_id, **values):
        with self.assertRaises(RequestError) as caught:
            build(tool_id, **values)
        self.assertEqual(caught.exception.key, key, str(caught.exception))
        self.assertTrue(str(caught.exception))

    def test_text_stamp_fields(self):
        self.assertBlames("text", "pdf-stamp-text")  # required
        self.assertBlames("text", "pdf-stamp-text", text="a\nb")
        self.assertBlames("text", "pdf-stamp-text", text="x" * 201)
        self.assertBlames("opacity", "pdf-stamp-text", text="x", opacity=101)
        self.assertBlames("opacity", "pdf-stamp-text", text="x", opacity=0)
        self.assertBlames("size", "pdf-stamp-text", text="x", size=3)
        self.assertBlames("size", "pdf-stamp-text", text="x", size=12.5)
        self.assertBlames("color", "pdf-stamp-text", text="x", color="red")
        self.assertBlames("position", "pdf-stamp-text", text="x", position="middle")
        self.assertBlames("rotation", "pdf-stamp-text", text="x", rotation="lots")

    def test_page_selection_is_checked_against_the_document(self):
        self.assertBlames("pages", "pdf-crop", top=1, pages="9")
        self.assertBlames("pages", "pdf-stamp-text", text="x", pages="3-1")
        build("pdf-crop", top=1, pages="5")

    def test_dependent_fields(self):
        self.assertBlames("left", "pdf-header-footer")
        self.assertBlames("start", "pdf-bates", start=None)
        self.assertBlames("prefix", "pdf-bates", prefix="100%")
        self.assertBlames("top", "pdf-crop")  # no margin at all
        self.assertBlames("top", "pdf-crop", top=-1)
        self.assertBlames("per_sheet", "pdf-nup", per_sheet=3)
        self.assertBlames("per_sheet", "pdf-booklet", per_sheet=6)
        self.assertBlames("to", "pdf-resize", to="B5")
        self.assertBlames("unit", "pdf-crop", top=1, unit="cm")

    def test_passwords(self):
        self.assertBlames("user", "pdf-encrypt")
        self.assertBlames("user", "pdf-encrypt", user="p" * 128)
        self.assertBlames("owner", "pdf-permissions")
        self.assertBlames("password", "pdf-decrypt")
        self.assertBlames("allow", "pdf-encrypt", user="x", allow=["fly"])

    def test_messages_say_what_to_change(self):
        with self.assertRaises(RequestError) as caught:
            build("pdf-stamp-text", text="x", opacity=101)
        self.assertIn("at most 100", str(caught.exception))


class AvailabilityTests(unittest.TestCase):
    def ids(self, **kwargs):
        return [tool.id for tool in available_tools(**kwargs)]

    def test_everything_but_remove_password_is_offered_for_an_open_document(self):
        ids = self.ids()
        self.assertNotIn("pdf-decrypt", ids)
        self.assertEqual(len(ids), len(TOOLS) - 1)

    def test_remove_password_is_offered_only_for_a_document_opened_with_one(self):
        self.assertIn("pdf-decrypt", self.ids(locked=True))

    def test_a_tool_the_daemon_lacks_is_left_out(self):
        ids = self.ids(daemon_ops={"pdf-crop", "pdf-resize"})
        self.assertEqual(ids, ["pdf-crop", "pdf-resize"])

    def test_tools_come_in_group_order(self):
        groups = [tool.group for tool in available_tools(locked=True)]
        self.assertEqual(groups, sorted(groups, key=GROUP_ORDER.index))


if __name__ == "__main__":
    unittest.main()
