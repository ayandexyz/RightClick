"""PDF Studio: a page-level PDF editor that drives the file-converter daemon."""

import os
import sys


def _ensure_client_importable():
    """Find the shared ``fc_client`` package when it is not already importable.

    In the source tree it is ``frontends/common/fc_client``, two levels above
    this package; installed, it sits next to ``pdf_studio``. Either way the
    launcher needs no knowledge of the layout.
    """
    try:
        import fc_client  # noqa: F401

        return
    except ImportError:
        pass
    here = os.path.dirname(os.path.abspath(__file__))
    for candidate in (os.path.join(here, "..", "..", "common"), os.path.join(here, "..")):
        candidate = os.path.normpath(candidate)
        if os.path.isdir(os.path.join(candidate, "fc_client")):
            sys.path.append(candidate)
            return


_ensure_client_importable()
