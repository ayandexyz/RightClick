"""The password prompt for a locked PDF.

Adw.AlertDialog (libadwaita 1.5) with a Gtk.PasswordEntry as its extra child.
The dialog only collects the password; what to do with it, and what to say when
it does not work, belongs to the window.
"""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402


def ask_password(parent, name, on_done, error=""):
    """Ask for ``name``'s password; call ``on_done(password)`` or ``on_done(None)``.

    ``error`` is shown above the field, for a password that was just refused.
    The entry's text is read once, when the dialog is answered, and is not kept.
    """
    dialog = Adw.AlertDialog.new("Password required", f"Enter the password for {name}.")
    entry = Gtk.PasswordEntry(show_peek_icon=True, activates_default=True, hexpand=True)
    box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
    if error:
        message = Gtk.Label(label=error, xalign=0, wrap=True)
        message.add_css_class("error")
        box.append(message)
    box.append(entry)
    dialog.set_extra_child(box)
    dialog.add_response("cancel", "Cancel")
    dialog.add_response("unlock", "Unlock")
    dialog.set_response_appearance("unlock", Adw.ResponseAppearance.SUGGESTED)
    dialog.set_default_response("unlock")
    dialog.set_close_response("cancel")

    def answered(_dialog, response):
        password = entry.get_text() if response == "unlock" else ""
        entry.set_text("")
        on_done(password or None)

    dialog.connect("response", answered)
    dialog.present(parent)
    entry.grab_focus()
    return dialog
