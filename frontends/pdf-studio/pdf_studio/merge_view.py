"""GTK-independent merge preview helpers."""

from .pagespec import PageSpecError, parse_pages


def merge_preview(files, file_pages):
    """Validate aligned per-file ranges and return ordered preview rows/errors.

    ``files`` may contain names, ``(name, page_count)`` pairs, or mappings
    with ``name``/``path`` and ``pages``/``page_count`` fields.  Names alone
    still get syntax validation; counts are used when available to catch a
    page past the end and to attribute the error to the right file.
    """
    files = list(files)
    file_pages = list(file_pages)
    rows = []
    errors = []
    if len(files) < 2:
        return {"rows": [], "errors": ["Merging needs at least two files"]}
    if len(file_pages) != len(files):
        return {"rows": [], "errors": ["Each file needs a page range entry, even if it is blank"]}

    for file, spec in zip(files, file_pages):
        name, page_count = _file_info(file)
        spec = spec.strip() if isinstance(spec, str) else str(spec)
        error = None
        if spec:
            try:
                parse_pages(spec, page_count or 0)
            except PageSpecError as raised:
                error = f"{name}: {raised}"
        row = {"file": name, "pages": spec, "error": error}
        rows.append(row)
        if error:
            errors.append(error)
    return {"rows": rows, "errors": errors}


def _file_info(file):
    if isinstance(file, (tuple, list)) and len(file) == 2:
        return str(file[0]), file[1]
    if isinstance(file, dict):
        return str(file.get("name", file.get("path", file))), file.get("page_count", file.get("pages"))
    return str(file), None
