"""Where an apply writes, and what happens when a name is taken."""

# (label shown, daemon on_conflict value); the first is the default.
CONFLICT_CHOICES = (
    ("Keep both", "rename"),
    ("Skip existing", "skip"),
    ("Replace", "overwrite"),
)


def build_output_options(folder, conflict):
    options = {}
    if folder:
        options["output_dir"] = folder
    if conflict:
        options["on_conflict"] = conflict
    return options


__all__ = ["CONFLICT_CHOICES", "build_output_options"]
