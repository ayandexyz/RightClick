"""Turning a drop on a page into a move, without any GTK."""


def drop_move(model, dragged_uid, target_uid, after):
    """What a drop of ``dragged_uid`` on ``target_uid`` should do.

    Returns ``(selection, index)``: the uids to move, and the ``index`` to give
    ``EditModel.move_to`` (a place among the pages that are not moving). Returns
    ``None`` when the drop changes nothing: onto a page that is itself moving,
    or onto the place the pages already are.

    A page dragged from outside the selection moves alone, and the selection
    becomes that page. A page dragged from inside it takes the whole selection.
    """
    uids = [item.uid for item in model.items]
    if dragged_uid not in uids or target_uid not in uids:
        return None
    moving = set(model.selection) if dragged_uid in model.selection else {dragged_uid}
    if target_uid in moving:
        return None
    rest = [uid for uid in uids if uid not in moving]
    index = rest.index(target_uid) + (1 if after else 0)
    chosen = [uid for uid in uids if uid in moving]
    if rest[:index] + chosen + rest[index:] == uids:
        return None
    return moving, index
