"""GTK-independent output destination helpers."""


def build_output_options(folder, conflict):
    options = {}
    if folder:
        options["output_dir"] = folder
    if conflict:
        options["on_conflict"] = conflict
    return options


__all__ = ["build_output_options"]
