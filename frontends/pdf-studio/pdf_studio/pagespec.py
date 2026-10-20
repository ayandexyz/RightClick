"""Page-spec grammar, matching the daemon's parser.

A spec is comma-separated items, each ``N``, ``N-M``, ``N-`` (to the last page),
``-M`` (from page 1) or ``z`` (the last page). Pages are 1-based and whitespace
is ignored. The Studio uses this for its range box; the daemon has the
authoritative parser (``backend/internal/pdfops/pagespec.go``) and both run
against ``testdata/pagespec_cases.json`` so they stay in step.
"""

MAX_PAGES = 1000


class PageSpecError(ValueError):
    """A spec that does not follow the grammar or names pages that do not exist."""


def parse_pages(spec, page_count):
    """Return the pages a spec names, in the order written.

    ``page_count`` bounds every page; 0 skips the bound while still enforcing
    the size limit. Duplicates and overlaps are kept.
    """
    trimmed = spec.strip()
    if not trimmed:
        raise PageSpecError("page spec is empty")
    pages = []
    for item in trimmed.split(","):
        item = item.strip()
        if not item:
            raise PageSpecError(f"empty page range in spec {spec!r}")
        expanded = _expand(item, page_count, spec)
        if len(pages) + len(expanded) > MAX_PAGES:
            raise PageSpecError(f"page spec {spec!r} selects more than the maximum of {MAX_PAGES} pages")
        pages.extend(expanded)
    if not pages:
        raise PageSpecError(f"page spec {spec!r} selects no pages")
    return pages


def _expand(item, page_count, spec):
    def invalid(reason):
        return PageSpecError(f"invalid page range {item!r} in spec {spec!r}: {reason}")

    if item.lower() == "z":
        if page_count < 1:
            raise invalid("z needs a known page count")
        return [page_count]
    if item.count("-") > 1:
        raise invalid("too many hyphens")
    lower_text, has_range, upper_text = item.partition("-")
    lower_text, upper_text = lower_text.strip(), upper_text.strip()
    if has_range and not lower_text:
        lower_text = "1"
    lower = _bound(lower_text, item, page_count, spec)
    if not has_range:
        return [lower]
    if not upper_text:
        return _run(lower, page_count)
    upper = _bound(upper_text, item, page_count, spec)
    if upper < lower:
        raise invalid("range runs backwards")
    return _run(lower, upper)


def _run(lower, upper):
    return list(range(lower, upper + 1)) if upper >= lower else []


def _bound(text, item, page_count, spec):
    def invalid(reason):
        return PageSpecError(f"invalid page range {item!r} in spec {spec!r}: {reason}")

    if not text:
        raise invalid("missing page number")
    if text.lower() == "z":
        if page_count < 1:
            raise invalid("z needs a known page count")
        return page_count
    if not (text.isascii() and text.isdigit()):
        raise invalid("page number must be a positive integer")
    number = int(text)
    if number < 1:
        raise invalid("page numbers start at 1")
    if page_count > 0 and number > page_count:
        raise invalid(f"page {number} is past the last page ({page_count})")
    return number


def format_pages(pages):
    """Write pages back as a canonical spec: sorted, unique, runs of three or
    more collapsed to ``a-b``."""
    ordered = sorted(set(pages))
    parts = []
    index = 0
    while index < len(ordered):
        start = end = ordered[index]
        while index + 1 < len(ordered) and ordered[index + 1] == end + 1:
            index += 1
            end = ordered[index]
        if end - start >= 2:
            parts.append(f"{start}-{end}")
        else:
            parts.extend(str(page) for page in range(start, end + 1))
        index += 1
    return ",".join(parts)
