"""User-facing wording for daemon error codes.

The Nautilus extension and the PDF Studio both report daemon errors, and the
wording must not drift between them, so it lives here rather than in either
frontend. Wording is user-facing: it says what happened and what the user can
do, never the raw daemon message unless there is nothing better to say.
"""


def friendly_error(code, message, name=None):
    """Return a sentence for a daemon error code.

    `code` is the protocol error code, `message` the daemon's own text, and
    `name` the file's base name when one is known, so the sentence can name the
    file without the caller having to format it.
    """
    subject = f"{name}: " if name else ""
    if code == "password_required":
        return f"{subject}this PDF is locked — passwords are not supported yet"
    if code == "invalid_pdf":
        if message:
            return f"{subject}this PDF could not be read: {message}"
        return f"{subject}this PDF could not be read"
    if message:
        return f"{subject}{message}"
    return f"{subject}the operation failed"