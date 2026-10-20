# File Converter Progress plugin

This hosted Omarchy `service` + `bar-widget` observes conversion events from the
user's `fcd` Unix socket and shows active progress in the topbar. It requires
Python 3 and the file-converter backend installed by `frontends/omarchy/install.sh`.

Install the backend first, then add this directory as an Omarchy plugin:

```sh
omarchy plugin add /absolute/path/to/file-converter/frontends/omarchy/plugin --enable
omarchy plugin validate /absolute/path/to/file-converter/frontends/omarchy/plugin
```

The service runs inside the existing Omarchy shell process and launches only a
small local Python socket bridge. It does not access credentials, network
services, or privileged operations.
