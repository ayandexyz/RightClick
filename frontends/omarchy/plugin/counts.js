.pragma library

// Pure helpers for summarising per-file job state; kept free of QML so they can be tested with node.

function empty() {
  return { done: 0, skipped: 0, failed: 0, cancelled: 0, active: 0 }
}

function summarize(files) {
  var counts = empty()
  var list = files || []
  for (var i = 0; i < list.length; i++) {
    var state = list[i].state
    if (state === "done") counts.done++
    else if (state === "skipped") counts.skipped++
    else if (state === "failed") counts.failed++
    else if (state === "cancelled") counts.cancelled++
    else counts.active++
  }
  return counts
}

function add(a, b) {
  return {
    done: a.done + b.done,
    skipped: a.skipped + b.skipped,
    failed: a.failed + b.failed,
    cancelled: a.cancelled + b.cancelled,
    active: a.active + b.active
  }
}

// "3 done · 1 failed" — finished outcomes only, zero counts omitted.
function describe(counts) {
  var parts = []
  var names = ["done", "skipped", "failed", "cancelled"]
  for (var i = 0; i < names.length; i++) {
    if (counts[names[i]] > 0) parts.push(counts[names[i]] + " " + names[i])
  }
  return parts.join(" · ")
}
