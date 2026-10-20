"""GTK-independent asynchronous PDF thumbnail loading."""

from __future__ import annotations

import inspect
import threading
from collections import deque


MAX_BATCH = 20


class ThumbnailLoader:
    """Load PDF thumbnails on one worker without touching GTK.

    ``client_factory`` is called once for every request attempt, keeping each
    batch on its own daemon connection. ``callback`` receives ``(page, item)``
    for each returned thumbnail. ``error_callback`` receives ``(error, pages)``
    after a batch fails twice. Callbacks run on the worker thread; a GTK caller
    should hand them back to the main loop.
    """

    def __init__(self, client_factory, file, page_count, width=200, callback=None, error_callback=None):
        self._client_factory = client_factory
        self.file = file
        self.page_count = page_count
        self.width = width
        self._callback = callback or (lambda _page, _item: None)
        self._error_callback = error_callback or (lambda _error, _pages: None)
        self._condition = threading.Condition()
        self._queue = deque()
        self._queued = set()
        self._loaded = set()
        self._stopped = False
        self._worker = threading.Thread(target=self._run, name="pdf-thumbnail-loader", daemon=True)
        self._worker.start()

    def load(self, pages, visible_pages=()):
        """Queue missing ``pages``, putting visible pages first.

        Page numbers outside the document are ignored. Repeated pages are
        de-duplicated while queued, in flight, or already delivered.
        """
        visible = self._normalise(visible_pages)
        requested = self._normalise(pages)
        ordered = visible + [page for page in requested if page not in visible]
        with self._condition:
            if self._stopped:
                return
            missing = [page for page in ordered if page not in self._queued and page not in self._loaded]
            for start in range(0, len(missing), MAX_BATCH):
                batch = missing[start:start + MAX_BATCH]
                self._queue.append(batch)
                self._queued.update(batch)
            self._condition.notify()

    def stop(self):
        """Stop accepting work and suppress callbacks from work in progress."""
        with self._condition:
            if self._stopped:
                return
            self._stopped = True
            self._queue.clear()
            self._condition.notify_all()

    def _normalise(self, pages):
        result = []
        seen = set()
        for page in pages:
            if isinstance(page, bool) or not isinstance(page, int) or not 1 <= page <= self.page_count:
                continue
            if page not in seen:
                result.append(page)
                seen.add(page)
        return result

    def _run(self):
        while True:
            with self._condition:
                while not self._queue and not self._stopped:
                    self._condition.wait()
                if self._stopped:
                    return
                batch = self._queue.popleft()
            error = None
            result = None
            for _attempt in range(2):
                try:
                    result = self._request(batch)
                    error = None
                    break
                except Exception as caught:  # client implementations expose varied error types
                    error = caught
            with self._condition:
                self._queued.difference_update(batch)
                stopped = self._stopped
                if not error:
                    self._loaded.update(batch)
            if stopped:
                continue
            if error:
                self._error_callback(error, batch)
                continue
            for item in self._items(result):
                page = self._item_page(item)
                if page in batch:
                    self._callback(page, item)

    def _request(self, pages):
        client = self._client_factory()
        method = client.pdf_thumbnails
        try:
            parameters = inspect.signature(method).parameters
        except (TypeError, ValueError):
            parameters = {}
        if all(name in parameters for name in ("file", "pages", "width")):
            return method(file=self.file, pages=list(pages), width=self.width)
        return method(self.file, list(pages), self.width)

    @staticmethod
    def _items(result):
        if isinstance(result, dict):
            return result.get("pages", ())
        return result or ()

    @staticmethod
    def _item_page(item):
        if isinstance(item, dict):
            return item.get("page")
        return getattr(item, "page", None)
