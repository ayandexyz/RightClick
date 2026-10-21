"""PDF Studio tools described as data, with no GTK.

A tool is a form: a list of typed fields and a builder that turns checked values
into the one ``run_operation`` request the daemon takes. The Tools tab renders
any tool from this description, so adding a tool means adding an entry here and
no widget code.

Everything a field accepts is checked here before a request is sent, and each
failure names the field, so the form can show the message under it. The daemon
checks again; this is for a useful message, not for trust.
"""

import os
import re
from dataclasses import dataclass
from typing import Any, Callable

from .pagespec import PageSpecError, parse_pages
from .requests import RequestError as _RequestError

# The longest password the daemon accepts, in bytes (the PDF limit).
MAX_PASSWORD_BYTES = 127

POSITIONS = (
    ("tl", "Top left"), ("tc", "Top centre"), ("tr", "Top right"),
    ("ml", "Middle left"), ("mc", "Centre"), ("mr", "Middle right"),
    ("bl", "Bottom left"), ("bc", "Bottom centre"), ("br", "Bottom right"),
)
PERMISSIONS = (("print", "Print"), ("copy", "Copy text and images"), ("modify", "Change the document"), ("annotate", "Add comments and fill forms"))


class RequestError(_RequestError):
    """A form value that cannot become a valid request.

    ``key`` is the field to blame and ``message`` a sentence that says what to
    change; ``str(error)`` is the message alone, ready to show.
    """

    def __init__(self, key, message):
        super().__init__(message)
        self.key = key
        self.message = message


@dataclass(frozen=True)
class Field:
    key: str
    label: str
    kind: str  # text, number, choice, colour, file, pages, switch, password, flags, regions, terms
    default: object = None
    required: bool = False
    help: str = ""
    minimum: float | None = None
    maximum: float | None = None
    integer: bool = False
    choices: tuple = ()  # (value, label) pairs for choice and flags


@dataclass(frozen=True)
class Tool:
    id: str
    label: str
    group: str
    op: str
    fields: tuple
    assemble: Callable[[dict[str, Any]], dict[str, Any]]
    params_keys: frozenset = frozenset()
    summary: str = ""
    only_locked: bool = False  # offered only for a document opened with a password

    def defaults(self, context=None):
        """The initial value of every field."""
        values = {f.key: f.default for f in self.fields}
        context = context or {}
        if self.id == "pdf-decrypt" and context.get("password"):
            values["password"] = context["password"]
        return values

    def build(self, values, context=None):
        """Check ``values`` and return ``(op, params)``, or raise RequestError."""
        context = context or {}
        clean = {}
        for f in self.fields:
            clean[f.key] = _clean(f, (values or {}).get(f.key, f.default), context)
        return self.op, self.assemble(clean)


# -- field checking ----------------------------------------------------------


def _blank(value):
    return value is None or (isinstance(value, str) and not value.strip())


def _clean(f, value, context):
    """Return ``value`` as the type the request needs, or raise RequestError."""
    kind = f.kind
    if kind == "switch":
        return bool(value)
    if kind == "flags":
        chosen = list(value or ())
        allowed = {v for v, _ in f.choices}
        for item in chosen:
            if item not in allowed:
                raise RequestError(f.key, f"{item!r} is not one of the options for {f.label}")
        return chosen
    if kind == "regions":
        return _regions(f, value or (), context)
    if kind == "terms":
        return _terms(f, value or (), context)
    if _blank(value):
        if f.required:
            raise RequestError(f.key, f"{f.label} is required")
        return {"number": None, "pages": "", "choice": f.default}.get(kind, "")
    if kind == "number":
        return _number(f, value)
    if kind == "choice":
        if value not in {v for v, _ in f.choices}:
            raise RequestError(f.key, f"choose one of the listed values for {f.label}")
        return value
    if kind == "colour":
        if not isinstance(value, str) or not re.fullmatch(r"#[0-9a-fA-F]{6}", value.strip()):
            raise RequestError(f.key, f"{f.label} must look like #rrggbb")
        return value.strip().lower()
    if kind == "file":
        if not isinstance(value, str) or not os.path.isabs(value):
            raise RequestError(f.key, f"{f.label} must be an absolute path")
        if not os.path.isfile(value):
            raise RequestError(f.key, f"{f.label} is not a file that exists")
        return value
    if kind == "pages":
        return _pages(f, value, context)
    if kind == "password":
        if not isinstance(value, str):
            raise RequestError(f.key, f"{f.label} must be text")
        if len(value.encode("utf-8")) > MAX_PASSWORD_BYTES:
            raise RequestError(f.key, f"{f.label} may be at most {MAX_PASSWORD_BYTES} bytes")
        return value
    # text
    if not isinstance(value, str):
        raise RequestError(f.key, f"{f.label} must be text")
    if "\n" in value or "\r" in value:
        raise RequestError(f.key, f"{f.label} must be one line")
    limit = int(f.maximum) if f.maximum is not None else 200
    if len(value) > limit:
        raise RequestError(f.key, f"{f.label} may be at most {limit} characters")
    return value


def _number(f, value):
    if isinstance(value, bool):
        raise RequestError(f.key, f"{f.label} must be a number")
    try:
        number = float(value)
    except (TypeError, ValueError):
        raise RequestError(f.key, f"{f.label} must be a number") from None
    if number != number or number in (float("inf"), float("-inf")):
        raise RequestError(f.key, f"{f.label} must be a number")
    if f.integer:
        if not number.is_integer():
            raise RequestError(f.key, f"{f.label} must be a whole number")
        number = int(number)
    elif number.is_integer():
        number = int(number)
    if f.minimum is not None and number < f.minimum:
        raise RequestError(f.key, f"{f.label} must be at least {f.minimum:g}")
    if f.maximum is not None and number > f.maximum:
        raise RequestError(f.key, f"{f.label} must be at most {f.maximum:g}")
    return number


def _pages(f, value, context):
    spec = str(value).strip()
    try:
        parse_pages(spec, int(context.get("page_count", 0)))
    except PageSpecError as error:
        raise RequestError(f.key, str(error)) from None
    return spec


def _regions(f, value, context):
    if not isinstance(value, (list, tuple)):
        raise RequestError(f.key, f"{f.label} must be a list of regions")
    if len(value) > 500:
        raise RequestError(f.key, f"{f.label} may contain at most 500 regions")
    page_count = int(context.get("page_count", 0))
    clean = []
    for index, region in enumerate(value, 1):
        if not isinstance(region, dict):
            raise RequestError(f.key, f"region {index} must be an object")
        try:
            page = int(region["page"])
            x, y, w, h = (float(region[name]) for name in ("x", "y", "w", "h"))
        except (KeyError, TypeError, ValueError):
            raise RequestError(f.key, f"region {index} must have page, x, y, w and h") from None
        if page < 1 or page > page_count or w <= 0 or h <= 0 or x < 0 or y < 0 or x + w > 1 or y + h > 1:
            raise RequestError(f.key, f"region {index} is outside the document")
        clean.append({"page": page, "x": x, "y": y, "w": w, "h": h})
    return clean


def _terms(f, value, _context):
    if isinstance(value, str):
        value = value.splitlines()
    if not isinstance(value, (list, tuple)):
        raise RequestError(f.key, f"{f.label} must be one term per line")
    terms = [str(term).strip() for term in value if str(term).strip()]
    if len(terms) > 50:
        raise RequestError(f.key, f"{f.label} may contain at most 50 terms")
    if any(len(term) < 1 or len(term) > 100 for term in terms):
        raise RequestError(f.key, f"each {f.label.lower()} must be 1-100 characters")
    return terms


# -- builders ----------------------------------------------------------------


def _stamp(clean, *keys):
    """The ``stamp`` object: the named fields, leaving out any left empty."""
    stamp = {}
    for key in keys:
        value = clean[key]
        if value is None or value == "":
            continue
        stamp[key] = value
    return {"stamp": stamp}


def _header_footer(clean):
    if not any(clean[zone] for zone in ("left", "centre", "right")):
        raise RequestError("left", "Fill in at least one of left, centre or right")
    return _stamp(clean, "edge", "left", "centre", "right", "pages")


def _bates(clean):
    if "%" in clean["prefix"]:
        raise RequestError("prefix", "The prefix cannot contain %")
    return _stamp(clean, "prefix", "start", "position", "pages")


def _crop(clean):
    margins = {side: clean[side] or 0 for side in ("top", "right", "bottom", "left")}
    if not any(margins.values()):
        raise RequestError("top", "Set at least one margin to remove")
    return {"crop": {"unit": clean["unit"], **margins, **({"pages": clean["pages"]} if clean["pages"] else {})}}


def _resize(clean):
    return {"resize": {"to": clean["to"], **({"pages": clean["pages"]} if clean["pages"] else {})}}


def _nup(clean):
    layout = {"per_sheet": clean["per_sheet"], "orientation": clean["orientation"], "margin": clean["margin"] or 0}
    if clean["border"]:
        layout["border"] = True
    return {"layout": layout}


def _booklet(clean):
    return {"layout": {"per_sheet": clean["per_sheet"]}}


def _encrypt(clean):
    # pdfcpu needs an owner password. A blank one reuses the opening password,
    # which the field's help says.
    owner = clean["owner"] or clean["user"]
    return {"encrypt_user": clean["user"], "encrypt_owner": owner, "permissions": clean["allow"]}


def _permissions(clean):
    return {"permissions_owner": clean["owner"], "permissions": clean["allow"]}


def _decrypt(clean):
    return {"password": clean["password"]}


def _metadata(clean):
    return {"metadata": {key: clean[key] for key in ("title", "author", "subject", "keywords")}}


def _redact(clean):
    if not clean["areas"] and not clean["terms"]:
        raise RequestError("areas", "Draw at least one box or add a search term")
    return {"redact": {"areas": clean["areas"], "terms": clean["terms"], "dpi": clean["dpi"]}}


def _f(key, label, kind, default=None, **options):
    return Field(key, label, kind, default, **options)


_POSITION = lambda default: _f("position", "Position", "choice", default, choices=POSITIONS)  # noqa: E731
_PAGES = _f("pages", "Pages", "pages", "", help="Leave empty for every page, or use a list like 1-3,7")
_ON_TOP = _f("on_top", "Draw over the page", "switch", True)

TOOLS = (
    Tool("pdf-stamp-text", "Text watermark", "Stamp and number", "pdf-stamp-text", (
        _f("text", "Text", "text", "", required=True, help="Latin letters, digits and punctuation. %p is the page number, %P the page count."),
        _POSITION("mc"),
        _f("size", "Size", "number", 48, minimum=4, maximum=200, integer=True),
        _f("color", "Colour", "colour", "#808080"),
        _f("opacity", "Opacity", "number", 50, minimum=1, maximum=100, integer=True, help="Percent"),
        _f("rotation", "Rotation", "number", 0, minimum=-180, maximum=180, integer=True, help="Degrees"),
        _PAGES, _ON_TOP,
    ), lambda c: _stamp(c, "text", "position", "size", "color", "opacity", "rotation", "pages", "on_top"), frozenset({"stamp"}),
        summary="Put text on the pages, such as DRAFT or CONFIDENTIAL."),
    Tool("pdf-stamp-image", "Image watermark", "Stamp and number", "pdf-stamp-image", (
        _f("image", "Image", "file", "", required=True, help="A PNG or JPEG file"),
        _POSITION("mc"),
        _f("opacity", "Opacity", "number", 50, minimum=1, maximum=100, integer=True, help="Percent"),
        _f("rotation", "Rotation", "number", 0, minimum=-180, maximum=180, integer=True, help="Degrees"),
        _PAGES, _ON_TOP,
    ), lambda c: _stamp(c, "image", "position", "opacity", "rotation", "pages", "on_top"), frozenset({"stamp"}),
        summary="Put a logo or picture on the pages."),
    Tool("pdf-page-numbers", "Page numbers", "Stamp and number", "pdf-page-numbers", (
        _f("format", "Format", "choice", "number", choices=(("number", "1, 2, 3"), ("of-total", "1 of N"))),
        _f("start", "First number", "number", 1, minimum=1, maximum=999999999, integer=True),
        _POSITION("bc"),
        _PAGES,
    ), lambda c: _stamp(c, "format", "start", "position", "pages"), frozenset({"stamp"}),
        summary="Number the pages."),
    Tool("pdf-header-footer", "Header and footer", "Stamp and number", "pdf-header-footer", (
        _f("edge", "Place at", "choice", "top", choices=(("top", "Top of the page"), ("bottom", "Bottom of the page"))),
        _f("left", "Left", "text", "", help="%p is the page number, %P the page count, %t the date"),
        _f("centre", "Centre", "text", ""),
        _f("right", "Right", "text", ""),
        _PAGES,
    ), _header_footer, frozenset({"stamp"}), summary="Repeat a line of text at the top or bottom of every page."),
    Tool("pdf-bates", "Bates numbers", "Stamp and number", "pdf-bates", (
        _f("prefix", "Prefix", "text", "", help="Text before the number, such as CASE-"),
        _f("start", "First number", "number", 1, required=True, minimum=1, maximum=999999999, integer=True),
        _POSITION("br"),
        _PAGES,
    ), _bates, frozenset({"stamp"}), summary="Stamp every page with a running reference number."),
    Tool("pdf-crop", "Crop margins", "Page layout", "pdf-crop", (
        _f("unit", "Unit", "choice", "mm", choices=(("mm", "Millimetres"), ("pt", "Points"), ("percent", "Percent"))),
        _f("top", "Top", "number", 0, minimum=0, maximum=100000),
        _f("right", "Right", "number", 0, minimum=0, maximum=100000),
        _f("bottom", "Bottom", "number", 0, minimum=0, maximum=100000),
        _f("left", "Left", "number", 0, minimum=0, maximum=100000),
        _PAGES,
    ), _crop, frozenset({"crop"}), summary="Trim the edges of the pages. The content is hidden, not deleted."),
    Tool("pdf-resize", "Resize pages", "Page layout", "pdf-resize", (
        _f("to", "Paper size", "choice", "A4", choices=tuple((v, v) for v in ("A4", "A3", "A5", "Letter", "Legal"))),
        _PAGES,
    ), _resize, frozenset({"resize"}), summary="Fit each page onto a standard paper size, keeping its proportions."),
    Tool("pdf-nup", "Several pages per sheet", "Page layout", "pdf-nup", (
        _f("per_sheet", "Pages per sheet", "choice", 4, choices=tuple((n, str(n)) for n in (2, 4, 6, 9))),
        _f("orientation", "Sheet", "choice", "portrait", choices=(("portrait", "Portrait"), ("landscape", "Landscape"))),
        _f("margin", "Margin", "number", 0, minimum=0, maximum=200, integer=True, help="Points"),
        _f("border", "Draw a border around each page", "switch", False),
    ), _nup, frozenset({"layout"}), summary="Print several pages on each sheet."),
    Tool("pdf-booklet", "Booklet", "Page layout", "pdf-booklet", (
        _f("per_sheet", "Pages per sheet", "choice", 2, choices=((2, "2 (fold in the middle)"), (4, "4"))),
    ), _booklet, frozenset({"layout"}), summary="Reorder pages so a double-sided print folds into a booklet. The last sheet is padded with blanks."),
    Tool("pdf-encrypt", "Password-protect", "Security", "pdf-encrypt", (
        _f("user", "Password to open", "password", "", required=True),
        _f("owner", "Password to change settings", "password", "", help="Leave empty to use the same password"),
        _f("allow", "Readers may", "flags", tuple(v for v, _ in PERMISSIONS), choices=PERMISSIONS),
    ), _encrypt, frozenset({"encrypt_user", "encrypt_owner", "permissions"}), summary="Ask for a password to open the PDF (AES-256)."),
    Tool("pdf-permissions", "Restrict what readers can do", "Security", "pdf-permissions", (
        _f("owner", "Owner password", "password", "", required=True, help="Needed to lift the restrictions later"),
        _f("allow", "Readers may", "flags", ("print",), choices=PERMISSIONS),
    ), _permissions, frozenset({"permissions_owner", "permissions"}), summary="Anyone can open it, but only the owner can print, copy or change it as chosen."),
    Tool("pdf-decrypt", "Remove password", "Security", "pdf-decrypt", (
        _f("password", "Password", "password", "", required=True),
    ), _decrypt, frozenset({"password"}), summary="Save a copy that opens without a password.", only_locked=True),
    Tool("pdf-metadata", "Document properties", "Document", "pdf-metadata", (
        _f("title", "Title", "text", ""), _f("author", "Author", "text", ""),
        _f("subject", "Subject", "text", ""), _f("keywords", "Keywords", "text", ""),
    ), _metadata, frozenset({"metadata"}), summary="Edit the title, author, subject and keywords."),
    Tool("pdf-redact", "Redact content", "Security", "pdf-redact", (
        _f("areas", "Regions", "regions", (), help="Page-relative regions to permanently cover"),
        _f("terms", "Search terms", "terms", (), help="One term per line"),
        _f("dpi", "Raster quality", "number", 150, minimum=72, maximum=300, integer=True),
    ), _redact, frozenset({"redact"}), summary="Permanently remove visible content by rasterising every page."),
)

GROUP_ORDER = ("Stamp and number", "Page layout", "Security", "Document")


def tools_by_id():
    return {tool.id: tool for tool in TOOLS}


def available_tools(daemon_ops=None, locked=False):
    """The tools to offer, in display order.

    ``daemon_ops`` is the set of operation ids the daemon reports as enabled;
    a tool whose operation is missing (an older daemon, or an engine that is not
    installed) is left out instead of failing when applied. ``None`` offers
    every tool. A tool marked ``only_locked`` is offered only for a document
    that was opened with a password.
    """
    offered = []
    for tool in TOOLS:
        if daemon_ops is not None and tool.op not in daemon_ops:
            continue
        if tool.only_locked and not locked:
            continue
        offered.append(tool)
    offered.sort(key=lambda tool: GROUP_ORDER.index(tool.group))
    return offered
