# file-converter

Convert files from where you are: pick a file, choose a target format in a radial menu, done.
One backend, a native frontend per platform.

```
frontends/omarchy   layer-shell overlay + Hyprland keybind + file-manager action   (first)
frontends/ubuntu    GTK4/libadwaita + Nautilus action
frontends/windows   native Windows client
        │  NDJSON over Unix socket / named pipe   (docs/PROTOCOL.md)
backend/            fcd: daemon that owns routing, job queue and conversion engines
```

## Status

Skeleton and protocol draft only. See `docs/PROTOCOL.md`.

## Roadmap

1. `fcd`: `hello`, `targets`, `convert`, `cancel`, events (protocol v1); pick the conversion engines
2. Omarchy frontend: `fc-pick <files>` opens the overlay at the cursor; Hyprland bind and Nautilus/Thunar action
3. Ubuntu frontend
4. Windows frontend
