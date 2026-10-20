"""Turn the Studio's state into daemon requests.

Each builder returns ``(operation id, params)`` ready for ``run_operation``;
the caller supplies the files and the output options. The rules here mirror the
daemon's (``backend/internal/pdfops``), so a request the UI would accept is one
the daemon accepts and an impossible one is refused with a reason before a job
exists. The daemon stays the authority: these checks are for the user's
benefit, not a substitute for its own.
"""

from .pagespec import PageSpecError, parse_pages

MAX_SPLIT_PARTS = 200


class RequestError(ValueError):
    """A request the daemon would refuse; the message is safe to show."""


def edit_request(model, only_selected=False):
    """The ``pdf-edit`` request for the model's current page list.

    With ``only_selected`` the request keeps just the selected pages, in
    document order, which is how "extract these pages" is expressed.
    """
    items = [item for item in model.items if not only_selected or item.uid in model.selection]
    if not items:
        raise RequestError("Select one or more pages first" if only_selected else "The document has no pages")
    if all(item.blank for item in items):
        raise RequestError("At least one original page is needed")
    return "pdf-edit", {"edit": [{"src": item.src, "rotate": item.rotate} for item in items]}


def split_parts(mode, value, page_count):
    """The pages of each part a split would produce, or why it cannot.

    ``mode`` is ``"ranges"`` (``value`` is text such as ``"1-3;4-6;7-"``) or
    ``"every"`` (``value`` is a whole number of pages per part). The Split view
    uses this for its live preview, and ``split_request`` for validation.
    """
    if mode == "ranges":
        groups = _range_groups(value, page_count)
    elif mode == "every":
        groups = _every_groups(value, page_count)
    else:
        raise RequestError(f"Unknown way to split: {mode!r}")
    if len(groups) > MAX_SPLIT_PARTS:
        raise RequestError(f"A split can make at most {MAX_SPLIT_PARTS} files")
    return groups


def _range_groups(text, page_count):
    if not isinstance(text, str) or not text.strip():
        raise RequestError("Enter the page ranges for each file, for example 1-3;4-6;7-")
    groups = []
    for number, raw in enumerate(text.split(";"), start=1):
        if not raw.strip():
            raise RequestError(f"Range {number} is empty")
        try:
            groups.append(parse_pages(raw, page_count))
        except PageSpecError as error:
            raise RequestError(f"Range {number}: {error}") from error
    return groups


def _every_groups(value, page_count):
    # bool is an int in Python; "True pages per file" is not a number the user typed.
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise RequestError("Pages per file must be a whole number of at least 1")
    if value >= page_count:
        raise RequestError("That would make a single file; choose fewer pages per file")
    return [list(range(start, min(start + value, page_count + 1))) for start in range(1, page_count + 1, value)]


def split_request(mode, value, page_count):
    """The split request for a mode and value, validated against the page count."""
    split_parts(mode, value, page_count)
    if mode == "ranges":
        groups = ";".join(part.strip() for part in value.split(";"))
        return "pdf-split-ranges", {"ranges": groups}
    return "pdf-split-every", {"every": value}


def merge_request(files, file_pages=None, page_counts=None):
    """The ``pdf-merge`` request for files in the order given.

    ``file_pages`` is one page spec per file (blank means every page). With
    ``page_counts`` each spec is also checked against its file, and an error
    names the file so a bad range in a long list is easy to find.
    """
    if len(files) < 2:
        raise RequestError("Merging needs at least two files")
    if file_pages is None:
        file_pages = [""] * len(files)
    if len(file_pages) != len(files):
        raise RequestError("Each file needs a page range entry, even if it is blank")
    cleaned = [spec.strip() for spec in file_pages]
    if page_counts is not None:
        for name, spec, count in zip(files, cleaned, page_counts):
            if not spec:
                continue
            try:
                parse_pages(spec, count)
            except PageSpecError as error:
                raise RequestError(f"{name}: {error}") from error
    if not any(cleaned):
        return "pdf-merge", {}
    return "pdf-merge", {"file_pages": cleaned}
