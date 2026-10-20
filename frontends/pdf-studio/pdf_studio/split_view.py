"""GTK-independent split preview helpers."""

from .requests import RequestError, split_parts


def split_preview(page_count, mode, value):
    """Return the split parts, or a user-displayable validation error."""
    try:
        return {"parts": split_parts(mode, value, page_count), "error": None}
    except (RequestError, TypeError, ValueError) as error:
        return {"parts": [], "error": str(error)}
